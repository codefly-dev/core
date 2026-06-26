package wool

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync/atomic"

	"github.com/pkg/errors"
)

// rethrowAfterCatch, when set, makes Catch re-raise the panic after logging it
// instead of swallowing it. The agent gRPC server opts into this (see
// agents.Serve) so a recovered panic propagates to the panic-recovery
// interceptor and becomes a proper RPC error the host can see — rather than a
// silently-swallowed log line that lets a half-initialized handler "succeed".
// Off by default so non-served callers (e.g. the CLI) keep the old
// keep-running-no-matter-what behavior.
var rethrowAfterCatch atomic.Bool

// SetRethrowAfterCatch toggles whether Catch re-raises after logging.
func SetRethrowAfterCatch(v bool) { rethrowAfterCatch.Store(v) }

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
		// The full goroutine stack is debug-only noise in the terminal — the
		// one-line panic value above is what the user needs, and the stack is
		// always captured in the log file. Run with --debug to see it inline.
		w.Debug(string(debug.Stack()))
		// In a served agent, re-raise so the recovery interceptor turns this
		// into an RPC error the host actually sees. A swallowed panic let the
		// handler return a zero value as if it succeeded — masking the failure
		// and cascading into later RPCs (e.g. Start running after Init panicked).
		if rethrowAfterCatch.Load() {
			panic(r)
		}
	}
}

// LogLevel returns the effective log level for this Wool instance.
// A zero-value (DEFAULT) instance level means "inherit the global level" —
// otherwise an instance never filters and custom processors (gRPC streaming,
// buffered, agent loggers) receive every TRACE/DEBUG line regardless of the
// configured level.
func (w *Wool) LogLevel() Loglevel {
	if w.logLevel == DEFAULT {
		return GlobalLogLevel()
	}
	return w.logLevel
}

func (w *Wool) process(l Loglevel, msg string, fs ...*LogField) {
	if w.LogLevel() > l {
		return
	}
	// Do not mutate caller-owned LogField pointers (they may be shared across
	// goroutines or reused across calls). Replace only those whose level is
	// DEFAULT with a shallow copy carrying the effective level.
	if len(fs) > 0 {
		out := make([]*LogField, len(fs))
		for i, f := range fs {
			if f != nil && f.Level == DEFAULT {
				cp := *f
				cp.Level = l
				out[i] = &cp
			} else {
				out[i] = f
			}
		}
		fs = out
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
	// Structured fields attached via In(...)/With(...) are intentionally NOT
	// inlined into the error message. They are a logging concern; dumping them
	// (often a whole struct via %v, e.g. an orchestration Action) into the
	// error string buries the real cause behind noise and pushes it past the
	// terminal width — leaving the user with a truncated, useless error. The
	// fields still travel with every log line emitted by this Wool.
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

const LogEvent = "log"
