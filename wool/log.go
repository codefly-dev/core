package wool

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type LogProcessor interface {
	Process(msg *Log)
}

type LogProcessorWithSource interface {
	ProcessWithSource(msg *Log, source *Identifier)
}

/*

This is how open-telemetry view logs

span.AddEvent("LogEvent", trace.WithAttributes(
attribute.String("message", "This is a log message"),
attribute.Int("someIntValue", 42),
attribute.String("severityText", "INFO"),
))
*/

// LogField is a key value pair with a log level
// A Field is shown only if the log level is equal or higher than the log level of the log
type LogField struct {
	Key   string   `json:"key"`
	Level Loglevel `json:"level"`
	Value any      `json:"value"`
}

func (f *LogField) String() string {
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
)

// DEFAULT will inherit its level from the statement

func TraceField(key string, value string) *LogField {
	return &LogField{Key: key, Value: value, Level: TRACE}
}

func DebugField(key string, value string) *LogField {
	return &LogField{Key: key, Value: value, Level: DEBUG}
}

func InfoField(key string, value any) *LogField {
	return &LogField{Key: key, Value: value, Level: INFO}
}

func WarnField(key string, value any) *LogField {
	return &LogField{Key: key, Value: value, Level: WARN}
}

func ErrorField(s string, value any) *LogField {
	return &LogField{Key: s, Value: value, Level: ERROR}
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

func RequestField(req any) *LogField {
	return &LogField{Key: "request", Value: req}
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
	return &LogField{Key: "error", Value: err}
}

func StatusOK() *LogField {
	return &LogField{Key: "status", Value: "OK"}
}

func StatusFailed() *LogField {
	return &LogField{Key: "status", Value: "FAILED"}
}

type Log struct {
	Level   Loglevel    `json:"level"`
	Message string      `json:"message"`
	Header  string      `json:"header"`
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

func (l *Log) String() string {
	return fmt.Sprintf("[%s] %s %s", l.Header, l.Message, l.Fields)
}
