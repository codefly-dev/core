package wool

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/pkg/errors"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

type Wool struct {
	name   string
	source *Identifier
	ref    *CodefReference

	fields []*LogField

	ctx context.Context

	provider *Provider
	span     trace.Span

	logger LogProcessor
}

type Otel interface {
}

func (c *Wool) In(method string, fields ...*LogField) *Wool {
	c.name = method
	c.fields = fields
	// We keep track of the stack
	//stack := c.Stack()
	return c
}

func (c *Wool) Context() context.Context {
	return c.ctx
}

// Catch recovers from a panic and logs the error
func (c *Wool) Catch() {
	if r := recover(); r != nil {
		c.Warn("PANIC CAUGHT INSIDE THE AGENT CODE ", Field("panic", r))
		c.Warn(string(debug.Stack()))
	}
}

func (c *Wool) process(l Loglevel, msg string, fields ...*LogField) {
	for _, f := range fields {
		if f.Level == DEFAULT {
			f.Level = l
		}
	}
	log := &Log{Message: msg, Fields: fields, Header: c.Name(), Level: l}

	if WithTelemetry() {
		c.span.AddEvent(LogEvent, log.Event())
	}
	if c.logger != nil {
		c.logger.Process(log)
	}
}

func (c *Wool) Trace(msg string, fields ...*LogField) {
	c.process(TRACE, msg, fields...)
}

func (c *Wool) Info(msg string, fields ...*LogField) {
	c.process(INFO, msg, fields...)
}

func (c *Wool) Debug(msg string, fields ...*LogField) {
	c.process(DEBUG, msg, fields...)
}

func (c *Wool) Warn(msg string, fields ...*LogField) {
	c.process(WARN, msg, fields...)
}

func (c *Wool) Error(msg string, fields ...*LogField) {
	c.process(ERROR, msg, fields...)
}

func (c *Wool) Fatal(msg string, fields ...*LogField) {
	c.process(FATAL, msg, fields...)
}

func (c *Wool) Wrap(err error) error {
	if msg := c.Name(); msg != "" {
		return errors.Wrap(err, msg)
	}
	return err
}

func (c *Wool) Wrapf(err error, msg string, args ...any) error {
	msg = fmt.Sprintf(msg, args...)
	if name := c.Name(); name != "" {
		msg = fmt.Sprintf("%s: %s", c.Name(), msg)
	}
	if msg != "" {
		return errors.Wrap(err, msg)
	}
	return err
}

func (c *Wool) Close() {
	if c.span != nil {
		c.span.End()
	}
}

func (c *Wool) NewError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

type CodePath struct {
	Method string      `json:"method"`
	Fields []*LogField `json:"fields"`
}

const CodePathKey = "codepath"

func (c *Wool) StackTrace() []CodePath {
	b := baggage.FromContext(c.ctx)
	m := b.Member(CodePathKey)
	return toCodePaths(m)
}

type ContextKey string

const KeyInContext = ContextKey("provider")

type Resource struct {
	Resource *resource.Resource
	*Identifier
}

func WithTelemetry() bool {
	return false
}

func (c *Wool) Name() string {
	return c.name
}

func (c *Wool) WithLogger(l LogProcessor) *Wool {
	c.logger = l
	return c
}

const LogEvent = "log"

func toCodePaths(m baggage.Member) []CodePath {
	// Use Properties of the Member to get the values
	// and convert them to CodePath
	var paths []CodePath
	for _, v := range m.Properties() {
		paths = append(paths, toCodePath(v))
	}
	return paths
}

func toCodePath(v baggage.Property) CodePath {
	return CodePath{}
}

type CodefReference struct {
	Line int    `json:"line"`
	File string `json:"file"`
}

func (w *Wool) File() string {
	if w.ref == nil {
		return ""
	}
	return w.ref.File
}
