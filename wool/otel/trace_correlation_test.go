package otel

import (
	"context"
	"regexp"
	"testing"

	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// This is the end-to-end half of the log/trace join: wool/trace_correlation_test.go
// pins the contract with fakes, and these tests prove the OTEL adapter reports
// the same ids OpenTelemetry itself puts on the wire. If these two disagree, a
// log line names a trace the backend stored under a different id — which looks
// like working correlation right up to the moment someone clicks the link.
//
// The test is internal to the package so the invalid-context case can build a
// spanAdapter directly; the recording case goes through the public path.

var (
	traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func TestTheAdapterReportsTheSameIDsOpenTelemetryDoes(t *testing.T) {
	// Enable registers globally, exactly as a service's boot does. WithStdout
	// keeps the exporter off the network; no span is ended, so nothing prints.
	provider, err := Enable(WithStdout(), WithServiceName("correlation-test"))
	require.NoError(t, err)

	ctx, span := provider.NewTracer("test").Start(context.Background(), "Operation")

	identity, ok := span.(wool.SpanIdentity)
	require.True(t, ok, "the OTEL span adapter must implement wool.SpanIdentity")

	// What OpenTelemetry itself says this context is — the id the backend will
	// store the trace under.
	expected := oteltrace.SpanFromContext(ctx).SpanContext()
	require.True(t, expected.IsValid())

	require.Equal(t, expected.TraceID().String(), identity.TraceID())
	require.Equal(t, expected.SpanID().String(), identity.SpanID())
	require.Regexp(t, traceIDPattern, identity.TraceID())
	require.Regexp(t, spanIDPattern, identity.SpanID())
}

// An invalid span context stringifies to all zeros. Reporting that as a trace
// id is worse than reporting nothing: it is a well-formed id for a trace that
// was never recorded, and a reader cannot tell the difference from the line.
func TestAnInvalidSpanContextReportsNoIDsRatherThanZeros(t *testing.T) {
	// The span carried by a bare context is OTEL's non-recording span, whose
	// context is invalid — the same shape a service gets when it logs outside
	// any span.
	adapter := &spanAdapter{span: oteltrace.SpanFromContext(context.Background())}
	require.False(t, adapter.span.SpanContext().IsValid())

	require.Empty(t, adapter.TraceID())
	require.Empty(t, adapter.SpanID())
}

// Logging must never panic, and a type assertion to SpanIdentity succeeds on an
// interface holding a nil *spanAdapter.
func TestANilAdapterReportsNoIDs(t *testing.T) {
	var adapter *spanAdapter

	require.Empty(t, adapter.TraceID())
	require.Empty(t, adapter.SpanID())
}
