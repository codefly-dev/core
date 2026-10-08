// Package otel provides an OpenTelemetry backend for wool.
//
// Import this package to enable OTEL tracing in wool:
//
//	import _ "github.com/codefly-dev/core/wool/otel"
//
// Or call Enable() explicitly for configuration:
//
//	otel.Enable(otel.WithEndpoint("localhost:4317"), otel.WithServiceName("my-svc"))
//
// To export over TLS, give the collector as a URL and let its scheme decide
// the transport — "https" is TLS with the system roots, "http" is plaintext.
// The URL names a host and a port; see WithEndpointURL for the rules it is held
// to and for the one environment that overrules a scheme:
//
//	otel.Enable(otel.WithEndpointURL("https://collector.example.com:4317"))
//
// OTEL_EXPORTER_OTLP_ENDPOINT is a URL by specification and takes the same path,
// so a collector configured entirely by environment can also be reached over TLS.
package otel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/codefly-dev/core/wool"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
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

	// endpointURL is the collector as a URL, and endpointURLSet says WithEndpointURL
	// was the last endpoint option given. It is a separate flag so that an empty
	// URL is refused rather than read as "not given".
	endpointURL    string
	endpointURLSet bool
	// endpointURLFromEnvironment distinguishes a URL this process was configured
	// with from one a caller wrote at the call site, because only the second is
	// judged when the URL will not be dialled. See Enable.
	endpointURLFromEnvironment bool
}

// WithEndpoint sets the OTLP collector endpoint (e.g. "localhost:4317").
//
// The connection is plaintext. To have the endpoint's scheme choose between
// plaintext and TLS, use WithEndpointURL; if both are given, the last one wins.
func WithEndpoint(endpoint string) Option {
	return func(c *config) {
		c.endpoint = endpoint
		c.endpointURLSet = false
		c.endpointURLFromEnvironment = false
	}
}

// WithEndpointURL sets the OTLP collector endpoint as a URL and takes the
// transport security from its scheme: "http" connects in plaintext, "https"
// connects over TLS verified against the system roots. The URL must name a host
// and a port, and must not carry userinfo credentials; any other scheme, an
// empty URL, or a URL that breaks one of those rules makes Enable return an
// error naming the rule, and Enable registers nothing.
//
// The port is required rather than defaulted because the gRPC exporter passes
// the URL's authority through untouched and gRPC fills a missing port with 443,
// which is not the OTLP/gRPC port.
//
// The scheme decides, and WithInsecure cannot downgrade an "https" URL. The one
// thing that overrules a scheme is an OTLP certificate in the environment
// (OTEL_EXPORTER_OTLP_CERTIFICATE and its TRACES_/CLIENT_ variants): the
// exporter prefers those credentials over the scheme's plaintext flag, so
// "https" honours them as its trust anchor while "http" beside one is refused
// as a contradiction rather than quietly upgraded.
//
// A path in the URL is ignored by the gRPC exporter, and
// OTEL_EXPORTER_OTLP_ENDPOINT is not consulted once this option is given.
//
// If WithEndpoint and WithEndpointURL are both given, the last one wins.
func WithEndpointURL(endpointURL string) Option {
	return func(c *config) {
		c.endpointURL = endpointURL
		c.endpointURLSet = true
		c.endpointURLFromEnvironment = false
	}
}

// validateEndpointURL refuses everything WithEndpointURL's transport rule cannot
// honestly decide from. The exporter's own parsing treats every scheme but
// "https" as plaintext and silently keeps its default on a URL that does not
// parse, so a malformed or scheme-less value (a bare "host:4317" parses as the
// scheme "host") would otherwise be dialled in plaintext or at the wrong address.
// The error never carries the raw URL: it may hold credentials.
//
// Each refusal below is a rule a fixture names. They are not interchangeable:
// the scheme rule decides the transport, the host and port rules decide the
// address, and the userinfo rule refuses credentials that would be dropped.
func validateEndpointURL(raw string) error {
	if raw == "" {
		return errors.New("wool/otel: endpoint URL is empty; want an http:// or https:// collector URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Even the parser's underlying error can contain credentials: an
		// unescaped slash or # in a password makes it look like an invalid port.
		return errors.New("wool/otel: endpoint URL is not a valid URL; want an http:// or https:// collector URL with an explicit host and port")
	}
	switch u.Scheme {
	case "http", "https":
	case "":
		return errors.New("wool/otel: endpoint URL has no scheme; want http:// (plaintext) or https:// (TLS)")
	default:
		return fmt.Errorf("wool/otel: endpoint URL scheme %q is not supported; want http:// (plaintext) or https:// (TLS)", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("wool/otel: endpoint URL with scheme %q has no host", u.Scheme)
	}
	// The port is REQUIRED, and this rule is the reason: the exporter hands
	// u.Host to gRPC untouched, and gRPC's default DNS resolver fills a missing
	// port with 443 — not the OTLP/gRPC 4317. A portless URL therefore exports
	// to the wrong port and says nothing: the collector is never reached, the
	// batcher's Shutdown reports only a context deadline, and the global error
	// handler sees nothing. Naming the port is the only way the address the
	// caller wrote is the address that is dialled.
	if u.Port() == "" {
		return fmt.Errorf("wool/otel: endpoint URL with scheme %q has no port; name it explicitly (the OTLP/gRPC port is usually 4317) — gRPC would otherwise dial 443", u.Scheme)
	}
	// Userinfo cannot survive: the exporter keeps only u.Host, so credentials in
	// the URL are dropped and the export goes out unauthenticated. Refusing is
	// the only way the caller learns that; staying silent trades an authenticated
	// export for an anonymous one.
	if u.User != nil {
		return fmt.Errorf("wool/otel: endpoint URL with scheme %q carries userinfo credentials, which the gRPC exporter drops; pass OTLP authentication through OTEL_EXPORTER_OTLP_HEADERS instead", u.Scheme)
	}
	return nil
}

// otlpTLSEnvVars are the OTLP environment variables that install gRPC transport
// credentials. The exporter builds a *tls.Config from any of them and sets it as
// cfg.Traces.GRPCCredentials, which NewGRPCConfig prefers over the Insecure flag
// that a URL's scheme sets — so these, not the scheme, decide the transport when
// they are present.
var otlpTLSEnvVars = []string{
	"OTEL_EXPORTER_OTLP_CERTIFICATE",
	"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE",
	"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE",
	"OTEL_EXPORTER_OTLP_CLIENT_KEY",
	"OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE",
	"OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY",
}

// refuseEnvOverrulingScheme refuses the one case where the environment would
// overrule a URL's scheme instead of merely configuring it.
//
// An "https" URL and an OTLP certificate agree: both ask for TLS, and the
// certificate is how an operator points at a private CA rather than the system
// roots, so it is honoured. An "http" URL and an OTLP certificate contradict
// each other, and the certificate wins inside the exporter — the caller asked
// for plaintext and would get TLS, which fails against a plaintext collector
// and loses every span with nothing in the error handler. Core cannot clear
// those credentials through the exporter's option surface, so the contradiction
// is refused here and "http is plaintext" stays true.
func refuseEnvOverrulingScheme(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return nil
	}
	for _, name := range otlpTLSEnvVars {
		if os.Getenv(name) == "" {
			continue
		}
		return fmt.Errorf(
			"wool/otel: endpoint URL scheme \"http\" asks for plaintext but %s installs TLS credentials the exporter prefers over it; use https:// for that collector, or unset %s",
			name, name)
	}
	return nil
}

// endpointURLFromEnv reports whether an OTEL_EXPORTER_OTLP_ENDPOINT value is the
// URL the OTEL specification says it is, so it takes the URL path and its scheme
// decides the transport.
//
// A bare "host:4317" is not: it is the shape this package accepted before the
// URL path existed, and the exporter's own environment reader rejects it
// outright ("first path segment in URL cannot contain colon"), so it is kept on
// the plaintext endpoint path rather than refused.
func endpointURLFromEnv(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// WithServiceName sets the service name for traces.
func WithServiceName(name string) Option {
	return func(c *config) { c.serviceName = name }
}

// WithStdout uses a stdout exporter instead of OTLP (useful for development).
func WithStdout() Option {
	return func(c *config) { c.useStdout = true }
}

// WithInsecure states that the OTLP connection is plaintext, which it already
// is: an endpoint given by WithEndpoint or by a bare "host:port" in the
// environment is never dialled over TLS, and WithEndpointURL takes its transport
// from the URL's scheme instead. This option therefore changes nothing on any
// path, and is kept only because removing an exported symbol from a published
// package is a breaking change this PR is not the place to make. Choose the
// transport with WithEndpointURL.
func WithInsecure() Option {
	return func(c *config) { c.insecure = true }
}

// Enable creates an OTEL TelemetryProvider and registers it with wool, and
// installs the W3C TraceContext propagator so a trace survives a service hop.
// If no options are provided, it reads from standard OTEL environment variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME).
//
// An endpoint given by WithEndpoint is dialled in plaintext. WithEndpointURL is
// the way to choose TLS: the URL's scheme decides.
//
// OTEL_EXPORTER_OTLP_ENDPOINT is a URL by specification, so a value carrying an
// http or https scheme takes the same path as WithEndpointURL — its scheme
// decides the transport and the same rules refuse it. A bare "host:4317", the
// shape this package took before the URL path existed, stays plaintext.
func Enable(opts ...Option) (*Provider, error) {
	cfg := &config{
		serviceName: os.Getenv("OTEL_SERVICE_NAME"),
		insecure:    true,
	}
	// The environment's endpoint is read into whichever field matches its shape.
	// Handing a spec-form URL to WithEndpoint's path would pass the whole string
	// to gRPC as a target, which resolves nowhere: the collector is never dialled
	// and every span is dropped in silence.
	if raw := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); raw != "" {
		if endpointURLFromEnv(raw) {
			cfg.endpointURL, cfg.endpointURLSet = raw, true
			cfg.endpointURLFromEnvironment = true
		} else {
			cfg.endpoint = raw
		}
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = "unknown"
	}
	// A URL is judged when it was written at the call site, and when it is the
	// one that will be dialled. The asymmetry is deliberate: an unusable argument
	// a caller passed is that caller's mistake and is worth refusing even if this
	// run would not have dialled it, whereas a collector this machine happens to
	// be configured with must not stop WithStdout from working — that would make
	// stdout unusable anywhere a collector is configured.
	if cfg.endpointURLSet && (!cfg.endpointURLFromEnvironment || !cfg.useStdout) {
		if err := validateEndpointURL(cfg.endpointURL); err != nil {
			return nil, err
		}
		if err := refuseEnvOverrulingScheme(cfg.endpointURL); err != nil {
			return nil, err
		}
	}

	var exporter sdktrace.SpanExporter
	var err error

	if cfg.useStdout {
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	} else if cfg.endpointURLSet {
		// No transport option here: the exporter reads it off the URL's scheme,
		// validateEndpointURL has already refused any scheme that would not mean
		// exactly http or https, and refuseScheme has refused the one environment
		// that would overrule a plaintext scheme.
		exporter, err = otlptrace.New(
			context.Background(),
			otlptracegrpc.NewClient(otlptracegrpc.WithEndpointURL(cfg.endpointURL)),
		)
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
	// The instrumentation this package installs — the otelgrpc handlers of
	// GRPCServerOptions and GRPCDialOptions, and otelhttp or otelconnect in a
	// service that reaches for them — carries a trace across a hop through the
	// GLOBAL propagator, whose default injects and extracts nothing. Without
	// this, two fully instrumented services exchange no traceparent and each
	// side opens a trace of its own for one request (#712).
	//
	// TraceContext alone, never a composite with Baggage: baggage is
	// caller-supplied key/value data that would then be forwarded verbatim
	// across a trust boundary, which is a decision this package does not get to
	// make on a service's behalf.
	otel.SetTextMapPropagator(propagation.TraceContext{})

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
