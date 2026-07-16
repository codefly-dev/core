package toolbox

import (
	"math"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
)

const RedactedValue = "[REDACTED]"

// RedactWithSchema returns a JSON-shaped copy safe for diagnostics. Schema
// fields marked writeOnly, x-codefly-sensitive, or with a sensitive
// x-codefly-classification are replaced. Unknown object fields and values with
// no schema are redacted fail-closed.
func RedactWithSchema(schema *structpb.Struct, value any) any {
	if schema == nil {
		return RedactedValue
	}
	return redactJSON(schema.AsMap(), value)
}

func redactJSON(schema map[string]any, value any) any {
	if mustRedactSchema(schema) {
		return RedactedValue
	}
	schemaType, _ := schema["type"].(string)
	switch typed := value.(type) {
	case map[string]any:
		if schemaType != "object" {
			return RedactedValue
		}
		properties, _ := schema["properties"].(map[string]any)
		out := make(map[string]any, len(typed))
		for key, field := range typed {
			fieldSchema, ok := properties[key].(map[string]any)
			if !ok {
				out[key] = RedactedValue
				continue
			}
			out[key] = redactJSON(fieldSchema, field)
		}
		return out
	case []any:
		if schemaType != "array" {
			return RedactedValue
		}
		itemSchema, ok := schema["items"].(map[string]any)
		if !ok {
			return RedactedValue
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = redactJSON(itemSchema, item)
		}
		return out
	case string:
		if schemaType != "string" {
			return RedactedValue
		}
		return typed
	case bool:
		if schemaType != "boolean" {
			return RedactedValue
		}
		return typed
	case float64:
		if schemaType != "number" && (schemaType != "integer" || math.Trunc(typed) != typed) {
			return RedactedValue
		}
		return typed
	case float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		if schemaType != "number" && schemaType != "integer" {
			return RedactedValue
		}
		return typed
	case nil:
		if schemaType != "null" {
			return RedactedValue
		}
		return nil
	default:
		return RedactedValue
	}
}

func mustRedactSchema(schema map[string]any) bool {
	if len(schema) == 0 {
		return true
	}
	// This redactor deliberately supports only direct object/array/primitive
	// schemas. Composite and reference keywords require full evaluation; treating
	// them as ordinary metadata could expose a value classified in another branch.
	for _, keyword := range []string{
		"$ref", "$dynamicRef", "allOf", "anyOf", "oneOf", "not", "if", "then", "else",
		"dependentSchemas", "patternProperties", "unevaluatedProperties", "prefixItems", "contains",
	} {
		if _, present := schema[keyword]; present {
			return true
		}
	}
	if raw, present := schema["x-codefly-sensitive"]; present {
		sensitive, ok := raw.(bool)
		if !ok || sensitive {
			return true
		}
	}
	if raw, present := schema["writeOnly"]; present {
		writeOnly, ok := raw.(bool)
		if !ok || writeOnly {
			return true
		}
	}
	rawClassification, classified := schema["x-codefly-classification"]
	classification, ok := rawClassification.(string)
	if classified && !ok {
		return true
	}
	switch strings.ToLower(classification) {
	case "secret", "credential", "sensitive", "confidential", "principal", "tenant":
		return true
	case "", "public", "internal":
		return false
	default:
		return true
	}
}
