package wool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// These are the record-level and compatibility rules, which need no backend.
// Everything that depends on a real span — an incoming instrumented RPC, a
// nested backend span, the on-wire join, sampling — is in wool/otel against the
// real OpenTelemetry SDK, because the first version of this change passed its
// fakes while being broken on every real path.
//
// The implementations below are NOT fakes of infrastructure: they are second
// implementations of wool's published Span/Tracer/TelemetryProvider contracts,
// covering shapes no real backend produces (a span that cannot name itself, a
// typed-nil span). The defects they pin were found with exactly these shapes.

// only returns the single record the sink received, failing otherwise. Declared
// here rather than beside capture in log_test.go so this file's additions stay
// self-contained.
func (c *capture) only(t *testing.T) *wool.Log {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.Len(t, c.logs, 1, "expected exactly one captured log record")
	return c.logs[0]
}

// anonymousSpan implements wool.Span only. A backend that cannot name its spans
// must keep working — that is the reason SpanIdentity is a separate, optional
// interface rather than two more methods on Span.
type anonymousSpan struct{ events int }

func (s *anonymousSpan) AddEvent(_ string, _ []*wool.LogField) { s.events++ }
func (s *anonymousSpan) End()                                  {}

// externalSpan is a disabled-span sentinel of the kind a downstream tracer
// returns as a typed nil: its AddEvent and End are nil-safe no-ops, while its
// identity getters read a field and so dereference the receiver.
type externalSpan struct {
	traceID string
	spanID  string
}

func (s *externalSpan) AddEvent(_ string, _ []*wool.LogField) {}
func (s *externalSpan) End()                                  {}
func (s *externalSpan) TraceID() string                       { return s.traceID }
func (s *externalSpan) SpanID() string                        { return s.spanID }

// endDerefSpan isolates the StartSpan guard: its getters are absent so the
// identity path cannot be what saves it, and its End dereferences the receiver.
// Without that guard, StartSpan hands this back as the func every caller defers.
type endDerefSpan struct{ ended bool }

func (s *endDerefSpan) AddEvent(_ string, _ []*wool.LogField) {}
func (s *endDerefSpan) End()                                  { s.ended = true }

type tracerOf struct{ span wool.Span }

func (t tracerOf) Start(ctx context.Context, _ string) (context.Context, wool.Span) {
	return ctx, t.span
}

type backendOf struct{ span wool.Span }

func (b backendOf) NewTracer(string) wool.Tracer   { return tracerOf{span: b.span} }
func (b backendOf) Shutdown(context.Context) error { return nil }

func woolInSpan(t *testing.T, span wool.Span) (*wool.Wool, *capture) {
	t.Helper()
	sink := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "correlation"}).
		WithLogger(sink).
		WithTelemetry(backendOf{span: span})
	w, end := wool.StartSpan(provider.Inject(context.Background()), "Operation")
	t.Cleanup(end)
	return w.WithLogger(sink), sink
}

// FINDING 1. A typed-nil span passes a comma-ok assertion and an `!= nil`
// check, so the previous head called into it and logging panicked. wool now
// normalizes it away at binding, which also protects AddEvent and End.
func TestATypedNilSpanDoesNotMakeLoggingPanic(t *testing.T) {
	var disabled *externalSpan // typed nil, carried in a non-nil interface
	w, sink := woolInSpan(t, disabled)

	require.NotPanics(t, func() { w.Info("handling request") })

	require.Len(t, sink.logs, 1, "the line must still reach the sink")
	require.Empty(t, sink.logs[0].TraceID)
	require.Empty(t, sink.logs[0].SpanID)
}

// The same span must not panic when the deferred end func runs either.
func TestATypedNilSpanDoesNotMakeTheEndFuncPanic(t *testing.T) {
	var disabled *externalSpan
	sink := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "end"}).
		WithLogger(sink).
		WithTelemetry(backendOf{span: disabled})

	w, end := wool.StartSpan(provider.Inject(context.Background()), "Operation")
	w.Info("handling request")

	require.NotPanics(t, end)
}

// THE POSITIVE FALLBACK CASE. A backend with no ContextIdentity must still get
// its ids onto the record through wool's own span. Without this, deleting the
// entire SpanIdentity fallback left every test in this package green — the
// review caught exactly that, and no amount of negative cases substitutes for
// one assertion that the path produces a real id.
func TestABackendWithoutAContextReaderStillStampsItsSpansIDs(t *testing.T) {
	w, sink := woolInSpan(t, &externalSpan{
		traceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		spanID:  "00f067aa0ba902b7",
	})

	w.Info("handling request")

	record := sink.only(t)
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", record.TraceID,
		"backendOf implements no ContextIdentity, so this can only have come from the span fallback")
	require.Equal(t, "00f067aa0ba902b7", record.SpanID)
}

// The End hazard, isolated. The getters are absent here, so only the StartSpan
// guard can prevent this.
func TestTheEndFuncIsNeverATypedNilSpansMethod(t *testing.T) {
	var disabled *endDerefSpan
	sink := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "end-deref"}).
		WithLogger(sink).
		WithTelemetry(backendOf{span: disabled})

	_, end := wool.StartSpan(provider.Inject(context.Background()), "Operation")

	require.NotPanics(t, end)
}

// FORWARD records, exercised through the real emitting methods rather than by
// populating a Log by hand: the ids belong on the record while the bytes pass
// through untouched.
func TestForwardAndWriteEmitVerbatimWhileTheRecordKeepsTheIDs(t *testing.T) {
	w, sink := woolInSpan(t, &externalSpan{
		traceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		spanID:  "00f067aa0ba902b7",
	})

	w.Forwardf("forwarded %s", "output")
	_, err := w.Write([]byte("written output"))
	require.NoError(t, err)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	require.Len(t, sink.logs, 2)
	for _, record := range sink.logs {
		require.Equal(t, wool.FORWARD, record.Level)
		require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", record.TraceID,
			"a forwarded line still carries its trace on the record")
		require.NotContains(t, record.String(), "trace_id",
			"but the rendered bytes pass through untouched")
	}
	require.Equal(t, "forwarded output", sink.logs[0].String())
	require.Equal(t, "written output", sink.logs[1].String())
}

// A backend that cannot name its spans keeps working, with no ids.
func TestASpanThatCannotNameItsTraceStillLogs(t *testing.T) {
	span := &anonymousSpan{}
	w, sink := woolInSpan(t, span)

	w.Info("handling request")

	require.Len(t, sink.logs, 1)
	require.Empty(t, sink.logs[0].TraceID)
	require.Empty(t, sink.logs[0].SpanID)
	require.Equal(t, 1, span.events, "the line still reaches the span as an event")
}

// A span reporting empty ids must put nothing on the line: "trace_id=" or an
// all-zero id is a well-formed reference to a trace that never existed.
func TestASpanWithNoIdentityAddsNothingToTheLine(t *testing.T) {
	w, sink := woolInSpan(t, &externalSpan{})

	w.Info("handling request")

	require.Len(t, sink.logs, 1)
	require.NotContains(t, sink.logs[0].String(), "trace_id")
	require.NotContains(t, sink.logs[0].String(), "span_id")
}

// With no telemetry at all, a local run's output keeps exactly its old shape.
func TestWithoutTelemetryTheLineIsUnchanged(t *testing.T) {
	w, sink := newWool(t, wool.INFO)

	w.Info("handling request", wool.Field("id", 42))

	require.Len(t, sink.logs, 1)
	require.Empty(t, sink.logs[0].TraceID)
	require.Equal(t, "(INFO) handling request id=42", sink.logs[0].String())
}

// The stdout line is one half of the join: a node-level collector reads
// container stdout, so the id has to be ON the rendered line.
func TestTheRenderedLineCarriesTheIDsLast(t *testing.T) {
	log := &wool.Log{
		Level:   wool.INFO,
		Message: "handling request",
		Fields:  []*wool.LogField{wool.Field("id", 42)},
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
	}

	line := log.String()

	require.Equal(t,
		"(INFO) handling request id=42 trace_id=4bf92f3577b34da6a3ce929d0e0e4736 span_id=00f067aa0ba902b7",
		line)
	require.Less(t, strings.Index(line, "handling request"), strings.Index(line, "trace_id="))
}

// FINDING 3. A FORWARD record is another process's output passing through, and
// String returns it verbatim — so its ids live on the record only. The docs now
// say so; this holds them to it, in both directions.
func TestForwardedOutputIsRenderedVerbatimAndCarriesIDsOnTheRecordOnly(t *testing.T) {
	log := &wool.Log{
		Level:   wool.FORWARD,
		Message: "forwarded output",
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
	}

	require.Equal(t, "forwarded output", log.String(),
		"forwarded bytes must pass through unchanged")

	data, err := json.Marshal(log)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", decoded["trace_id"],
		"a collector joins a forwarded line on the field, not the text")
	require.Equal(t, "00f067aa0ba902b7", decoded["span_id"])
}

// The JSON sink (agents write a JSON envelope to stderr) must use the
// conventional snake_case names a collector's parser looks for.
func TestTheJSONRecordUsesTheConventionalIDNames(t *testing.T) {
	data, err := json.Marshal(&wool.Log{
		Level:   wool.INFO,
		Message: "handling request",
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", decoded["trace_id"])
	require.Equal(t, "00f067aa0ba902b7", decoded["span_id"])
}

// Absent rather than present-and-empty, so a parser cannot read "" as a trace.
func TestTheJSONRecordOmitsAbsentIDs(t *testing.T) {
	data, err := json.Marshal(&wool.Log{Level: wool.INFO, Message: "handling request"})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotContains(t, decoded, "trace_id")
	require.NotContains(t, decoded, "span_id")
}

// Field filtering must not drop correlation: a line that survives filtering but
// loses its ids is no longer joinable.
func TestFieldFilteringKeepsCorrelation(t *testing.T) {
	log := &wool.Log{
		Level:   wool.INFO,
		Message: "handling request",
		Fields:  []*wool.LogField{wool.Field("kept", 1).Error(), wool.Field("dropped", 2).Trace()},
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
	}

	filtered := log.AtLevel(wool.WARN)

	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", filtered.TraceID)
	require.Equal(t, "00f067aa0ba902b7", filtered.SpanID)
	require.Len(t, filtered.Fields, 1)
}
