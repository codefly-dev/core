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

	logger       LogProcessor
	logLevel     Loglevel
	disableCatch bool
}

type LogFunc func(string, ...*LogField)

func Writer() *LogField {
	return &LogField{Key: "writer"}
}

// Writer implements the io.WriteCloser interface
func (w *Wool) Write(p []byte) (n int, err error) {
	return w.Forward(p)
}

func (w *Wool) Close() error {
	return nil
}

func (w *Wool) In(method string, fields ...*LogField) *Wool {
	newW := &Wool{
		source:   w.source,
		ref:      w.ref,
		ctx:      w.ctx,
		provider: w.provider,
		logger:   w.logger,
		logLevel: w.logLevel,
	}
	newW.name = method
	newW.fields = fields
	// We keep track of the stack
	// stack := c.Stack()
	return newW
}

func (w *Wool) With(fields ...*LogField) *Wool {
	w.fields = append(w.fields, fields...)
	return w
}

func (w *Wool) Inject(ctx context.Context) context.Context {
	return w.provider.Inject(ctx)
}

// Catch recovers from a panic and logs the error
func (w *Wool) Catch() {
	if w == nil {
		return
	}
	if w.disableCatch {
		return
	}
	if r := recover(); r != nil {
		w.Warn("PANIC CAUGHT INSIDE THE AGENT CODE ", Field("panic", r))
		w.Warn(string(debug.Stack()))
	}
}

func (w *Wool) LogLevel() Loglevel {
	g := GlobalLogLevel()
	if w.logLevel > g {
		return g
	}
	return w.logLevel
}

func (w *Wool) process(l Loglevel, msg string, fs ...*LogField) {
	if w.LogLevel() > l {
		return
	}
	for _, f := range fs {
		if f.Level == DEFAULT {
			f.Level = l
		}
	}
	// If LogLevel is ERROR, always add the code reference information

	// if l >= ERROR {
	//	ref, err := getFileInfo(3)
	//	if err == nil {
	//		fs = append(fs, &LogField{
	//			Key:   "code",
	//			Value: ref,
	//		})
	//	}
	// }

	log := &Log{Message: msg, Fields: fs, Level: l, Header: w.name}
	log.Fields = append(log.Fields, w.fields...)

	if WithTelemetry() {
		w.span.AddEvent(LogEvent, log.Event())
	}
	if w.logger != nil {
		w.logger.Process(log)
	}
}

// func getFileInfo(depth int) (*CodeReference, error) {
//	_, file, line, ok := runtime.Caller(depth)
//	if !ok {
//		return nil, errors.New("cannot get caller information")
//	}
//	return &CodeReference{
//		Line: line,
//		File: file,
//	}, nil
// }

func (w *Wool) Forward(p []byte) (n int, err error) {
	w.process(FORWARD, string(p))
	return len(p), nil
}

func (w *Wool) Forwardf(msg string, args ...any) {
	w.process(FORWARD, fmt.Sprintf(msg, args...))
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

func (w *Wool) Focus(msg string, fields ...*LogField) {
	w.process(FOCUS, msg, fields...)
}

func (w *Wool) Wrap(err error) error {
	if msg := w.Name(); msg != "" {
		return errors.Wrap(err, msg)
	}
	return err
}

func (w *Wool) Wrapf(err error, msg string, args ...any) error {
	msg = fmt.Sprintf(msg, args...)
	if len(w.fields) > 0 {
		fields := fmt.Sprintf("%v", w.fields)
		msg = fmt.Sprintf("%s: %s", msg, fields)
	}
	if name := w.Name(); name != "" {
		msg = fmt.Sprintf("%s: %s", w.Name(), msg)
	}
	if msg != "" {
		return errors.Wrap(err, msg)
	}
	return err
}

type NotFoundError struct {
	what ContextKey
}

func (err *NotFoundError) Error() string {
	return fmt.Sprintf("<%s> not found", err.what)
}

func NotFound(what ContextKey) error {
	return &NotFoundError{what: what}
}

func (w *Wool) lookup(key ContextKey) (string, bool) {
	// Check context Key first

	if value, ok := w.ctx.Value(key).(string); ok {
		return value, true
	}
	return "", false
}

func (w *Wool) with(key ContextKey, value string) {
	// Add to context values
	w.ctx = context.WithValue(w.ctx, key, value)
}

type ContextKey string

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

const ProviderKey = ContextKey("provider")

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

func (w *Wool) WithLoglevel(level Loglevel) {
	w.logLevel = level
}

func (w *Wool) DisableCatch() {
	w.disableCatch = true
}

func (w *Wool) HTTP() *HTTP {
	return &HTTP{w: w}
}

func (w *Wool) GRPC() *GRPC {
	return &GRPC{w: w}
}

func (w *Wool) Context() context.Context {
	return w.ctx
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

func (c *CodeReference) String() string {
	return fmt.Sprintf("%s:%d", c.File, c.Line)
}
