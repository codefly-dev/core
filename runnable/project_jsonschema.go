package runnable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// rejectedStringFormats carry a value the bounded profile has no
// representation for. They are what the protobuf types the proto projection
// rejects transcode to — bytes becomes byte or binary, Timestamp becomes
// date-time, a calendar date becomes date — so a payload refused as a
// descriptor is refused as a document too. Every other format annotates a
// string the profile already carries: uuid, email and uri are strings.
var rejectedStringFormats = []string{"binary", "byte", "date", "date-time"}

// acceptedIntegerFormats are the widths the profile's signed 64-bit integer
// covers. Any other width names a range it does not.
var acceptedIntegerFormats = []string{"", "int32", "int64"}

// JSONSchemaType is a schema's declared type, which JSON Schema spells either
// as one name or as a list of them.
type JSONSchemaType []string

func (t *JSONSchemaType) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*t = JSONSchemaType{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("type is neither a name nor a list of names: %w", err)
	}
	*t = many
	return nil
}

// JSONSchemaProperty is one member of an object schema.
type JSONSchemaProperty struct {
	Name   string
	Schema *JSONSchema
}

// JSONSchemaProperties keeps an object's members in the order the document
// spells them. The bounded profile's fields are an ordered list, so decoding
// them into a map would make the projection — and the package digest taken
// over it — depend on Go's map iteration order.
type JSONSchemaProperties []JSONSchemaProperty

func (p *JSONSchemaProperties) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening != json.Delim('{') {
		return fmt.Errorf("properties is not an object")
	}
	members := JSONSchemaProperties{}
	for decoder.More() {
		key, keyErr := decoder.Token()
		if keyErr != nil {
			return keyErr
		}
		member := JSONSchemaProperty{Name: key.(string), Schema: &JSONSchema{}}
		if err = decoder.Decode(member.Schema); err != nil {
			return err
		}
		members = append(members, member)
	}
	*p = members
	return nil
}

// JSONSchema is the part of a JSON Schema the bounded profile reads. Keywords
// outside it are of two kinds: an annotation such as title or example, which
// says nothing about the value's type and is ignored, and a keyword naming a
// shape the profile does not carry, which is named here so it is rejected
// rather than silently dropped.
type JSONSchema struct {
	Ref      string          `json:"$ref"`
	Type     JSONSchemaType  `json:"type"`
	Format   string          `json:"format"`
	Nullable bool            `json:"nullable"`
	Required []string        `json:"required"`
	Enum     json.RawMessage `json:"enum"`
	OneOf    json.RawMessage `json:"oneOf"`
	AnyOf    json.RawMessage `json:"anyOf"`
	AllOf    json.RawMessage `json:"allOf"`
	// Properties is a pointer because an object declaring no members and one
	// declaring none are different schemas: the second is free-form.
	Properties *JSONSchemaProperties `json:"properties"`
	// Items and AdditionalProperties are read as written because their shape
	// is the decision: items may be a list (a tuple) and additionalProperties
	// may be a boolean or a schema.
	Items                json.RawMessage `json:"items"`
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
}

// ProjectJSONSchema projects a JSON Schema onto the bounded schema profile. It
// is the OpenAPI reader of the one profile ProjectMessage reads a descriptor
// into, and the two tables are held equal by a fixture pair rather than by two
// readings agreeing: a proto method and the document transcoded from it
// project to the same RunnableSchema.
//
// A schema outside the profile is rejected, named by its JSON pointer, and is
// never coerced. doc resolves a $ref; a schema carrying none needs no document.
func ProjectJSONSchema(doc *OpenAPIDocument, schema *JSONSchema) (*basev0.RunnableSchema, error) {
	if schema == nil {
		return nil, fmt.Errorf("%w: schema is required", ErrInvalid)
	}
	budget := MaxProjectionFields
	// Resolving here rather than only inside the walk is what lets the payload's
	// own rejections name the component they are about.
	resolved, at, open, err := resolve(doc, schema, "#", nil)
	if err != nil {
		return nil, err
	}
	payload, err := projectSchema(doc, resolved, at, open, 0, &budget)
	if err != nil {
		return nil, err
	}
	// The profile's payload is always an object, so a schema that projects to
	// anything else describes a body no invocation could carry.
	if payload.GetType() != basev0.RunnableField_OBJECT {
		return nil, fmt.Errorf("%w: %s is not an object, and an invocation payload always is", ErrInvalid, at)
	}
	if payload.GetNullable() {
		return nil, fmt.Errorf("%w: %s is nullable, and an invocation payload is always present", ErrInvalid, at)
	}
	return &basev0.RunnableSchema{Fields: payload.GetFields()}, nil
}

// projectSchema projects one schema into an unnamed field. open is the chain of
// $refs already being resolved, so it is the cycle check: a schema that reaches
// itself describes an unbounded payload, not a deep one. depth counts the
// objects already open, which no chain of inline schemas carries a name for.
func projectSchema(doc *OpenAPIDocument, schema *JSONSchema, at string, open []string, depth int, budget *int) (*basev0.RunnableField, error) {
	resolved, at, open, err := resolve(doc, schema, at, open)
	if err != nil {
		return nil, err
	}
	for _, composed := range []struct {
		keyword string
		value   json.RawMessage
	}{{"enum", resolved.Enum}, {"oneOf", resolved.OneOf}, {"anyOf", resolved.AnyOf}, {"allOf", resolved.AllOf}} {
		if len(composed.value) > 0 {
			return nil, fmt.Errorf("%w: %s declares %s, which the bounded profile does not cover", ErrInvalid, at, composed.keyword)
		}
	}
	named, nullable, err := boundedType(resolved, at)
	if err != nil {
		return nil, err
	}
	field := &basev0.RunnableField{Nullable: nullable}
	switch named {
	case "string":
		if slices.Contains(rejectedStringFormats, resolved.Format) {
			return nil, fmt.Errorf("%w: %s is a string of format %q, which carries a value the bounded profile has no representation for", ErrInvalid, at, resolved.Format)
		}
		field.Type = basev0.RunnableField_STRING
	case "integer":
		if !slices.Contains(acceptedIntegerFormats, resolved.Format) {
			return nil, fmt.Errorf("%w: %s is an integer of format %q, whose range the signed 64-bit integer the bounded profile carries does not cover", ErrInvalid, at, resolved.Format)
		}
		field.Type = basev0.RunnableField_INTEGER
	case "boolean":
		field.Type = basev0.RunnableField_BOOLEAN
	case "object":
		fields, objectErr := projectProperties(doc, resolved, at, open, depth, budget)
		if objectErr != nil {
			return nil, objectErr
		}
		field.Type, field.Fields = basev0.RunnableField_OBJECT, fields
	case "array":
		items, itemsErr := projectItems(doc, resolved, at, open, depth, budget)
		if itemsErr != nil {
			return nil, itemsErr
		}
		field.Type, field.Items = basev0.RunnableField_ARRAY, items
	default:
		return nil, fmt.Errorf("%w: %s is of type %q, which the bounded profile does not cover", ErrInvalid, at, named)
	}
	return field, nil
}

// resolve follows a $ref to the component it names. A $ref beside other
// keywords is refused rather than merged: which of the two wins is exactly the
// kind of reading two implementations disagree about.
func resolve(doc *OpenAPIDocument, schema *JSONSchema, at string, open []string) (*JSONSchema, string, []string, error) {
	for schema.Ref != "" {
		if !onlyRef(schema) {
			return nil, "", nil, fmt.Errorf("%w: %s is a $ref beside other keywords, which the bounded profile does not merge", ErrInvalid, at)
		}
		if slices.Contains(open, schema.Ref) {
			return nil, "", nil, fmt.Errorf("%w: %s reaches %s again, and a recursive payload has no bounded shape", ErrInvalid, at, schema.Ref)
		}
		target, found := doc.componentSchema(schema.Ref)
		if !found {
			return nil, "", nil, fmt.Errorf("%w: %s references %s, which the document does not define", ErrInvalid, at, schema.Ref)
		}
		open = append(open, schema.Ref)
		at, schema = schema.Ref, target
	}
	return schema, at, open, nil
}

// onlyRef reports whether $ref is the whole schema. Nullable is read off the
// declaration rather than compared, because false is both its zero value and a
// legitimate spelling.
func onlyRef(schema *JSONSchema) bool {
	return len(schema.Type) == 0 && schema.Format == "" && !schema.Nullable && schema.Required == nil &&
		len(schema.Enum) == 0 && len(schema.OneOf) == 0 && len(schema.AnyOf) == 0 && len(schema.AllOf) == 0 &&
		schema.Properties == nil && len(schema.Items) == 0 && len(schema.AdditionalProperties) == 0
}

// boundedType reads the one type a schema declares and whether its value may be
// null, which OpenAPI 3.0 spells as nullable and JSON Schema as a "null" member
// of the type list.
func boundedType(schema *JSONSchema, at string) (string, bool, error) {
	nullable := schema.Nullable
	named := make([]string, 0, len(schema.Type))
	for _, declared := range schema.Type {
		if declared == "null" {
			nullable = true
			continue
		}
		named = append(named, declared)
	}
	switch {
	case len(named) == 0:
		return "", false, fmt.Errorf("%w: %s declares no type, and the bounded profile has no untyped value", ErrInvalid, at)
	case len(named) > 1:
		return "", false, fmt.Errorf("%w: %s declares types %s, and the bounded profile has no union", ErrInvalid, at, strings.Join(named, ", "))
	}
	return named[0], nullable, nil
}

// projectProperties walks one object. An object with no properties is free-form
// and one accepting additional properties is a map, and neither has the fixed
// set of typed members the profile describes.
func projectProperties(doc *OpenAPIDocument, schema *JSONSchema, at string, open []string, depth int, budget *int) ([]*basev0.RunnableField, error) {
	if schema.Properties == nil {
		return nil, fmt.Errorf("%w: %s is an object declaring no properties, which the bounded profile does not cover", ErrInvalid, at)
	}
	if !bytes.Equal(bytes.TrimSpace(schema.AdditionalProperties), []byte("false")) {
		return nil, fmt.Errorf("%w: %s is an object that does not close additionalProperties, and the bounded profile describes a fixed set of members", ErrInvalid, at)
	}
	if depth >= MaxProjectionDepth {
		return nil, fmt.Errorf("%w: %s nests more than %d objects deep", ErrInvalid, at, MaxProjectionDepth)
	}
	// A name in required that properties does not declare would otherwise be
	// dropped in silence: the object closes additionalProperties, so the
	// declaration is unsatisfiable, and the derived contract would say nothing
	// about a key the owner marked mandatory.
	for _, name := range schema.Required {
		if !slices.ContainsFunc(*schema.Properties, func(p JSONSchemaProperty) bool { return p.Name == name }) {
			return nil, fmt.Errorf("%w: %s requires %q, which it does not declare as a property", ErrInvalid, at, name)
		}
	}
	fields := make([]*basev0.RunnableField, 0, len(*schema.Properties))
	for _, property := range *schema.Properties {
		if *budget--; *budget < 0 {
			return nil, fmt.Errorf("%w: %s projects more than %d fields; depth alone does not bound a payload whose members are themselves objects", ErrInvalid, at, MaxProjectionFields)
		}
		field, err := projectSchema(doc, property.Schema, at+"/properties/"+escapePointer(property.Name), open, depth+1, budget)
		if err != nil {
			return nil, err
		}
		field.Name = property.Name
		field.Optional = !slices.Contains(schema.Required, property.Name)
		fields = append(fields, field)
	}
	return fields, nil
}

// projectItems reads an array's element schema. A tuple names a different type
// per position, which is a fixed-length record rather than the homogeneous list
// the profile carries.
func projectItems(doc *OpenAPIDocument, schema *JSONSchema, at string, open []string, depth int, budget *int) (*basev0.RunnableField, error) {
	trimmed := bytes.TrimSpace(schema.Items)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: %s is an array declaring no items, and the bounded profile has no untyped element", ErrInvalid, at)
	}
	if trimmed[0] == '[' {
		return nil, fmt.Errorf("%w: %s is an array of positional items, which the bounded profile does not cover", ErrInvalid, at)
	}
	items := &JSONSchema{}
	if err := json.Unmarshal(trimmed, items); err != nil {
		return nil, fmt.Errorf("%w: %s items is not a schema: %v", ErrInvalid, at, err)
	}
	// An element is present or the list is shorter, so the item carries neither
	// a name nor optionality — the same reading the proto projection makes.
	return projectSchema(doc, items, at+"/items", open, depth, budget)
}

// escapePointer spells a property name as an RFC 6901 reference token, so a
// name containing a slash names one token rather than two.
func escapePointer(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}
