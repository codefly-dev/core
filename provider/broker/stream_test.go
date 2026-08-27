package broker_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

	// The capture lives only on its event — it is not duplicated onto the
	// top-level captures — yet it still arms the session capture gate: a second
	// request against the same checkpoint is refused until a newer one
	// acknowledges the capture.
	require.Empty(t, response.GetCaptures())
	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.ErrorContains(t, err, "capture must be checkpointed")
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

// TestExecute_StreamDeliversEventsLive proves the streaming path is incremental,
// not buffered: the server holds back the tail of the stream until the caller's
// OnStreamEvent callback has already received the first event. A buffered
// implementation would never invoke the callback (it reads the whole body
// first), so the server would block and Execute would deadlock — the watchdog
// timeout fails the test in that case.
func TestExecute_StreamDeliversEventsLive(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"id\":\"acct_123\"}\n\n")
		flusher.Flush()
		select {
		case <-released:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
		flusher.Flush()
	}))
	t.Cleanup(server.Close)

	h := newHarness(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	var (
		mu   sync.Mutex
		seen []string
		once sync.Once
	)
	cfg.OnStreamEvent = func(event *providerv0.FilteredEvent) error {
		mu.Lock()
		seen = append(seen, event.GetEventType())
		mu.Unlock()
		once.Do(func() { close(released) }) // the first event unblocks the tail
		return nil
	}
	session, err := broker.New(cfg)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, execErr := session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
		done <- execErr
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not complete: events were not delivered incrementally")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"message_start", "message_stop"}, seen)
}

// TestExecute_StreamReplayDeliversEventsLive proves replay is delivery-identical:
// recorded events are surfaced through the same live callback, in order.
func TestExecute_StreamReplayDeliversEventsLive(t *testing.T) {
	rec := newHarness(t)
	server := streamServer(t, streamBody)
	recordCassette := cassette.New(cassette.ModeRecord, "1.2.3")
	recCfg := rec.config(false)
	recCfg.ClientFor = dialClientFor(serverAddr(t, server))
	recCfg.Cassette = recordCassette
	recSession, err := broker.New(recCfg)
	require.NoError(t, err)
	_, err = recSession.Execute(context.Background(), rec.executeRequest(rec.mintHandle(t, rec.create, providerv0.HTTPMethod_HTTP_METHOD_POST), rec.create))
	require.NoError(t, err)
	data, err := recordCassette.Marshal()
	require.NoError(t, err)

	replayCassette, err := cassette.Load(data, "1.2.3")
	require.NoError(t, err)
	rep := newHarness(t)
	repCfg := rep.config(false)
	repCfg.ClientFor = dialClientFor(reservedClosedAddr(t))
	repCfg.Cassette = replayCassette
	var seen []string
	repCfg.OnStreamEvent = func(event *providerv0.FilteredEvent) error {
		seen = append(seen, event.GetEventType())
		return nil
	}
	repSession, err := broker.New(repCfg)
	require.NoError(t, err)
	_, err = repSession.Execute(context.Background(), rep.executeRequest(rep.mintHandle(t, rep.create, providerv0.HTTPMethod_HTTP_METHOD_POST), rep.create))
	require.NoError(t, err)
	require.Equal(t, []string{"message_start", "content_block_delta", "message_delta", "message_stop"}, seen)
}

// TestExecute_StreamIgnoresUnknownFields proves a benign non-event/non-data
// framing field (id, retry, or a vendor extension) is ignored per the SSE spec
// rather than failing the whole stream closed.
func TestExecute_StreamIgnoresUnknownFields(t *testing.T) {
	const body = "id: 42\n" +
		"retry: 1000\n" +
		"x-vendor-hint: ignore-me\n" +
		": this is a comment\n" +
		"event: message_start\n" +
		"data: {\"id\":\"acct_123\"}\n" +
		"\n"
	h := newHarness(t)
	server := streamServer(t, body)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	response, err := session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.NoError(t, err)
	require.Equal(t, []string{"message_start"}, eventTypes(response))
	require.Equal(t, remoteID, response.GetEvents()[0].GetForwarded()[0].GetValue().GetStringValue())
}

// TestExecute_StreamDoneSentinelTerminates proves the OpenAI-style `data: [DONE]`
// sentinel is delivered as the terminal event without being filtered as JSON.
func TestExecute_StreamDoneSentinelTerminates(t *testing.T) {
	const body = "data: {\"id\":\"acct_123\"}\n" +
		"\n" +
		"data: [DONE]\n" +
		"\n"
	h := newHarness(t)
	server := streamServer(t, body)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	session, err := broker.New(cfg)
	require.NoError(t, err)

	response, err := session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.NoError(t, err)
	require.Len(t, response.GetEvents(), 2)
	require.Equal(t, remoteID, response.GetEvents()[0].GetForwarded()[0].GetValue().GetStringValue())
	// The [DONE] event carries no filtered fields and is the terminal event.
	require.Empty(t, response.GetEvents()[1].GetForwarded())
	require.True(t, response.GetEvents()[1].GetTerminal())
}

// TestExecute_StreamReadIsBounded proves a stalled stream cannot hold the session
// open forever: with no budget deadline, MaxRequestDuration bounds the read and
// Execute fails closed promptly instead of blocking indefinitely.
func TestExecute_StreamReadIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // never send the body; wait for the client to give up
	}))
	t.Cleanup(server.Close)

	h := newHarness(t)
	cfg := h.config(false)
	cfg.ClientFor = dialClientFor(serverAddr(t, server))
	cfg.MaxRequestDuration = 200 * time.Millisecond
	session, err := broker.New(cfg)
	require.NoError(t, err)

	start := time.Now()
	_, err = session.Execute(context.Background(), h.executeRequest(h.mintHandle(t, h.create, providerv0.HTTPMethod_HTTP_METHOD_POST), h.create))
	require.Error(t, err)
	require.Less(t, time.Since(start), 3*time.Second, "read must be bounded by MaxRequestDuration")
}
