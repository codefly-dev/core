package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/broker"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// respondingServer replies to every request with a fixed status and body.
func respondingServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// Finding 1: a create request that omits the declared ownership body field must
// be rejected — omission is a bypass of identity binding, not a valid request.
func TestExecute_OwnershipBodyFieldOmittedRejected(t *testing.T) {
	m := mustLoad(t)
	origin := admittedOrigin(t)
	// A create request whose body omits the ownership field "name" entirely.
	request := &providerv0.PlannedRequest{
		RequestDescriptorId:     "account.create",
		RequestDescriptorDigest: descriptorDigest(t, m, "account.create"),
		Method:                  providerv0.HTTPMethod_HTTP_METHOD_POST,
		AdmittedOriginDigest:    origin.GetAdmissionDigest(),
		Body:                    map[string]*providerv0.PublicValue{}, // no "name"
		CredentialPurposes:      []providerv0.CredentialPurpose{providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT},
		ResponsePolicyDigest:    fakeDigest("account-response-policy"),
		IdempotencyKey:          "idem-1",
	}
	bound, err := canonical.BindPlannedRequestDigest(request)
	require.NoError(t, err)

	h := newHarness(t)
	h.action = createAction(t, bound)
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, bound, providerv0.HTTPMethod_HTTP_METHOD_POST), bound))
	require.ErrorContains(t, err, "ownership body field \"name\" is missing")
	require.Empty(t, h.sink.stored)
}

// Finding 2a: a non-2xx response surfaces its status with the untrusted error
// body dropped, rather than hard-erroring and losing the status.
func TestExecute_NonSuccessSurfacesStatusAndDropsBody(t *testing.T) {
	h := newHarness(t)
	body := fmt.Sprintf(`{"error":{"message":"bad","hint":%q}}`, poisonSecret)
	server := respondingServer(t, http.StatusBadRequest, "application/json", body)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	response, err := session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.NoError(t, err)
	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, response.GetDelivery())
	require.Equal(t, uint32(http.StatusBadRequest), response.GetStatusCode())
	require.Equal(t, providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE, response.GetCertainty())
	require.Empty(t, response.GetForwarded())
	require.Empty(t, response.GetCaptures())

	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), poisonSecret)
}

// Finding 2b: a bodyless successful response (204, no schema-required field) is
// surfaced by status alone instead of failing on an empty JSON parse.
func TestExecute_BodylessSuccessSurfacesStatus(t *testing.T) {
	m := mustLoad(t)
	origin := admittedOrigin(t)
	del := boundDeleteRequest(t, m, origin)

	h := newHarness(t)
	h.action = deleteAction(t, del)
	h.checkpoints.checkpoint = checkpoint("cp1", "idem-del")
	server := respondingServer(t, http.StatusNoContent, "", "")
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	response, err := session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, del, providerv0.HTTPMethod_HTTP_METHOD_DELETE), del))
	require.NoError(t, err)
	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, response.GetDelivery())
	require.Equal(t, uint32(http.StatusNoContent), response.GetStatusCode())
	require.Empty(t, response.GetForwarded())
}

// Finding 2c: a bodyless response where the schema requires a field is drift and
// fails closed — a vendor cannot skip a required capture by returning 204.
func TestExecute_BodylessSuccessWithRequiredCaptureFailsClosed(t *testing.T) {
	h := newHarness(t)
	server := respondingServer(t, http.StatusNoContent, "", "")
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.ErrorContains(t, err, "schema requires fields")
	require.Empty(t, h.sink.stored)
}

// Finding 3: the request budget deadline bounds the whole round trip, not just
// the pre-send checks. A response that never arrives before the deadline is
// reported as SENT_OUTCOME_UNKNOWN.
func TestExecute_BudgetDeadlineEnforcedOnRoundTrip(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // never respond before the deadline
	}))
	// Cleanups run LIFO: unblock the handler first, then close the server.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	h := newHarness(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	cfg.Budget = &providerv0.RequestBudget{
		RequestCount:  4,
		RequestBytes:  8192,
		ResponseBytes: 65536,
		Deadline:      timestamppb.New(time.Now().Add(200 * time.Millisecond)),
	}
	session, err := broker.New(cfg)
	require.NoError(t, err)

	response, err := session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.NoError(t, err)
	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN, response.GetDelivery())
}

// Finding 4: replay never resolves DNS. A cassette recorded for an unresolvable
// host replays successfully even with a resolver that fails every lookup, while
// a live session against the same host fails at resolution.
func TestExecute_ReplayDoesNotResolveDNS(t *testing.T) {
	failing := &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dns disabled")
		},
	}
	h := newHarnessOn(t, originConfig{scheme: "https", host: "unresolvable.invalid", port: 443, class: "public"})

	recorded := &providerv0.ExecuteRequestResponse{
		StatusCode: http.StatusCreated,
		Certainty:  providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		Forwarded:  []*providerv0.FilteredField{{Selector: "$.id", Value: pubString(remoteID)}},
	}
	recorder := cassette.New(cassette.ModeRecord, "1.2.3")
	require.NoError(t, recorder.Record(cassette.NewKey(h.create, h.admitted), http.StatusCreated, nil, recorded))
	data, err := recorder.Marshal()
	require.NoError(t, err)
	replay, err := cassette.Load(data, "1.2.3")
	require.NoError(t, err)

	replayCfg := h.config(false)
	replayCfg.Resolver = failing
	replayCfg.Cassette = replay
	replaySession, err := broker.New(replayCfg)
	require.NoError(t, err)

	response, err := replaySession.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.NoError(t, err, "replay must not depend on DNS")
	require.Equal(t, uint32(http.StatusCreated), response.GetStatusCode())

	// A live session against the same unresolvable host fails at resolution,
	// confirming resolution normally happens on the live path.
	liveCfg := h.config(false)
	liveCfg.Resolver = failing
	liveSession, err := broker.New(liveCfg)
	require.NoError(t, err)
	_, err = liveSession.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.Error(t, err)
}

// Finding 7: the request byte budget bounds the full outbound message, including
// host-owned headers and the injected credential, not just the JSON body.
func TestExecute_RequestByteBudgetCountsHeaders(t *testing.T) {
	h := newHarness(t)
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	// The create body alone is tiny; a 60-byte ceiling is only exceeded once the
	// request line, Host, and headers are counted.
	cfg.Budget = &providerv0.RequestBudget{RequestCount: 4, RequestBytes: 60, ResponseBytes: 65536}
	session, err := broker.New(cfg)
	require.NoError(t, err)

	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.ErrorContains(t, err, "exceeds byte budget")
	require.Empty(t, h.sink.stored)
}

// Finding 8: concurrent Execute calls on one session are serialized, so a
// single-callback budget admits exactly one request. Run under -race.
func TestExecute_ConcurrentCallsRespectBudget(t *testing.T) {
	h := newHarness(t)
	server := accountServer(t, nil)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	cfg.Budget = &providerv0.RequestBudget{RequestCount: 1, RequestBytes: 8192, ResponseBytes: 65536}
	session, err := broker.New(cfg)
	require.NoError(t, err)

	const workers = 8
	handles := make([]*providerv0.CredentialHandle, workers)
	for i := range handles {
		handles[i] = h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := range workers {
		wg.Add(1)
		go func(handle *providerv0.CredentialHandle) {
			defer wg.Done()
			_, err := session.Execute(context.Background(), h.executeRequest(handle, h.create))
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(handles[i])
	}
	wg.Wait()
	require.Equal(t, 1, successes, "a single-callback budget must admit exactly one request")
}
