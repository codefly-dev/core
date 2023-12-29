package wool

import (
	"fmt"
	"reflect"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type LogProcessor interface {
	Process(msg *Log)
}

type LogProcessorWithSource interface {
	ProcessWithSource(msg *Log, source *Identifier)
}

var system *Identifier

func init() {
	system = &Identifier{Kind: "system", Unique: "wool:system"}
}

func System() *Identifier {
	return system
}

func (identifier *Identifier) IsSystem() bool {
	return identifier.Unique == "wool:system"
}

/*

This is how open-telemetry view logs

span.AddEvent("LogEvent", trace.WithAttributes(
attribute.String("message", "This is a log message"),
attribute.Int("someIntValue", 42),
attribute.String("severityText", "INFO"),
))
*/

type Log struct {
	Level   Loglevel    `json:"level"`
	Header  string      `json:"header"`
	Message string      `json:"message"`
	Fields  []*LogField `json:"fields"`
}

func eventOption(f *LogField) attribute.KeyValue {
	switch f.Value.(type) {
	case string:
		return attribute.String(f.Key, f.Value.(string))
	case int:
		return attribute.Int(f.Key, f.Value.(int))
	case int64:
		return attribute.Int64(f.Key, f.Value.(int64))
	case float64:
		return attribute.Float64(f.Key, f.Value.(float64))
	case bool:
		return attribute.Bool(f.Key, f.Value.(bool))
	default:
		return attribute.String(f.Key, "unknown")
	}
}

func (l *Log) Event() trace.SpanStartEventOption {
	var attrs []attribute.KeyValue
	for _, f := range l.Fields {
		attrs = append(attrs, eventOption(f))
	}
	return trace.WithAttributes(attrs...)
}

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

// String display a log message into this format
// (level) (this) message [key=value, key=value]
func (l *Log) String() string {
	// Forward case
	if l.Level == FORWARD {
		return l.Message
	}
	// We treat the "this" field differently
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

// LogField is a key value pair with a log level
// A Field is shown only if the log level is equal or higher than the log level of the log
type LogField struct {
	Key   string   `json:"key"`
	Level Loglevel `json:"level"`
	Value any      `json:"value"`
}

func (f *LogField) String() string {
	if f.Value == nil {
		return fmt.Sprintf("%s=nil", f.Key)
	}
	return fmt.Sprintf("%s=%v", f.Key, f.Value)
}

type Loglevel int

const (
	DEFAULT Loglevel = iota
	TRACE
	DEBUG
	INFO
	WARN
	ERROR
	FATAL
	FOCUS
	FORWARD
)

var levelToString = map[Loglevel]string{
	TRACE:   "TRACE",
	DEBUG:   "DEBUG",
	INFO:    "INFO",
	WARN:    "WARN",
	ERROR:   "ERROR",
	FATAL:   "FATAL",
	FOCUS:   "FOCUS",
	FORWARD: "FORWARD",
}

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

func LogTrace(msg string, fields ...*LogField) *Log {
	log := &Log{Message: msg, Fields: fields, Level: TRACE}
	return log
}

func LogError(err error, msg string, fields ...*LogField) *Log {
	log := &Log{Message: msg, Fields: fields, Level: ERROR}
	log.Fields = append(log.Fields, ErrField(err))
	return log
}

// Field with default level
func Field(key string, value any) *LogField {
	return &LogField{Key: key, Value: value}
}

// Conventions

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

func PathField(dir string) *LogField {
	return &LogField{Key: "path", Value: dir}
}

func SliceCountField[T any](slice []T) *LogField {
	return &LogField{Key: "count", Value: len(slice)}
}

func ErrField(err error) *LogField {
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
