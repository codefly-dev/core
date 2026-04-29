package wool

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/pkg/errors"
)

// Wool is the main observability handle. It combines structured logging,
// optional tracing (via TelemetryProvider), and context propagation.
//
// Usage:
//
//	w := wool.Get(ctx).In("MyService.Handle")
//	w.Info("processing request", wool.Field("id", 42))
//	if err != nil {
//	    return w.Wrapf(err, "cannot process")
//	}
type Wool struct {
	name   string
	source *Identifier
	ref    *CodeReference

	fields []*LogField

	ctx context.Context

	provider *Provider
	span     Span // nil when telemetry is not enabled

	logger       LogProcessor
	logLevel     Loglevel
	disableCatch bool
}

type LogFunc func(string, ...*LogField)

// Write implements io.Writer -- forwards bytes as FORWARD-level log entries.
func (w *Wool) Write(p []byte) (n int, err error) {
	return w.Forward(p)
}

// Close implements io.Closer (no-op).
func (w *Wool) Close() error {
	return nil
}

// In creates a scoped child Wool with a method name and optional fields.
func (w *Wool) In(method string, fields ...*LogField) *Wool {
	newW := &Wool{
		source:   w.source,
		ref:      w.ref,
		ctx:      w.ctx,
		provider: w.provider,
		logger:   w.logger,
		logLevel: w.logLevel,
		span:     w.span,
	}
	newW.name = method
	newW.fields = fields
	return newW
}

// With appends fields to this Wool instance.
func (w *Wool) With(fields ...*LogField) *Wool {
	w.fields = append(w.fields, fields...)
	return w
}

// Inject stores the provider in the context for later retrieval via Get.
// Inject persists this Wool's provider on ctx so downstream code that
// calls wool.Get(ctx) sees the same call-chain context. No-op when
// no provider is registered (test harnesses, agent boot before
// telemetry is wired) — without this guard, the previous version
// nil-deref'd and brought down the calling goroutine.
func (w *Wool) Inject(ctx context.Context) context.Context {
	if w == nil || w.provider == nil {
		return ctx
	}
	return w.provider.Inject(ctx)
}

// Catch recovers from a panic and logs the error.
// Use with defer: defer w.Catch()
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

// LogLevel returns the effective log level for this Wool instance.
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

	log := &Log{Message: msg, Fields: fs, Level: l, Header: w.name}
	log.Fields = append(log.Fields, w.fields...)

	// Send to telemetry if enabled
	if w.span != nil {
		w.span.AddEvent(LogEvent, log.Fields)
	}

	if w.logger != nil {
		w.logger.Process(log)
	}
}

// --- Logging methods ---

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

// --- Error wrapping ---

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

func (w *Wool) NewError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// --- Context helpers ---

type ContextKey string

const ProviderKey = ContextKey("provider")

func (w *Wool) lookup(key ContextKey) (string, bool) {
	if value, ok := w.ctx.Value(key).(string); ok {
		return value, true
	}
	return "", false
}

func (w *Wool) with(key ContextKey, value string) {
	w.ctx = context.WithValue(w.ctx, key, value)
}

// --- Accessors ---

func (w *Wool) Name() string {
	return w.name
}

func (w *Wool) Source() *Identifier {
	return w.source
}

func (w *Wool) Context() context.Context {
	return w.ctx
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

// --- Not Found error ---

type NotFoundError struct {
	what ContextKey
}

func (err *NotFoundError) Error() string {
	return fmt.Sprintf("<%s> not found", err.what)
}

func NotFound(what ContextKey) error {
	return &NotFoundError{what: what}
}

// --- Code references ---

type CodeReference struct {
	Line int    `json:"line"`
	File string `json:"file"`
}

func (c *CodeReference) String() string {
	return fmt.Sprintf("%s:%d", c.File, c.Line)
}

type CodePath struct {
	Method string      `json:"method"`
	Fields []*LogField `json:"fields"`
}

const LogEvent = "log"
