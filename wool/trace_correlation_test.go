package wool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// The join key between a log stream and a trace stream is trace_id/span_id on
// the log record. These tests pin that contract at the wool layer, with fake
// backends, so it holds for any telemetry provider and not just OTEL. The
// end-to-end proof against a real OTEL span lives in wool/otel.

// identifiedSpan is a backend span that CAN name its trace: it implements both
// wool.Span and wool.SpanIdentity.
type identifiedSpan struct {
	traceID string
	spanID  string
	events  int
}

func (s *identifiedSpan) AddEvent(_ string, _ []*wool.LogField) { s.events++ }
func (s *identifiedSpan) End()                                  {}
func (s *identifiedSpan) TraceID() string                       { return s.traceID }
func (s *identifiedSpan) SpanID() string                        { return s.spanID }

// anonymousSpan is a backend span that CANNOT name its trace: it implements
// wool.Span only. Such a backend must keep working, with logs simply carrying
// no ids — this is why SpanIdentity is a separate, optional interface.
type anonymousSpan struct{ events int }

func (s *anonymousSpan) AddEvent(_ string, _ []*wool.LogField) { s.events++ }
func (s *anonymousSpan) End()                                  {}

type fakeTracer struct{ span wool.Span }

func (t *fakeTracer) Start(ctx context.Context, _ string) (context.Context, wool.Span) {
	return ctx, t.span
}

type fakeTelemetry struct{ span wool.Span }

func (f *fakeTelemetry) NewTracer(string) wool.Tracer   { return &fakeTracer{span: f.span} }
func (f *fakeTelemetry) Shutdown(context.Context) error { return nil }

// woolInSpan wires a provider whose tracer hands out the given span, and
// returns a Wool bound to it plus the sink that captured its records.
func woolInSpan(t *testing.T, span wool.Span) (*wool.Wool, *capture) {
	t.Helper()
	cap := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "trace-correlation"}).
		WithLogger(cap).
		WithTelemetry(&fakeTelemetry{span: span})
	w, end := wool.StartSpan(provider.Inject(context.Background()), "Operation")
	t.Cleanup(end)
	return w.WithLogger(cap), cap
}

func TestLogRecordCarriesTheTraceItWasEmittedInside(t *testing.T) {
	span := &identifiedSpan{
		traceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		spanID:  "00f067aa0ba902b7",
	}
	w, cap := woolInSpan(t, span)

	w.Info("handling request", wool.Field("id", 42))

	require.Len(t, cap.logs, 1)
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", cap.logs[0].TraceID)
	require.Equal(t, "00f067aa0ba902b7", cap.logs[0].SpanID)
	// The span still receives the line as an event; correlation is additive.
	require.Equal(t, 1, span.events)
}

// A backend that cannot name its spans must not break. This is the regression
// that a widened wool.Span interface would have caused.
func TestASpanThatCannotNameItsTraceStillLogs(t *testing.T) {
	span := &anonymousSpan{}
	w, cap := woolInSpan(t, span)

	w.Info("handling request")

	require.Len(t, cap.logs, 1)
	require.Empty(t, cap.logs[0].TraceID)
	require.Empty(t, cap.logs[0].SpanID)
	require.Equal(t, 1, span.events)
}

// A non-recording span reports empty ids, and an empty id must never reach a
// line: "trace_id=" or an all-zero id sends a reader hunting a trace that does
// not exist.
func TestANonRecordingSpanAddsNothingToTheLine(t *testing.T) {
	w, cap := woolInSpan(t, &identifiedSpan{traceID: "", spanID: ""})

	w.Info("handling request")

	require.Len(t, cap.logs, 1)
	require.Empty(t, cap.logs[0].TraceID)
	require.NotContains(t, cap.logs[0].String(), "trace_id")
	require.NotContains(t, cap.logs[0].String(), "span_id")
}

// With no telemetry at all there is no span, so a local run's lines keep
// exactly the shape they had before this change.
func TestWithoutTelemetryTheLineIsUnchanged(t *testing.T) {
	w, cap := newWool(t, wool.INFO)

	w.Info("handling request", wool.Field("id", 42))

	require.Len(t, cap.logs, 1)
	require.Empty(t, cap.logs[0].TraceID)
	require.Empty(t, cap.logs[0].SpanID)
	require.Equal(t, "(INFO) handling request id=42", cap.logs[0].String())
}

// The console/stdout line is one half of the join: the node-level collector
// reads container stdout, so the id has to be ON the rendered line.
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
	// The message must stay ahead of the correlation, not be displaced by it.
	require.Less(t, strings.Index(line, "handling request"), strings.Index(line, "trace_id="))
}

// The other half of the join: the JSON sink (agents write a JSON envelope to
// stderr) must carry the ids under the conventional snake_case names a
// collector's parser looks for.
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

// Without telemetry the keys must be absent rather than present-and-empty, so
// a parser cannot mistake "" for a trace.
func TestTheJSONRecordOmitsAbsentIDs(t *testing.T) {
	data, err := json.Marshal(&wool.Log{Level: wool.INFO, Message: "handling request"})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotContains(t, decoded, "trace_id")
	require.NotContains(t, decoded, "span_id")
}

// AtLevel filters fields. It must not filter away correlation: a line that
// survives filtering but loses its trace id is no longer joinable.
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

// Correlation must survive the scoping that real call chains are built from,
// or only the top frame of an operation would be joinable.
func TestAScopedChildKeepsTheTrace(t *testing.T) {
	w, cap := woolInSpan(t, &identifiedSpan{
		traceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		spanID:  "00f067aa0ba902b7",
	})

	w.In("Inner").With(wool.Field("scope", "inner")).Info("deeper")

	require.Len(t, cap.logs, 1)
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", cap.logs[0].TraceID)
}
