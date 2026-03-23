package wool

import (
	"context"
	"fmt"
	"runtime"
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
	if _, file, line, ok := runtime.Caller(1); ok {
		base.ref = &CodeReference{File: file, Line: line}
	} else {
		base.ref = &CodeReference{File: "unknown", Line: 0}
	}
	base.provider = p
	if p.logger != nil {
		base.logger = p.logger
	}

	if p.tracer != nil {
		currentCtx, span := p.tracer.Start(ctx, p.identifier.Unique)
		base.ctx = currentCtx
		base.span = span
	}
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

// get retrieves the provider from context.
func get(ctx context.Context) (*Provider, error) {
	w := ctx.Value(ProviderKey)
	if w == nil {
		return nil, fmt.Errorf("no wool in context")
	}
	return w.(*Provider), nil
}

// Get retrieves or creates a Wool instance from the context.
// If no provider is found, falls back to a Console logger.
func Get(ctx context.Context) *Wool {
	if ctx == nil {
		panic("nil context")
	}

	base := &Wool{ctx: ctx, logger: getFallbackLogger()}
	if _, file, line, ok := runtime.Caller(1); ok {
		base.ref = &CodeReference{File: file, Line: line}
	} else {
		base.ref = &CodeReference{File: "unknown", Line: 0}
	}
	provider, err := get(ctx)
	if err != nil {
		return base
	}
	base.provider = provider
	if provider.logger != nil {
		base.logger = provider.logger
	}

	if provider.tracer != nil {
		currentCtx, span := provider.tracer.Start(ctx, provider.identifier.Unique)
		base.ctx = currentCtx
		base.span = span
	}
	return base
}
