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

// Tracer creates spans.
type Tracer interface {
	// Start creates a new span. The returned context carries the span.
	Start(ctx context.Context, name string) (context.Context, Span)
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
