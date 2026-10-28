package runnable

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// SchemaJSON renders the current bounded field profile for an existing JSON
// Schema registry. Field names, optionality and nullability are exact; no
// spelling aliases or scalar coercions are added. A compiler consuming this
// schema must use ConfigureJSONSchemaCompiler so formats are assertions.
func SchemaJSON(schema *basev0.RunnableSchema) (json.RawMessage, error) {
	if schema == nil {
		return nil, fmt.Errorf("%w: schema is required", ErrInvalid)
	}
	budget := MaxProjectionFields
	if err := boundSchema(schema.GetFields(), 0, &budget); err != nil {
		return nil, err
	}
	if carriesUnknownFields(schema.ProtoReflect()) {
		return nil, fmt.Errorf("%w: schema contains unknown fields", ErrInvalid)
	}
	// Reuse the declaration/descriptor validator after bounding traversal, so
	// even an in-process cyclic protobuf cannot recurse without a limit.
	if _, err := resources.RunnableContractFromProto(&basev0.RunnableContract{
		Protocol: resources.RunnableServiceProtocolV1, Input: schema, Output: schema,
	}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return json.Marshal(schemaObject(schema.GetFields()))
}

func boundSchema(fields []*basev0.RunnableField, depth int, budget *int) error {
	if depth > MaxProjectionDepth {
		return fmt.Errorf("%w: schema exceeds bounded depth", ErrInvalid)
	}
	for _, field := range fields {
		*budget--
		if field == nil || *budget < 0 {
			return fmt.Errorf("%w: schema has nil fields or exceeds field budget", ErrInvalid)
		}
		if len(field.GetFields()) != 0 {
			if err := boundSchema(field.GetFields(), depth+1, budget); err != nil {
				return err
			}
		}
		if field.GetItems() != nil {
			if err := boundSchema([]*basev0.RunnableField{field.GetItems()}, depth+1, budget); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaObject(fields []*basev0.RunnableField) map[string]any {
	properties := make(map[string]any, len(fields))
	required := []string{}
	for _, field := range fields {
		properties[field.GetName()] = schemaField(field)
		if !field.GetOptional() {
			required = append(required, field.GetName())
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func schemaField(field *basev0.RunnableField) map[string]any {
	value := map[string]any{}
	switch field.GetType() {
	case basev0.RunnableField_STRING:
		value["type"] = "string"
		if field.GetFormat() != "" {
			value["format"] = field.GetFormat()
		}
		if len(field.GetValues()) != 0 {
			value["enum"] = field.GetValues()
		}
	case basev0.RunnableField_INTEGER:
		value["type"], value["minimum"], value["maximum"] = "integer", int64(math.MinInt64), int64(math.MaxInt64)
	case basev0.RunnableField_BOOLEAN:
		value["type"] = "boolean"
	case basev0.RunnableField_OBJECT:
		value = schemaObject(field.GetFields())
	case basev0.RunnableField_ARRAY:
		value["type"], value["items"] = "array", schemaField(field.GetItems())
	case basev0.RunnableField_NUMBER:
		value["anyOf"] = []any{map[string]any{"type": "number"}, map[string]any{"enum": []string{"NaN", "Infinity", "-Infinity"}}}
	case basev0.RunnableField_JSON:
		if field.GetFormat() != "" {
			value["type"] = field.GetFormat() // validated: object or array
		} else if !field.GetNullable() {
			value["not"] = map[string]any{"type": "null"}
		}
	}
	if field.GetNullable() {
		return map[string]any{"anyOf": []any{value, map[string]any{"type": "null"}}}
	}
	return value
}

// ConfigureJSONSchemaCompiler installs the bounded profile's string formats
// on an existing v6 compiler and enables format assertions. It adds no loader,
// registry or parser. Use it before compiling schemas returned by SchemaJSON.
// The formats describe canonical ProtoJSON output, not every spelling that a
// permissive protobuf input decoder may also accept.
func ConfigureJSONSchemaCompiler(compiler *jsonschema.Compiler) error {
	if compiler == nil {
		return fmt.Errorf("%w: JSON Schema compiler is required", ErrInvalid)
	}
	for _, format := range []struct {
		name    string
		message proto.Message
	}{
		{FormatDateTime, &timestamppb.Timestamp{}},
		{FormatProtobufDuration, &durationpb.Duration{}},
		{FormatFieldMask, &fieldmaskpb.FieldMask{}},
		{FormatByte, &wrapperspb.BytesValue{}},
		{FormatInt64, &wrapperspb.Int64Value{}},
		{FormatUint64, &wrapperspb.UInt64Value{}},
	} {
		compiler.RegisterFormat(&jsonschema.Format{Name: format.name, Validate: canonicalProtoString(format.message)})
	}
	compiler.AssertFormat()
	return nil
}

func canonicalProtoString(prototype proto.Message) func(any) error {
	return func(value any) error {
		text, ok := value.(string)
		if !ok {
			return nil // JSON Schema's type assertion owns non-strings.
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return fmt.Errorf("invalid ProtoJSON string")
		}
		message := prototype.ProtoReflect().New().Interface()
		if err = protojson.Unmarshal(encoded, message); err != nil {
			return fmt.Errorf("invalid ProtoJSON string")
		}
		canonical, err := protojson.Marshal(message)
		if err != nil {
			return fmt.Errorf("invalid ProtoJSON string")
		}
		var written string
		if err = json.Unmarshal(canonical, &written); err != nil || written != text {
			return fmt.Errorf("noncanonical ProtoJSON string")
		}
		return nil
	}
}
