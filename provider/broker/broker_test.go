package broker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/broker"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/stretchr/testify/require"
)

func accountServer(t *testing.T, captured *http.Request) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			*captured = *r.Clone(context.Background())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"secret":%q,"metadata":{"internal":"private"}}`, remoteID, poisonSecret)
	}))
	t.Cleanup(server.Close)
	return server
}

func serverAddr(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return server.Listener.Addr().String()
}

func TestExecute_CreateFiltersAndCapturesSecret(t *testing.T) {
	h := newHarness(t)
	var sent http.Request
	server := accountServer(t, &sent)

	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	response, err := session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.NoError(t, err)

	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, response.GetDelivery())
	require.Equal(t, uint32(http.StatusOK), response.GetStatusCode())

	// The secret was captured to the sink and never forwarded.
	require.Equal(t, []string{poisonSecret}, h.sink.stored)
	require.Len(t, response.GetCaptures(), 1)
	require.Len(t, response.GetSuppressedPresence(), 1)

	// $.id is the only forwarded field.
	require.Len(t, response.GetForwarded(), 1)
	require.Equal(t, remoteID, response.GetForwarded()[0].GetValue().GetStringValue())

	// The poison secret is nowhere in the provider-facing response.
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), poisonSecret)

	// The host owned every header: Authorization is the injected bearer and no
	// caller/proxy headers exist.
	require.Equal(t, "Bearer "+poisonSecret, sent.Header.Get("Authorization"))
	require.Equal(t, "identity", sent.Header.Get("Accept-Encoding"))
	require.Equal(t, "idem-1", sent.Header.Get("Idempotency-Key"))
	require.Equal(t, "codefly-provider-broker", sent.Header.Get("User-Agent"))
}

func TestExecute_RejectsMutatingRequestInReadOnlyContext(t *testing.T) {
	h := newHarness(t)
	server := accountServer(t, nil)
	cfg := h.config(true) // read-only context
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.ErrorContains(t, err, "read-only")
	require.Empty(t, h.sink.stored)
}

func TestExecute_WrongResourceIDInPathRejected(t *testing.T) {
	m := mustLoad(t)
	origin := admittedOrigin(t)
	// Observe request whose path id is not the planned prospective id.
	observe := boundObserveRequest(t, m, origin, "acct_999")
	action := createAction(t, observe)

	h := newHarness(t)
	h.manifest, h.action, h.origin = m, action, origin
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, observe, providerv0.HTTPMethod_HTTP_METHOD_GET)
	_, err = session.Execute(context.Background(), h.executeRequest(handle, observe))
	require.ErrorContains(t, err, "planned remote id")
	require.Empty(t, h.sink.stored)
}

func TestExecute_MissingCheckpointRefusesSend(t *testing.T) {
	h := newHarness(t)
	h.checkpoints.checkpoint = nil // no durable checkpoint
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.ErrorContains(t, err, "durable checkpoint is required")
}

func TestExecute_BudgetExhausted(t *testing.T) {
	h := newHarness(t)
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.Budget = &providerv0.RequestBudget{RequestCount: 1, RequestBytes: 8192, ResponseBytes: 65536}
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.NoError(t, err)

	// Budget is now exhausted; a second call is refused before send.
	handle2 := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle2, h.create))
	require.ErrorContains(t, err, "budget is exhausted")
}

func TestExecute_TimeoutBeforeSendIsNotSent(t *testing.T) {
	h := newHarness(t)
	// Dial a port with no listener: connection refused before any bytes are sent.
	closed := reservedClosedAddr(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(closed)
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	response, err := session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.NoError(t, err)
	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT, response.GetDelivery())
}

func TestExecute_LostResponseIsSentOutcomeUnknown(t *testing.T) {
	// A server that reads the full request then closes without responding: the
	// request bytes crossed the boundary but no response returned.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				// Read the request bytes off the wire (a single read is enough
				// for a small request) and close without responding, so the
				// request crosses the boundary but no response returns.
				buffer := make([]byte, 4096)
				_, _ = conn.Read(buffer)
				_ = conn.Close()
			}()
		}
	}()

	h := newHarness(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(listener.Addr().String())
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	response, err := session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.NoError(t, err)
	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN, response.GetDelivery())
	require.Equal(t, providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_UNCERTAIN, response.GetCertainty())
}

func TestExecute_CaptureMustBeCheckpointedBeforeNext(t *testing.T) {
	h := newHarness(t)
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.NoError(t, err)
	require.Equal(t, []string{poisonSecret}, h.sink.stored)

	// The capture gate is armed: a second request against the same checkpoint is
	// refused until a newer checkpoint acknowledges the capture.
	handle2 := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle2, h.create))
	require.ErrorContains(t, err, "capture must be checkpointed")

	// A newer checkpoint releases the gate.
	h.checkpoints.checkpoint = checkpoint("cp2", "idem-1")
	handle3 := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	_, err = session.Execute(context.Background(), h.executeRequest(handle3, h.create))
	require.NoError(t, err)
}

func TestExecute_CredentialCannotBeReusedOnAnotherRequest(t *testing.T) {
	h := newHarness(t)
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	// Mint a handle for the create request, then try to spend it on a different
	// (observe) request: the request digest no longer matches the handle.
	m := mustLoad(t)
	observe := boundObserveRequest(t, m, h.origin, remoteID)
	h.action = createAction(t, h.create, observe)
	cfg = h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err = broker.New(cfg)
	require.NoError(t, err)

	createHandle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	// Present the create handle while executing the observe request.
	request := h.executeRequest(createHandle, observe)
	request.Request = observe
	_, err = session.Execute(context.Background(), request)
	require.Error(t, err)
	require.Empty(t, h.sink.stored)
}

func mustLoad(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load([]byte(brokerManifest))
	require.NoError(t, err)
	return m
}

// reservedClosedAddr returns a loopback address that is bound then immediately
// closed, so a dial to it is refused.
func reservedClosedAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}
