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
