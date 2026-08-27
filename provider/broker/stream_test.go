package broker_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/broker"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/stretchr/testify/require"
)

// streamBody is an Anthropic-style SSE response for the account schema: the
// first event forwards $.id, the second carries a secret that must be captured,
// the third reports the presence of $.metadata.internal, and message_stop
// terminates the stream.
const streamBody = "event: message_start\n" +
	"data: {\"id\":\"acct_123\"}\n" +
	"\n" +
	"event: content_block_delta\n" +
	"data: {\"secret\":\"sk_live_1234567890abcdef\"}\n" +
	"\n" +
	"event: message_delta\n" +
	"data: {\"metadata\":{\"internal\":\"private\"}}\n" +
	"\n" +
	"event: message_stop\n" +
	"data: {}\n" +
	"\n"

func streamServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func eventTypes(response *providerv0.ExecuteRequestResponse) []string {
	types := make([]string, 0, len(response.GetEvents()))
	for _, event := range response.GetEvents() {
		types = append(types, event.GetEventType())
	}
	return types
}

// TestExecute_StreamFiltersEachEvent proves a live SSE response is delivered as
// an ordered event stream with the response policy applied per event: the delta
// text forwards, the secret is captured to the sink and never forwarded, and the
// terminal event is the last one.
func TestExecute_StreamFiltersEachEvent(t *testing.T) {
	h := newHarness(t)
	server := streamServer(t, streamBody)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	handle := h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST)
	response, err := session.Execute(context.Background(), h.executeRequest(handle, h.create))
	require.NoError(t, err)

	require.Equal(t, providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, response.GetDelivery())
	require.Equal(t, []string{"message_start", "content_block_delta", "message_delta", "message_stop"}, eventTypes(response))

	// Only the last event is terminal.
	for i, event := range response.GetEvents() {
		require.Equal(t, i == len(response.GetEvents())-1, event.GetTerminal())
	}

	// $.id forwarded on the first event; the secret captured on the second and
	// never forwarded anywhere; presence suppressed on the third.
	require.Equal(t, remoteID, response.GetEvents()[0].GetForwarded()[0].GetValue().GetStringValue())
	require.Len(t, response.GetEvents()[1].GetCaptures(), 1)
	require.Equal(t, []string{poisonSecret}, h.sink.stored)
	require.Equal(t, []string{"$.metadata.internal"}, response.GetEvents()[2].GetSuppressedPresence())

	// The captured secret arms the session capture gate through the aggregate.
	require.Len(t, response.GetCaptures(), 1)
}

// TestExecute_StreamRecordThenReplay records a live SSE stream, then replays it
// through the same admission path with no network reachable: replay reproduces
// the exact ordered events and terminal event deterministically, and the
// cassette never persists the secret.
func TestExecute_StreamRecordThenReplay(t *testing.T) {
	rec := newHarness(t)
	server := streamServer(t, streamBody)
	recordCassette := cassette.New(cassette.ModeRecord, "1.2.3")
	recCfg := rec.config(false)
	recCfg.ClientFor = dialClientFor(serverAddr(t, server))
	recCfg.Cassette = recordCassette
	recSession, err := broker.New(recCfg)
	require.NoError(t, err)

	recorded, err := recSession.Execute(context.Background(), rec.executeRequest(rec.mintHandle(t, rec.create, providerv0.HTTPMethod_HTTP_METHOD_POST), rec.create))
	require.NoError(t, err)

	data, err := recordCassette.Marshal()
	require.NoError(t, err)
	require.NotContains(t, string(data), poisonSecret, "cassette must not persist the streamed secret")

	replayCassette, err := cassette.Load(data, "1.2.3")
	require.NoError(t, err)
	rep := newHarness(t)
	repCfg := rep.config(false)
	repCfg.ClientFor = dialClientFor(reservedClosedAddr(t)) // proves no live fallback
	repCfg.Cassette = replayCassette
	repSession, err := broker.New(repCfg)
	require.NoError(t, err)

	replayed, err := repSession.Execute(context.Background(), rep.executeRequest(rep.mintHandle(t, rep.create, providerv0.HTTPMethod_HTTP_METHOD_POST), rep.create))
	require.NoError(t, err)

	// The replay produced no live capture (sink untouched) yet reproduced the
	// exact ordered events, forwarded values, and terminal event.
	require.Empty(t, rep.sink.stored)
	require.Equal(t, eventTypes(recorded), eventTypes(replayed))
	require.Equal(t, len(recorded.GetEvents()), len(replayed.GetEvents()))
	require.Equal(t,
		recorded.GetEvents()[0].GetForwarded()[0].GetValue().GetStringValue(),
		replayed.GetEvents()[0].GetForwarded()[0].GetValue().GetStringValue())
	require.True(t, replayed.GetEvents()[len(replayed.GetEvents())-1].GetTerminal())
	require.Equal(t, len(recorded.GetCaptures()), len(replayed.GetCaptures()))

	// Re-marshalling the replayed cassette is byte-stable.
	again, err := replayCassette.Marshal()
	require.NoError(t, err)
	require.Equal(t, string(data), string(again))
}

// TestExecute_StreamReplayMissingEntryDoesNotHitNetwork proves an unrecorded
// streaming request fails closed rather than reaching the network.
func TestExecute_StreamReplayMissingEntryDoesNotHitNetwork(t *testing.T) {
	empty := cassette.New(cassette.ModeReplay, "1.2.3")
	h := newHarness(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(reservedClosedAddr(t))
	cfg.Cassette = empty
	session, err := broker.New(cfg)
	require.NoError(t, err)

	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.ErrorContains(t, err, "does not fall back to live")
}

// TestExecute_StreamExceedsByteBudget proves the response-bytes budget bounds
// the whole stream, not a single frame: a stream larger than the budget fails
// closed mid-read and forwards nothing.
func TestExecute_StreamExceedsByteBudget(t *testing.T) {
	h := newHarness(t)
	server := streamServer(t, streamBody)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	tight := budget()
	tight.ResponseBytes = 32 // smaller than the whole stream
	cfg.Budget = tight
	session, err := broker.New(cfg)
	require.NoError(t, err)

	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.ErrorContains(t, err, "stream exceeds byte budget")
	require.Empty(t, h.sink.stored)
}
