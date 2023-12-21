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
	ref    *CodeReference

	fields []*LogField

	ctx context.Context

	provider *Provider
	span     trace.Span

	logger LogProcessor
}

func Writer() *LogField {
	return &LogField{Key: "writer"}
}

// Writer implements the io.Writer interface
func (w *Wool) Write(p []byte) (n int, err error) {
	w.With(Writer()).Info(string(p))
	return len(p), nil
}

func (w *Wool) In(method string, fields ...*LogField) *Wool {
	w.name = method
	w.fields = fields
	// We keep track of the stack
	//stack := c.Stack()
	return w
}

func (w *Wool) With(fields ...*LogField) *Wool {
	w.fields = append(w.fields, fields...)
	return w
}

func (w *Wool) Inject(ctx context.Context) context.Context {
	return w.provider.WithContext(ctx)
}

// Catch recovers from a panic and logs the error
func (w *Wool) Catch() {
	if r := recover(); r != nil {
		w.Warn("PANIC CAUGHT INSIDE THE AGENT CODE ", Field("panic", r))
		w.Warn(string(debug.Stack()))
	}
}

func (w *Wool) process(l Loglevel, msg string, fs ...*LogField) {
	for _, f := range fs {
		if f.Level == DEFAULT {
			f.Level = l
		}
	}
	var fields []*LogField
	for _, f := range fs {
		if f.Level >= l {
			fields = append(fields, f)
		}
	}

	log := &Log{Message: msg, Header: w.Name(), Fields: fields, Level: l}
	log.Fields = append(log.Fields, w.fields...)

	if WithTelemetry() {
		w.span.AddEvent(LogEvent, log.Event())
	}
	if w.logger != nil {
		w.logger.Process(log)
	}
}

func (w *Wool) Trace(msg string, fields ...*LogField) {
	w.process(TRACE, msg, fields...)
}

func (w *Wool) Info(msg string, fields ...*LogField) {
	w.process(INFO, msg, fields...)
}

func (w *Wool) Debug(msg string, fields ...*LogField) {
	w.process(DEBUG, msg, fields...)
}

func (w *Wool) Warn(msg string, fields ...*LogField) {
	w.process(WARN, msg, fields...)
}

func (w *Wool) Error(msg string, fields ...*LogField) {
	w.process(ERROR, msg, fields...)
}

func (w *Wool) Fatal(msg string, fields ...*LogField) {
	w.process(FATAL, msg, fields...)
}

func (w *Wool) Wrap(err error) error {
	if msg := w.Name(); msg != "" {
		return errors.Wrap(err, msg)
	}
	return err
}

func (w *Wool) Wrapf(err error, msg string, args ...any) error {
	msg = fmt.Sprintf(msg, args...)
	if name := w.Name(); name != "" {
		msg = fmt.Sprintf("%s: %s", w.Name(), msg)
	}
	if msg != "" {
		return errors.Wrap(err, msg)
	}
	return err
}

func (w *Wool) Close() {
	if w.span != nil {
		w.span.End()
	}
}

func (w *Wool) Source() *Identifier {
	return w.source
}

func (w *Wool) NewError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

type CodePath struct {
	Method string      `json:"method"`
	Fields []*LogField `json:"fields"`
}

const CodePathKey = "codepath"

func (w *Wool) StackTrace() []CodePath {
	b := baggage.FromContext(w.ctx)
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

func (w *Wool) Name() string {
	return w.name
}

func (w *Wool) WithLogger(l LogProcessor) *Wool {
	w.logger = l
	return w
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

func toCodePath(baggage.Property) CodePath {
	return CodePath{}
}

type CodeReference struct {
	Line int    `json:"line"`
	File string `json:"file"`
}

func (w *Wool) File() string {
	if w.ref == nil {
		return ""
	}
	return w.ref.File
}
