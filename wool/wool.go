package wool

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	trace "go.opentelemetry.io/otel/trace"
)

type ContextKey string

const KeyInContext = ContextKey("wool")

type Resourceful interface {
	Resource() *resource.Resource
	Name() string
}

func ToAttributes(agent *configurations.Agent) []attribute.KeyValue {
	var attr []attribute.KeyValue

	return attr
}

type Agent struct {
	agent *configurations.Agent
	name  string
}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Resource() *resource.Resource {
	return resource.NewSchemaless(ToAttributes(a.agent)...)
}

func NewAgent(agent *configurations.Agent) *Agent {
	return &Agent{agent: agent, name: agent.Name}
}

func WithTelemetry() bool {
	return false
}

func New(ctx context.Context, who Resourceful) *Wool {
	// Define a wool
	if WithTelemetry() {

		// Create a new exporter
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			panic(err)
		}

		bsp := sdktrace.NewBatchSpanProcessor(exp)

		r := who.Resource()

		// Create tracer provider
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithSpanProcessor(bsp),
			sdktrace.WithResource(r),
		)
		otel.SetTracerProvider(tp)
		return &Wool{
			ctx:      ctx,
			resource: r,
			tp:       tp,
			name:     who.Name(),
			tracer:   otel.Tracer(""),
		}
	}
	return &Wool{
		ctx:  ctx,
		name: who.Name(),
	}

}

// Wool keeping track
type Wool struct {
	tp       *sdktrace.TracerProvider
	resource *resource.Resource
	name     string
	tracer   trace.Tracer
	ctx      context.Context
}

func (w *Wool) Context() *Context {
	return &Context{wool: w}
}

func (w *Wool) Done() {
	if w.tp != nil {
		_ = w.tp.Shutdown(w.ctx)
	}
}

func (w *Wool) NewContext() context.Context {
	return context.WithValue(w.ctx, KeyInContext, w)
}

func (c *Context) Name() string {
	return c.wool.name
}

const LogEvent = "log"

func (c *Context) Info(msg string, fields ...*LogField) {
	log := &Log{Message: msg, Fields: fields}
	if WithTelemetry() {
		c.span.AddEvent(LogEvent, log.Event())
	}
	if logHandler != nil {
		logHandler(log)
	}
}

type Context struct {
	wool *Wool
	ctx  context.Context
	span trace.Span
}

func CLI(ctx context.Context) *Wool {
	return New(ctx, NewAgent(configurations.CLI))
}

func get(ctx context.Context) *Wool {
	w := ctx.Value(KeyInContext)
	if w == nil {
		return CLI(ctx)
	}
	return w.(*Wool)
}

func Get(ctx context.Context) *Context {
	w := get(ctx)
	if w.tracer != nil {
		// Create a span
		currentCtx, span := w.tracer.Start(ctx, w.name)
		return &Context{wool: w, ctx: currentCtx, span: span}
	}
	return &Context{wool: w, ctx: ctx}
}

func (c *Context) Context() context.Context {
	return c.ctx
}

func (c *Context) Close() {
	if c.span != nil {
		c.span.End()
	}
}

type LogHandler func(msg *Log)

var logHandler LogHandler

func SetLogExporter(handler LogHandler) {
	logHandler = handler
}
