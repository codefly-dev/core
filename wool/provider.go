package wool

import (
	"context"
	"fmt"
)

// Identifier holds the kind and unique name of a wool source.
type Identifier struct {
	Kind   string `json:"kind"`
	Unique string `json:"unique"`
}

// Resource is the input to New -- identifies what is being observed.
type Resource struct {
	Kind   string
	Unique string
}

// Provider creates Wool instances and manages an optional telemetry backend.
type Provider struct {
	identifier *Identifier
	logger     LogProcessor
	tracer     Tracer // nil when telemetry is not enabled
	ctx        context.Context
	shutdown   func(context.Context) error
}

// New creates a new Provider for the given resource.
// If a TelemetryProvider has been registered (via RegisterTelemetry or
// WithTelemetry), tracing is automatically enabled.
func New(ctx context.Context, r *Resource) *Provider {
	p := &Provider{
		ctx: ctx,
		identifier: &Identifier{
			Kind:   r.Kind,
			Unique: r.Unique,
		},
	}

	// Auto-enable telemetry if a provider is registered
	if tp := GetTelemetry(); tp != nil {
		p.tracer = tp.NewTracer(r.Unique)
		p.shutdown = tp.Shutdown
	}

	return p
}

// WithConsole sets a console logger at the given level.
func (p *Provider) WithConsole(lvl Loglevel) *Provider {
	p.logger = &Console{level: lvl}
	return p
}

// WithLogger sets a custom log processor.
func (p *Provider) WithLogger(l LogProcessor) *Provider {
	p.logger = l
	return p
}

// WithTelemetry explicitly sets a telemetry provider for this provider.
func (p *Provider) WithTelemetry(tp TelemetryProvider) *Provider {
	p.tracer = tp.NewTracer(p.identifier.Unique)
	p.shutdown = tp.Shutdown
	return p
}

// Done shuts down telemetry if enabled.
func (p *Provider) Done() {
	if p.shutdown != nil {
		_ = p.shutdown(p.ctx)
	}
}

// Inject stores this provider in the context.
func (p *Provider) Inject(ctx context.Context) context.Context {
	return context.WithValue(ctx, ProviderKey, p)
}

// Get creates a Wool instance from this provider.
func (p *Provider) Get(ctx context.Context) *Wool {
	base := &Wool{ctx: ctx, source: p.identifier}
	base.provider = p
	if p.logger != nil {
		base.logger = p.logger
	}

	// Reuse the active span (started via Span()) rather than creating a fresh
	// one per Get. The old code called tracer.Start on EVERY Get and nothing
	// ever ended those spans — no traces were exported and the un-ended spans
	// leaked. Spans now have explicit lifetimes (see Span()).
	base.span = activeSpan(ctx)
	return base
}

// --- Console logger ---

// Console is a simple stdout LogProcessor.
type Console struct {
	level       Loglevel
	messageOnly bool
}

// NewMessageConsole returns a Console that only prints the message text.
func NewMessageConsole() *Console {
	return &Console{level: globalLogLevel, messageOnly: true}
}

func (c Console) Process(msg *Log) {
	if msg.Level < c.level {
		return
	}
	if c.messageOnly {
		fmt.Println(msg.Message)
		return // Fixed: was falling through and double-printing
	}
	fmt.Println(msg)
}

// --- Package-level Get ---

// errNoProvider is a package-level sentinel so the (very hot) no-provider
// path of get() doesn't allocate a fresh error on every wool.Get call.
var errNoProvider = fmt.Errorf("no wool in context")

// get retrieves the provider from context. Returns errNoProvider when none is
// present (or a typed-nil *Provider was stuck on ctx).
func get(ctx context.Context) (*Provider, error) {
	v := ctx.Value(ProviderKey)
	if v == nil {
		return nil, errNoProvider
	}
	p, ok := v.(*Provider)
	if !ok || p == nil {
		return nil, errNoProvider
	}
	return p, nil
}

// Get retrieves or creates a Wool instance from the context.
// If no provider is found, falls back to a Console logger.
//
// wool.Get is on essentially every function's hot path, so it avoids work on
// the common (provider-present) branch: no stack walk, no throwaway error, and
// getFallbackLogger() (which takes a lock + allocates a Console) is only called
// when there is genuinely no provider logger to use.
func Get(ctx context.Context) *Wool {
	if ctx == nil {
		panic("nil context")
	}

	provider, err := get(ctx)
	if err != nil || provider == nil {
		// No registered provider OR a typed-nil *Provider stuck on ctx. Return
		// a fallback Wool — the calling code's chain (.In/.Wrapf) keeps working.
		return &Wool{ctx: ctx, logger: getFallbackLogger()}
	}

	base := &Wool{ctx: ctx, provider: provider, span: activeSpan(ctx)}
	if provider.logger != nil {
		base.logger = provider.logger
	} else {
		base.logger = getFallbackLogger()
	}
	return base
}

type spanContextKey struct{}

// activeSpan returns the span started by the nearest enclosing Span() call, or
// nil when none is active.
func activeSpan(ctx context.Context) Span {
	if s, ok := ctx.Value(spanContextKey{}).(Span); ok {
		return s
	}
	return nil
}

// StartSpan starts a named tracing span and returns a Wool bound to it plus an
// end func the caller MUST defer:
//
//	w, end := wool.StartSpan(ctx, "MyOp")
//	defer end()
//
// Log lines emitted by w (and any wool.Get on the returned context chain) attach
// as events to this span. When no telemetry provider/tracer is configured it is
// a no-op (end is a no-op too), so it is always safe to call.
func StartSpan(ctx context.Context, name string) (*Wool, func()) {
	provider, err := get(ctx)
	if err != nil || provider == nil || provider.tracer == nil {
		return Get(ctx), func() {}
	}
	spanCtx, span := provider.tracer.Start(ctx, name)
	spanCtx = context.WithValue(spanCtx, spanContextKey{}, span)
	return Get(spanCtx), span.End
}
