package wool

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

/*

This is how open-telemetry view logs

span.AddEvent("LogEvent", trace.WithAttributes(
attribute.String("message", "This is a log message"),
attribute.Int("someIntValue", 42),
attribute.String("severityText", "INFO"),
))
*/

// LogField is a key value pair with a log level

type LogField struct {
	Key   string
	Level Loglevel
	Value any
}

type Loglevel int

const (
	DEBUG Loglevel = iota
	INFO
)

func Field(key string, value any) *LogField {
	return &LogField{Key: key, Value: value, Level: INFO}
}

func DebugField(key string, value string) *LogField {
	return &LogField{Key: key, Value: value, Level: DEBUG}
}

type Log struct {
	Message string
	Fields  []*LogField
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
