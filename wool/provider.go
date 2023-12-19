package wool

import (
	"context"
	"fmt"
	"runtime"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func New(ctx context.Context, r *Resource) *Provider {
	// Define a provider
	base := &Provider{
		ctx: ctx,
		identifier: &Identifier{
			Kind:   r.Kind,
			Unique: r.Unique,
		},
		resource: r.Resource,
	}
	if WithTelemetry() {

		// Create a new exporter
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			panic(err)
		}

		bsp := sdktrace.NewBatchSpanProcessor(exp)

		// Create tracer provider
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithSpanProcessor(bsp),
			sdktrace.WithResource(r.Resource),
		)
		otel.SetTracerProvider(tp)

		base.tp = tp
		base.tracer = otel.Tracer("")
	}
	return base

}

type Identifier struct {
	Kind   string `json:"kind"`
	Unique string `json:"unique"`
}

// Provider keeping track
type Provider struct {
	identifier *Identifier

	resource *resource.Resource

	logger LogProcessor

	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
	ctx    context.Context
}

func (w *Provider) Done() {
	if w.tp != nil {
		_ = w.tp.Shutdown(w.ctx)
	}
}

func (w *Provider) WithContext(ctx context.Context) context.Context {
	// TODO: ADD TO BAGGAGE
	return context.WithValue(ctx, KeyInContext, w)
}

func (w *Provider) WithLogger(l LogProcessor) *Provider {
	w.logger = l
	return w
}

// TODO: MOVE TO BAGGAGE
func get(ctx context.Context) (*Provider, error) {
	w := ctx.Value(KeyInContext)
	if w == nil {
		return nil, fmt.Errorf("no wool in context")
	}
	return w.(*Provider), nil
}

func (provider *Provider) Get(ctx context.Context) *Wool {
	base := &Wool{ctx: ctx, source: provider.identifier}
	if _, file, line, ok := runtime.Caller(1); ok {
		base.ref = &CodefReference{File: file, Line: line}
	} else {
		base.ref = &CodefReference{File: "unknown", Line: 0}
	}
	base.provider = provider
	if provider.logger != nil {
		base.logger = provider.logger
	}

	if provider.tracer != nil {
		// Create a span
		currentCtx, span := provider.tracer.Start(ctx, provider.identifier.Unique)
		base.ctx = currentCtx
		base.span = span
	}
	return base
}

type Console struct {
	level Loglevel
}

func (c Console) Process(msg *Log) {
	if msg.Level < c.level {
		return
	}
	fmt.Println(msg.Message, msg.Fields)
}

func Get(ctx context.Context) *Wool {
	if ctx == nil {
		panic("nil context")
	}
	base := &Wool{ctx: ctx, logger: &Console{level: INFO}}
	if _, file, line, ok := runtime.Caller(1); ok {
		base.ref = &CodefReference{File: file, Line: line}
	} else {
		base.ref = &CodefReference{File: "unknown", Line: 0}
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
		// Create a span
		currentCtx, span := provider.tracer.Start(ctx, provider.identifier.Unique)
		base.ctx = currentCtx
		base.span = span
	}
	return base
}
