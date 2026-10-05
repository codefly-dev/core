// Package otel provides an OpenTelemetry backend for wool.
//
// Import this package to enable OTEL tracing in wool:
//
//	import _ "github.com/codefly-dev/core/wool/otel"
//
// Or call Enable() explicitly for configuration:
//
//	otel.Enable(otel.WithEndpoint("localhost:4317"), otel.WithServiceName("my-svc"))
package otel

import (
	"context"
	"os"

	"github.com/codefly-dev/core/wool"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Option configures the OTEL provider.
type Option func(*config)

type config struct {
	endpoint    string
	serviceName string
	useStdout   bool
	insecure    bool
}

// WithEndpoint sets the OTLP collector endpoint (e.g. "localhost:4317").
func WithEndpoint(endpoint string) Option {
	return func(c *config) { c.endpoint = endpoint }
}

// WithServiceName sets the service name for traces.
func WithServiceName(name string) Option {
	return func(c *config) { c.serviceName = name }
}

// WithStdout uses a stdout exporter instead of OTLP (useful for development).
func WithStdout() Option {
	return func(c *config) { c.useStdout = true }
}

// WithInsecure disables TLS for the OTLP connection.
func WithInsecure() Option {
	return func(c *config) { c.insecure = true }
}

// Enable creates an OTEL TelemetryProvider and registers it with wool.
// If no options are provided, it reads from standard OTEL environment variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME).
func Enable(opts ...Option) (*Provider, error) {
	cfg := &config{
		endpoint:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		serviceName: os.Getenv("OTEL_SERVICE_NAME"),
		insecure:    true,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = "unknown"
	}

	var exporter sdktrace.SpanExporter
	var err error

	if cfg.useStdout {
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	} else if cfg.endpoint != "" {
		grpcOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.endpoint),
		}
		if cfg.insecure {
			grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
		}
		exporter, err = otlptrace.New(
			context.Background(),
			otlptracegrpc.NewClient(grpcOpts...),
		)
	} else {
		// No endpoint configured -- use stdout as fallback
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
	if err != nil {
		return nil, err
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", cfg.serviceName),
			attribute.String("library.name", "wool"),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	provider := &Provider{tp: tp}
	wool.RegisterTelemetry(provider)
	return provider, nil
}

// Provider implements wool.TelemetryProvider backed by OpenTelemetry.
type Provider struct {
	tp *sdktrace.TracerProvider
}

// NewTracer creates a new OTEL-backed Tracer.
func (p *Provider) NewTracer(name string) wool.Tracer {
	return &tracer{t: p.tp.Tracer(name)}
}

// SpanIdentityFromContext implements wool.ContextIdentity: the identity of
// OpenTelemetry's OWN active span in ctx, which is where an instrumentation
// library puts the span it creates.
//
// This is the path that makes correlation work on a real service. GRPCServerOptions
// installs an otelgrpc stats handler, and the server span it starts for an
// incoming RPC lives here and nowhere wool can see. The same is true of any span
// a caller starts from an OTEL tracer directly, including one nested inside a
// wool span — so reading this first is also what attributes a line to the span
// it actually ran in rather than to its parent.
//
// Empty strings when ctx carries no valid span: a context with no span at all
// yields OpenTelemetry's non-recording span, whose context is invalid and whose
// ids are zeros.
func (p *Provider) SpanIdentityFromContext(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	return identityOf(oteltrace.SpanFromContext(ctx))
}

// identityOf reports a span's ids, or empty strings when it has no identity to
// report.
//
// The guard is IsValid, deliberately NOT IsRecording. A valid, non-recording
// span — unsampled, propagated from a remote caller, or already ended — has real
// ids that join this line to every other line and service carrying the same
// trace, so they are stamped. What is refused is an ABSENT identity: an invalid
// context stringifies to all zeros, and a line claiming
// trace_id=00000000000000000000000000000000 is a well-formed id for a trace that
// never existed, which a reader cannot tell from a real one.
func identityOf(span oteltrace.Span) (string, string) {
	sc, ok := identityContextOf(span)
	if !ok {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// identityContextOf reports a span's context when it has an identity to report.
// The getters use this rather than identityOf so each one formats only the id it
// was asked for: a real backend behind a TelemetryProvider that predates
// ContextIdentity takes the per-getter path, and formatting both ids in each
// getter made that fallback cost more than it did before ContextIdentity existed.
func identityContextOf(span oteltrace.Span) (oteltrace.SpanContext, bool) {
	if span == nil {
		return oteltrace.SpanContext{}, false
	}
	sc := span.SpanContext()
	if !sc.IsValid() {
		return oteltrace.SpanContext{}, false
	}
	return sc, true
}

// Shutdown flushes and shuts down the OTEL provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.tp.Shutdown(ctx)
}

// --- tracer adapter ---

type tracer struct {
	t oteltrace.Tracer
}

func (t *tracer) Start(ctx context.Context, name string) (context.Context, wool.Span) {
	ctx, span := t.t.Start(ctx, name)
	return ctx, &spanAdapter{span: span}
}

// --- span adapter ---

type spanAdapter struct {
	span oteltrace.Span
}

func (s *spanAdapter) AddEvent(name string, fields []*wool.LogField) {
	var attrs []attribute.KeyValue
	for _, f := range fields {
		attrs = append(attrs, toAttribute(f))
	}
	s.span.AddEvent(name, oteltrace.WithAttributes(attrs...))
}

func (s *spanAdapter) End() {
	s.span.End()
}

// TraceID implements wool.SpanIdentity: the id of the trace this span belongs
// to, as 32 lowercase hex characters, or "" when the span has no identity to
// report. See identityOf for why the guard is validity and not recording.
//
// The nil-receiver check is kept as a second line of defence. wool normalizes a
// typed-nil span away at binding now, so this should be unreachable from
// wool — but this type satisfies a published interface and nothing stops a
// caller holding one directly.
func (s *spanAdapter) TraceID() string {
	if s == nil {
		return ""
	}
	sc, ok := identityContextOf(s.span)
	if !ok {
		return ""
	}
	return sc.TraceID().String()
}

// SpanID implements wool.SpanIdentity: the id of this one operation within the
// trace, as 16 lowercase hex characters, under the same rules as TraceID.
func (s *spanAdapter) SpanID() string {
	if s == nil {
		return ""
	}
	sc, ok := identityContextOf(s.span)
	if !ok {
		return ""
	}
	return sc.SpanID().String()
}

func toAttribute(f *wool.LogField) attribute.KeyValue {
	switch v := f.Value.(type) {
	case string:
		return attribute.String(f.Key, v)
	case int:
		return attribute.Int(f.Key, v)
	case int64:
		return attribute.Int64(f.Key, v)
	case float64:
		return attribute.Float64(f.Key, v)
	case bool:
		return attribute.Bool(f.Key, v)
	default:
		return attribute.String(f.Key, "unknown")
	}
}
