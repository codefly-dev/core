package wool

import (
	"fmt"
	"reflect"
	"strings"
)

// LogProcessor is the sink interface for log messages.
type LogProcessor interface {
	Process(msg *Log)
}

// LogProcessorWithSource extends LogProcessor with source tracking.
type LogProcessorWithSource interface {
	ProcessWithSource(source *Identifier, msg *Log)
}

var system *Identifier

func init() {
	system = &Identifier{Kind: "system", Unique: "wool:system"}
}

// System returns the system identifier.
func System() *Identifier {
	return system
}

// IsSystem returns true if this is the system identifier.
func (identifier *Identifier) IsSystem() bool {
	return identifier.Unique == "wool:system"
}

// Log represents a structured log entry.
type Log struct {
	Level   Loglevel    `json:"level"`
	Header  string      `json:"header"`
	Message string      `json:"message"`
	Fields  []*LogField `json:"fields"`
	// TraceID and SpanID name the trace this line was emitted inside, when the
	// telemetry backend could report them (see SpanIdentity). They are the join
	// key between a log stream and a trace stream: logs leave a container on
	// stdout and are collected from the node, traces leave over OTLP, and these
	// two ids are the only thing that lets a reader move from a slow trace to
	// the lines it produced.
	//
	// Both are omitempty: a process with no telemetry enabled has no span, so
	// its lines carry neither, and nothing about a local run changes shape.
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
}

// AtLevel filters fields by the given log level.
func (l *Log) AtLevel(debug Loglevel) *Log {
	var fields []*LogField
	for _, f := range l.Fields {
		if f.Level >= debug {
			fields = append(fields, f)
		}
	}
	return &Log{
		Message: l.Message,
		Fields:  fields,
		// Field filtering must not drop correlation: a line whose ids were
		// filtered away is no longer joinable to its trace, which is the one
		// thing this copy is least entitled to decide.
		TraceID: l.TraceID,
		SpanID:  l.SpanID,
	}
}

// String formats a log entry.
// Format: (level) (this) (header) message [key=value, ...]
func (l *Log) String() string {
	if l.Level == FORWARD {
		return l.Message
	}
	var this *LogField
	var fields []*LogField
	for _, f := range l.Fields {
		if f.Key == "this" {
			this = f
			continue
		}
		fields = append(fields, f)
	}
	tokens := []string{fmt.Sprintf("(%s)", levelToString[l.Level])}
	if this != nil {
		tokens = append(tokens, fmt.Sprintf("(%s)", this.Value))
	}
	if l.Header != "" {
		tokens = append(tokens, fmt.Sprintf("(%s)", l.Header))
	}
	tokens = append(tokens, l.Message)
	for _, f := range fields {
		// Fields that render to nothing (empty/nil value) are noise — a bare
		// `key=` carries no information — so drop them from the line entirely.
		if s := f.String(); s != "" {
			tokens = append(tokens, s)
		}
	}
	// Correlation goes last so it never displaces the message, and only when a
	// span reported it: a run without telemetry renders exactly as before.
	if l.TraceID != "" {
		tokens = append(tokens, "trace_id="+l.TraceID)
	}
	if l.SpanID != "" {
		tokens = append(tokens, "span_id="+l.SpanID)
	}
	return strings.Join(tokens, " ")
}

// LogField is a key-value pair with an associated log level.
// A field is shown only if the log level is equal or higher than the field's level.
type LogField struct {
	Key   string   `json:"key"`
	Level Loglevel `json:"level"`
	Value any      `json:"value"`
}

// String renders the field as "key=value". It returns the empty string when
// the value renders to nothing (nil or empty), letting Log.String drop the
// field rather than emit a meaningless bare "key=".
func (f *LogField) String() string {
	v := f.renderValue()
	if v == "" {
		return ""
	}
	return fmt.Sprintf("%s=%s", f.Key, v)
}

// renderValue formats the field value, preferring fmt.Stringer over %v so
// domain types control their own representation instead of being dumped as a
// raw Go struct.
func (f *LogField) renderValue() string {
	if f.Value == nil {
		return ""
	}
	if stringer, ok := f.Value.(fmt.Stringer); ok {
		// A typed-nil pointer (e.g. (*T)(nil)) still satisfies fmt.Stringer, but
		// its String() may dereference the nil receiver and panic. Logging must
		// never panic, so render a nil underlying value as empty (and let
		// Log.String drop the field) rather than calling through.
		if rv := reflect.ValueOf(f.Value); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return ""
		}
		return stringer.String()
	}
	if s, ok := f.Value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", f.Value)
}

// Loglevel defines log severity.
type Loglevel int

// Levels are ordered by severity; a message is shown when its level is >= the
// effective log level (see Wool.LogLevel). FOCUS sits just above INFO and below
// WARN: it is a highlighted milestone. At the default INFO level FOCUS lines are
// shown (the highlight a user wants to see); running at FOCUS hides routine INFO
// chatter while keeping milestones, warnings and errors — the "signal only"
// view. FOCUS must stay above INFO so it is never accidentally filtered out by an
// INFO-level run.
//
// Choosing a level — the bar that keeps the default (INFO) stream readable:
//   - TRACE: pure internal bookkeeping only a codefly-internals dev would want —
//     per-step load/resolve cascades, hash computation, "sending request",
//     "running natively", "loaded agent pid=…". Never shown by default; a real
//     workspace (15+ services) emits these dozens of times.
//   - DEBUG: diagnostics worth seeing when something is actually wrong, but still
//     too noisy for a normal run — fallback paths taken, retries, resolved binary
//     paths, GitHub-lookup failures. Surfaced with --debug or CODEFLY_LOG.
//   - INFO: a small, high-value set per service — one aggregated resolution line,
//     "Will run N service(s)", lifecycle milestones. If a line repeats once per
//     service or per agent and carries no new fact, it belongs at TRACE.
//   - FOCUS: highlighted milestones (>> lines). WARN/ERROR: real problems. A
//     warning that fires identically for every agent should be emitted once, not
//     once per agent.
const (
	DEFAULT Loglevel = iota
	TRACE
	DEBUG
	INFO
	FOCUS
	WARN
	ERROR
	FATAL
	FORWARD
)

var levelToString = map[Loglevel]string{
	DEFAULT: "DEFAULT",
	TRACE:   "TRACE",
	DEBUG:   "DEBUG",
	INFO:    "INFO",
	FOCUS:   "FOCUS",
	WARN:    "WARN",
	ERROR:   "ERROR",
	FATAL:   "FATAL",
	FORWARD: "FORWARD",
}

// stringToLevel maps a lowercase level name to its Loglevel, for parsing
// per-scope overrides (see SetLogScopes / CODEFLY_LOG).
var stringToLevel = map[string]Loglevel{
	"trace": TRACE,
	"debug": DEBUG,
	"info":  INFO,
	"focus": FOCUS,
	"warn":  WARN,
	"error": ERROR,
	"fatal": FATAL,
}

// LevelFromString resolves a level name (case-insensitive, e.g. "debug") to a
// Loglevel. The second return is false for an unrecognized name.
func LevelFromString(s string) (Loglevel, bool) {
	l, ok := stringToLevel[strings.ToLower(strings.TrimSpace(s))]
	return l, ok
}

// String returns the human-readable name of the log level (e.g. "INFO"),
// or "L<n>" for an unknown value. Implements fmt.Stringer.
func (l Loglevel) String() string {
	if s, ok := levelToString[l]; ok {
		return s
	}
	return fmt.Sprintf("L%d", int(l))
}

// --- Field-level methods ---

func (f *LogField) Debug() *LogField {
	f.Level = DEBUG
	return f
}

func (f *LogField) Trace() *LogField {
	f.Level = TRACE
	return f
}

func (f *LogField) Error() *LogField {
	f.Level = ERROR
	return f
}

// --- Log constructors ---

func LogTrace(msg string, fields ...*LogField) *Log {
	return &Log{Message: msg, Fields: fields, Level: TRACE}
}

func LogError(err error, msg string, fields ...*LogField) *Log {
	log := &Log{Message: msg, Fields: fields, Level: ERROR}
	log.Fields = append(log.Fields, ErrField(err))
	return log
}

// --- Field constructors ---

func Field(key string, value any) *LogField {
	return &LogField{Key: key, Value: value}
}

type Unique interface {
	Unique() string
}

func ThisField(this Unique) *LogField {
	if this == nil {
		return &LogField{Key: "this", Value: "nil"}
	}
	return &LogField{Key: "this", Value: this.Unique()}
}

func NameField(name string) *LogField {
	return &LogField{Key: "name", Value: name}
}

func WorkspaceField(name string) *LogField {
	return &LogField{Key: "workspace", Value: name}
}

func ModuleField(name string) *LogField {
	return &LogField{Key: "module", Value: name}
}

func ServiceField(name string) *LogField {
	return &LogField{Key: "service", Value: name}
}

func TypeOf[T any]() string {
	var t T
	return fmt.Sprintf("%T", t)
}

func GenericField[T any]() *LogField {
	return &LogField{Key: "generic", Value: TypeOf[T]()}
}

func PointerField[T any](override *T) *LogField {
	if override == nil {
		return &LogField{Key: "pointer", Value: "nil"}
	}
	return &LogField{Key: "pointer", Value: *override}
}

func NullableField[T any](key string, value T) *LogField {
	var null T
	if reflect.DeepEqual(value, null) {
		return &LogField{Key: key, Value: "null"}
	}
	return &LogField{Key: key, Value: value}
}

func RequestField(req any) *LogField {
	return &LogField{Key: "request", Value: req}
}

func ResponseField(req any) *LogField {
	return &LogField{Key: "response", Value: req}
}

func FileField(file string) *LogField {
	return &LogField{Key: "file", Value: file}
}

func DirField(dir string) *LogField {
	return &LogField{Key: "dir", Value: dir}
}

func Path(dir string) *LogField {
	return &LogField{Key: "path", Value: dir}
}

func SliceCountField[T any](slice []T) *LogField {
	return &LogField{Key: "count", Value: len(slice)}
}

// ErrField wraps an error as a structured log field. Tolerates a nil
// err (returns "<nil>" rather than panicking) so it's safe to call from
// `defer Wool.Catch()` recovery paths and conditional-logging branches
// where `err` may legitimately be nil.
func ErrField(err error) *LogField {
	if err == nil {
		return &LogField{Key: "error", Value: "<nil>"}
	}
	return &LogField{Key: "error", Value: err.Error()}
}

func StatusOK() *LogField {
	return &LogField{Key: "status", Value: "OK"}
}

func StatusFailed() *LogField {
	return &LogField{Key: "status", Value: "FAILED"}
}

func FocusField() *LogField {
	return &LogField{Key: "focus", Value: "true"}
}

func InField(s string) *LogField {
	return &LogField{Key: "in", Value: s}
}

func Writer() *LogField {
	return &LogField{Key: "writer"}
}

// SecretField redacts the value at construction: the raw secret is dropped and
// never reaches any sink (console, file, gRPC, telemetry). The field always
// renders "****". This makes redaction a logging-layer guarantee rather than a
// convention each call site has to remember.
func SecretField(key string, _ any) *LogField {
	return &LogField{Key: key, Value: "****"}
}

// sliceValue renders a slice as a bracketed, comma-separated list ("[a, b]"),
// or "none" when empty — instead of a raw "{1 [a b]}" %v struct dump. Elements
// that implement fmt.Stringer render via String().
type sliceValue[T any] struct {
	items []T
}

func (s sliceValue[T]) String() string {
	if len(s.items) == 0 {
		return "none"
	}
	parts := make([]string, len(s.items))
	for i, it := range s.items {
		if str, ok := any(it).(fmt.Stringer); ok {
			parts[i] = str.String()
		} else {
			parts[i] = fmt.Sprintf("%v", it)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// SliceField formats a slice as a readable list (see sliceValue) so call sites
// can log domain collections without dumping internal struct layout.
func SliceField[T any](key string, items []T) *LogField {
	return &LogField{Key: key, Value: sliceValue[T]{items: items}}
}
