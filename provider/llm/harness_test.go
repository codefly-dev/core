package llm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
	"github.com/codefly-dev/core/provider/broker"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/codefly-dev/core/provider/credentials"
	"github.com/codefly-dev/core/provider/llm"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
	"github.com/stretchr/testify/require"
)

const (
	apiKey = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"
	model  = "claude-sonnet-4-5"
)

// policyD is the host-derived response-policy digest; its concrete value is
// opaque to the broker, which derives its own policy from the manifest, but it
// must be stable across record and replay.
var policyD = fakeDigest("llm-response-policy")

// testOrigin is a loopback origin so record mode resolves locally and the whole
// record/replay flow runs with no network.
func testOrigin() llm.Origin {
	return llm.Origin{Scheme: "http", Host: "localhost", Port: 8080, Class: "loopback"}
}

func urlguardOrigin() urlguard.Origin {
	o := testOrigin()
	return urlguard.Origin{Scheme: o.Scheme, Host: o.Host, Port: o.Port}
}

func loadManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := llm.Manifest(testOrigin())
	require.NoError(t, err)
	return m
}

func admittedOrigin(t *testing.T) *providerv0.AdmittedOrigin {
	t.Helper()
	origin := &providerv0.AdmittedOrigin{
		OriginRuleId:        "api",
		Scheme:              "http",
		Host:                "localhost",
		Port:                8080,
		PrivateNetworkClass: providerv0.PrivateNetworkClass_PRIVATE_NETWORK_CLASS_LOOPBACK,
	}
	digest, err := canonical.AdmittedOriginDigest(origin)
	require.NoError(t, err)
	origin.AdmissionDigest = digest
	return origin
}

func binding() *providerv0.BindingAddress {
	return &providerv0.BindingAddress{WorkspaceId: "ws", EnvironmentId: "env", BindingId: "bind"}
}

func operation() *providerv0.OperationIdentity {
	return &providerv0.OperationIdentity{OperationId: "op1", AttemptId: "att1", ActionId: "a1", PlanId: "plan1"}
}

func budget() *providerv0.RequestBudget {
	return &providerv0.RequestBudget{RequestCount: 4, RequestBytes: 262144, ResponseBytes: 1048576}
}

type fakeCheckpointer struct{ checkpoint *providerv0.ActionCheckpoint }

func (c *fakeCheckpointer) Latest(context.Context, *providerv0.OperationIdentity) (*providerv0.ActionCheckpoint, error) {
	return c.checkpoint, nil
}

func checkpoint(id, idempotencyKey string) *providerv0.ActionCheckpoint {
	return &providerv0.ActionCheckpoint{
		CheckpointId:   id,
		Operation:      operation(),
		Delivery:       providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT,
		IdempotencyKey: idempotencyKey,
	}
}

// nopSink satisfies the broker's Sink requirement; chat and embed declare no
// captures, so it is never called.
type nopSink struct{}

func (nopSink) Put(context.Context, responsepolicy.SinkTarget, string) (*providerv0.OpaqueReference, error) {
	return nil, nil
}

func fakeDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// dialClientFor dials addr regardless of the request URL so tests reach a
// loopback server through the real transport without depending on DNS.
func dialClientFor(addr string) func(urlguard.Origin, urlguard.Resolution) *http.Client {
	return func(urlguard.Origin, urlguard.Resolution) *http.Client {
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
}

func serverAddr(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return server.Listener.Addr().String()
}

func reservedClosedAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

// harness wires an admitted broker session for a single planned LLM request.
type harness struct {
	manifest *manifest.Manifest
	origin   *providerv0.AdmittedOrigin
	planned  *providerv0.PlannedRequest
	vault    *credentials.Vault
}

func newHarness(t *testing.T, planned *providerv0.PlannedRequest) *harness {
	t.Helper()
	return &harness{
		manifest: loadManifest(t),
		origin:   admittedOrigin(t),
		planned:  planned,
		vault:    credentials.NewVault(),
	}
}

func (h *harness) action(t *testing.T) *providerv0.PlanAction {
	t.Helper()
	action := &providerv0.PlanAction{
		ActionId:            "a1",
		Type:                providerv0.ActionType_ACTION_TYPE_CREATE,
		ResourceType:        "model",
		ProspectiveRemoteId: "messages",
		Ownership:           providerv0.Ownership_OWNERSHIP_OWNED,
		Requests:            []*providerv0.PlannedRequest{h.planned},
	}
	require.NoError(t, canonical.ValidatePlanAction(action))
	return action
}

func (h *harness) handle(t *testing.T) *providerv0.CredentialHandle {
	t.Helper()
	handle, err := h.vault.Mint(apiKey, credentials.Scope{
		Principal:      "user",
		Organization:   "org",
		ArtifactDigest: "sha256:aa",
		Binding:        binding(),
		PlanID:         "plan1",
		ActionID:       "a1",
		RequestDigest:  h.planned.GetRequestDigest(),
		Purpose:        providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		Origin:         urlguardOrigin(),
		Method:         providerv0.HTTPMethod_HTTP_METHOD_POST,
		Injection:      credentials.Injection{Kind: credentials.InjectHeader, Name: "x-api-key"},
		MaxUses:        1,
		TTL:            time.Minute,
	})
	require.NoError(t, err)
	return handle
}

func (h *harness) request(t *testing.T) *providerv0.ExecuteRequestRequest {
	t.Helper()
	handle := h.handle(t)
	return &providerv0.ExecuteRequestRequest{
		Context: &providerv0.ProviderContext{
			Offline:     &providerv0.OfflineProviderContext{Binding: binding()},
			Credentials: []*providerv0.CredentialHandle{handle},
			Operation:   operation(),
			Budget:      budget(),
		},
		RequestId:         "req-1",
		Request:           h.planned,
		Origin:            h.origin,
		CredentialHandles: []*providerv0.CredentialHandle{handle},
	}
}

func (h *harness) session(t *testing.T, addr string, cass *cassette.Cassette) *broker.Session {
	t.Helper()
	session, err := broker.New(broker.Config{
		Manifest:    h.manifest,
		Action:      h.action(t),
		Binding:     binding(),
		Budget:      budget(),
		Vault:       h.vault,
		Sink:        nopSink{},
		Checkpoints: &fakeCheckpointer{checkpoint: checkpoint("cp1", h.planned.GetIdempotencyKey())},
		Deadlines:   urlguard.DefaultDeadlines(),
		ClientFor:   dialClientFor(addr),
		Cassette:    cass,
	})
	require.NoError(t, err)
	return session
}
