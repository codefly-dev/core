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
		tokens = append(tokens, f.String())
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

func (f *LogField) String() string {
	if f.Value == nil {
		return fmt.Sprintf("%s=nil", f.Key)
	}
	if stringer, ok := f.Value.(fmt.Stringer); ok {
		return fmt.Sprintf("%s=%s", f.Key, stringer.String())
	}
	return fmt.Sprintf("%s=%v", f.Key, f.Value)
}

// Loglevel defines log severity.
type Loglevel int

const (
	DEFAULT Loglevel = iota
	TRACE
	DEBUG
	FOCUS
	INFO
	WARN
	ERROR
	FATAL
	FORWARD
)

var levelToString = map[Loglevel]string{
	TRACE:   "TRACE",
	DEBUG:   "DEBUG",
	FOCUS:   "FOCUS",
	INFO:    "INFO",
	WARN:    "WARN",
	ERROR:   "ERROR",
	FATAL:   "FATAL",
	FORWARD: "FORWARD",
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
