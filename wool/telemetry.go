package wool

import "context"

// Span represents an active trace span. Implementations are provided by
// telemetry backends (e.g. wool/otel).
type Span interface {
	// AddEvent records a log entry as a span event.
	AddEvent(name string, fields []*LogField)
	// End completes the span.
	End()
}

// SpanIdentity is an OPTIONAL capability of a Span: a backend whose spans can
// name the trace they belong to implements it, and wool stamps those ids onto
// every log record emitted inside the span (see Wool.process).
//
// It is deliberately a second interface rather than two more methods on Span.
// Span is part of this library's published surface and anything outside this
// repository may implement it; widening Span would break every such
// implementation at compile time for a capability not all backends have. A
// backend that cannot name its spans simply does not implement this, and logs
// carry no ids rather than failing to build.
//
// An implementation MUST return the empty string rather than a zero-valued id
// when it has no identity to report — an absent or invalid span context. A log
// line reading trace_id=00000000000000000000000000000000 is worse than one with
// no id at all: it looks like a real trace that cannot be found.
//
// "No identity" is NOT the same as "not recording", and this interface
// deliberately does not ask about recording. A span can be valid and
// non-recording — an unsampled span, a context propagated from a remote caller,
// a span after End() — and in every one of those cases its ids are real and
// worth stamping: they join this line to whatever else carries the same trace,
// including lines from a service that did sample it. Validity cannot promise a
// backend lookup will succeed, and it is not trying to.
type SpanIdentity interface {
	// TraceID identifies the whole trace, and is the key a log line is joined
	// to its trace by.
	TraceID() string
	// SpanID identifies the one operation within that trace.
	SpanID() string
}

// Tracer creates spans.
type Tracer interface {
	// Start creates a new span. The returned context carries the span.
	Start(ctx context.Context, name string) (context.Context, Span)
}

// ContextIdentity is an OPTIONAL capability of a TelemetryProvider: it reports
// the identity of the BACKEND's own active span in a context.
//
// This exists because wool's span and the backend's span are not the same thing.
// wool knows only about spans started through StartSpan, which it keeps under a
// private context key. An instrumentation library — including this repository's
// own wool/otel.GRPCServerOptions() — installs the span it creates for an
// incoming RPC in the BACKEND's context slot, which wool cannot see. Without
// this capability a handler logging inside a live, valid server span emitted no
// ids at all, and a line emitted inside a span a caller nested on the backend
// was attributed to the enclosing wool span instead of to the span it actually
// ran in.
//
// An implementation returns empty strings when the context carries no valid
// backend span, under the same rule as SpanIdentity. It must not start a span,
// mutate the context, or block: it is called once per emitted log line.
type ContextIdentity interface {
	SpanIdentityFromContext(ctx context.Context) (traceID, spanID string)
}

// TelemetryProvider is the bridge between wool and a tracing backend.
// Register one via RegisterTelemetry or pass it to Provider.WithTelemetry.
type TelemetryProvider interface {
	// NewTracer creates a Tracer scoped to the given name.
	NewTracer(name string) Tracer
	// Shutdown flushes and closes the telemetry backend.
	Shutdown(ctx context.Context) error
}

// --- Global registration (smart import pattern) ---

var globalTelemetry TelemetryProvider

// RegisterTelemetry sets the global telemetry provider.
// Typically called from an init() in a backend package (e.g. wool/otel).
func RegisterTelemetry(tp TelemetryProvider) {
	globalTelemetry = tp
}

// GetTelemetry returns the registered global telemetry provider, or nil.
func GetTelemetry() TelemetryProvider {
	return globalTelemetry
}

// TelemetryEnabled returns true if a telemetry provider has been registered.
func TelemetryEnabled() bool {
	return globalTelemetry != nil
}
