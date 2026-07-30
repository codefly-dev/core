package broker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"testing"
	"time"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
	"github.com/codefly-dev/core/provider/broker"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/credentials"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
	"github.com/stretchr/testify/require"
)

// brokerManifest is a Stripe-like provider manifest whose origin rule admits a
// loopback test server. It declares a read-only observe request and a mutating
// create request that returns a capturable secret.
const brokerManifest = `
schema_version: codefly.provider-manifest/v0
protocol_version: codefly.provider/v0
state_schema_versions: [1]
agent:
  kind: codefly:provider
  publisher: codefly.dev
  name: fixture
  version: 1.2.3
default_deletion_policy: retain
permissions:
  required:
    - id: account-create
      action: account.manage
      resource: "provider:fixture/${workspace}/${environment}/${binding}/account"
      resource_type: account
      reason: Reconcile the declared account.
      risk: high
      credential_purpose: management
  optional:
    - id: account-observe
      action: account.observe
      resource: "provider:fixture/${workspace}/${environment}/${binding}/account"
      resource_type: account
      reason: Observe the declared account.
      risk: low
      credential_purpose: management
resource_types:
  - id: account
    actions: [create, update, replace, delete, import, manual, blocked, no-op, project-output, observe]
    import_identity: [account_id]
    supports_replace: true
    supports_delete: true
requests:
  - id: account.observe
    permissions: [account-observe]
    resource_type: account
    action: observe
    origin_rule: api
    operation: observe
    method: GET
    path_template: /v1/accounts/{account_id}
    remote_id_parameters: [account_id]
    allowed_query_fields: [expand]
    request_byte_budget: 4096
    response_byte_budget: 65536
    read_only: true
    response_schema: account
    credential_purposes: [management]
  - id: account.create
    permissions: [account-create]
    resource_type: account
    action: create
    origin_rule: api
    operation: create
    method: POST
    path_template: /v1/accounts
    remote_id_parameters: []
    allowed_body_fields: [name, enabled]
    ownership_body_fields: [name]
    request_byte_budget: 8192
    response_byte_budget: 65536
    read_only: false
    response_schema: account
    credential_purposes: [management]
origin_rules:
  - id: api
    defaults: [http://localhost:8080]
    schemes: [http]
    host_patterns: [localhost]
    ports: [8080]
    binding_override: within-rule
    private_network_classes: [loopback]
credential_purposes:
  - id: management
    minimum_scope: Manage only the bound account.
    permitted_consumer: management
  - id: runtime
    minimum_scope: Use the bound runtime account.
    permitted_consumer: runtime
response_schemas:
  - id: account
    fields:
      - selector: {version: v1, path: "$.id"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.secret"}
        disposition: CAPTURE_TO_SINK
        purpose: runtime
      - selector: {version: v1, path: "$.metadata.internal"}
        disposition: SUPPRESS_REPORT_PRESENCE
sandbox:
  network: deny
state:
  schema_versions: [1]
  import_identity: true
  replace: true
  delete: true
  stepwise_upgrade: true
diagnostic_namespace: provider.fixture.
`

const (
	remoteID     = "acct_123"
	poisonSecret = "sk_live_1234567890abcdef"
)

func loopbackOrigin() urlguard.Origin {
	return urlguard.Origin{Scheme: "http", Host: "localhost", Port: 8080}
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

func fakeDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func descriptorDigest(t *testing.T, m *manifest.Manifest, id string) string {
	t.Helper()
	for _, descriptor := range m.Requests {
		if descriptor.ID == id {
			digest, err := manifest.RequestDescriptorDigest(descriptor)
			require.NoError(t, err)
			return digest
		}
	}
	t.Fatalf("descriptor %q not found", id)
	return ""
}

func pubString(value string) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: value}}
}

func boundCreateRequest(t *testing.T, m *manifest.Manifest, origin *providerv0.AdmittedOrigin) *providerv0.PlannedRequest {
	t.Helper()
	request := &providerv0.PlannedRequest{
		RequestDescriptorId:     "account.create",
		RequestDescriptorDigest: descriptorDigest(t, m, "account.create"),
		Method:                  providerv0.HTTPMethod_HTTP_METHOD_POST,
		AdmittedOriginDigest:    origin.GetAdmissionDigest(),
		Body:                    map[string]*providerv0.PublicValue{"name": pubString(remoteID)},
		CredentialPurposes:      []providerv0.CredentialPurpose{providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT},
		ResponsePolicyDigest:    fakeDigest("account-response-policy"),
		IdempotencyKey:          "idem-1",
	}
	bound, err := canonical.BindPlannedRequestDigest(request)
	require.NoError(t, err)
	return bound
}

func boundObserveRequest(t *testing.T, m *manifest.Manifest, origin *providerv0.AdmittedOrigin, accountID string) *providerv0.PlannedRequest {
	t.Helper()
	request := &providerv0.PlannedRequest{
		RequestDescriptorId:     "account.observe",
		RequestDescriptorDigest: descriptorDigest(t, m, "account.observe"),
		Method:                  providerv0.HTTPMethod_HTTP_METHOD_GET,
		AdmittedOriginDigest:    origin.GetAdmissionDigest(),
		PathParameters:          map[string]*providerv0.PublicValue{"account_id": pubString(accountID)},
		CredentialPurposes:      []providerv0.CredentialPurpose{providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT},
		ResponsePolicyDigest:    fakeDigest("account-response-policy"),
	}
	bound, err := canonical.BindPlannedRequestDigest(request)
	require.NoError(t, err)
	return bound
}

func createAction(t *testing.T, requests ...*providerv0.PlannedRequest) *providerv0.PlanAction {
	t.Helper()
	action := &providerv0.PlanAction{
		ActionId:            "a1",
		Position:            0,
		Type:                providerv0.ActionType_ACTION_TYPE_CREATE,
		ResourceType:        "account",
		ProspectiveRemoteId: remoteID,
		Ownership:           providerv0.Ownership_OWNERSHIP_OWNED,
		Requests:            requests,
	}
	require.NoError(t, canonical.ValidatePlanAction(action))
	return action
}

func binding() *providerv0.BindingAddress {
	return &providerv0.BindingAddress{WorkspaceId: "ws", EnvironmentId: "env", BindingId: "bind"}
}

func operation() *providerv0.OperationIdentity {
	return &providerv0.OperationIdentity{OperationId: "op1", AttemptId: "att1", ActionId: "a1", PlanId: "plan1"}
}

func budget() *providerv0.RequestBudget {
	return &providerv0.RequestBudget{RequestCount: 4, RequestBytes: 8192, ResponseBytes: 65536}
}

// fakeCheckpointer returns a preset durable checkpoint.
type fakeCheckpointer struct {
	checkpoint *providerv0.ActionCheckpoint
}

func (c *fakeCheckpointer) Latest(_ context.Context, _ *providerv0.OperationIdentity) (*providerv0.ActionCheckpoint, error) {
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

// dialClientFor returns a ClientFor that dials the given address regardless of
// the request URL, so tests reach a loopback server through the real transport
// path (tracing included) without depending on DNS or the manifest port.
func dialClientFor(addr string) func(urlguard.Origin, urlguard.Resolution) *http.Client {
	return func(_ urlguard.Origin, _ urlguard.Resolution) *http.Client {
		return &http.Client{
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
}

// harness wires a broker session with a mint credential handle and a durable
// checkpoint for the create request.
type harness struct {
	manifest    *manifest.Manifest
	action      *providerv0.PlanAction
	vault       *credentials.Vault
	sink        *recordingSink
	origin      *providerv0.AdmittedOrigin
	create      *providerv0.PlannedRequest
	checkpoints *fakeCheckpointer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	m, err := manifest.Load([]byte(brokerManifest))
	require.NoError(t, err)
	origin := admittedOrigin(t)
	create := boundCreateRequest(t, m, origin)
	action := createAction(t, create)
	return &harness{
		manifest:    m,
		action:      action,
		vault:       credentials.NewVault(),
		sink:        &recordingSink{},
		origin:      origin,
		create:      create,
		checkpoints: &fakeCheckpointer{checkpoint: checkpoint("cp1", "idem-1")},
	}
}

func (h *harness) mintHandle(t *testing.T, planned *providerv0.PlannedRequest, method providerv0.HTTPMethod) *providerv0.CredentialHandle {
	t.Helper()
	handle, err := h.vault.Mint(poisonSecret, credentials.Scope{
		Principal:      "user",
		Organization:   "org",
		ArtifactDigest: "sha256:aa",
		Binding:        binding(),
		PlanID:         "plan1",
		ActionID:       "a1",
		RequestDigest:  planned.GetRequestDigest(),
		Purpose:        providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		Origin:         loopbackOrigin(),
		Method:         method,
		Injection:      credentials.Injection{Kind: credentials.InjectBearer},
		MaxUses:        1,
		TTL:            time.Minute,
	})
	require.NoError(t, err)
	return handle
}

func (h *harness) executeRequest(handle *providerv0.CredentialHandle, planned *providerv0.PlannedRequest) *providerv0.ExecuteRequestRequest {
	return &providerv0.ExecuteRequestRequest{
		Context: &providerv0.ProviderContext{
			Offline:     &providerv0.OfflineProviderContext{Binding: binding()},
			Credentials: []*providerv0.CredentialHandle{handle},
			Operation:   operation(),
			Budget:      budget(),
		},
		RequestId:         "req-1",
		Request:           planned,
		Origin:            h.origin,
		CredentialHandles: []*providerv0.CredentialHandle{handle},
	}
}

func (h *harness) config(readOnly bool) broker.Config {
	return broker.Config{
		Manifest:    h.manifest,
		Action:      h.action,
		Binding:     binding(),
		Budget:      budget(),
		ReadOnly:    readOnly,
		Vault:       h.vault,
		Sink:        h.sink,
		Checkpoints: h.checkpoints,
		Deadlines:   urlguard.DefaultDeadlines(),
	}
}

// recordingSink records every captured secret.
type recordingSink struct {
	stored []string
}

func (s *recordingSink) Put(_ context.Context, target responsepolicy.SinkTarget, secret string) (*providerv0.OpaqueReference, error) {
	s.stored = append(s.stored, secret)
	return &providerv0.OpaqueReference{
		Reference: "capture://" + target.Key,
		Purpose:   target.Purpose,
	}, nil
}
