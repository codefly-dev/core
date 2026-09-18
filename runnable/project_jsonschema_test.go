package runnable_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

// document wraps one or more component schemas in the smallest document that
// carries them, so a case states only the shape under test.
func document(schemas ...string) string {
	return `{"openapi":"3.0.3","paths":{},"components":{"schemas":{` + strings.Join(schemas, ",") + `}}}`
}

// object spells the one object shape the profile accepts: closed, with its
// members declared and every one of them required.
func object(properties ...string) string {
	names := make([]string, 0, len(properties))
	for _, property := range properties {
		names = append(names, `"`+strings.SplitN(strings.TrimPrefix(property, `"`), `"`, 2)[0]+`"`)
	}
	return fmt.Sprintf(`{"type":"object","additionalProperties":false,"properties":{%s},"required":[%s]}`,
		strings.Join(properties, ","), strings.Join(names, ","))
}

// member is a root carrying the one shape under test, reached at
// #/components/schemas/Root/properties/value.
func member(schema string) string {
	return `"Root":` + object(`"value":`+schema)
}

func projectRoot(t *testing.T, schemas ...string) (*basev0.RunnableSchema, error) {
	t.Helper()
	doc, err := runnable.ParseOpenAPIDocument([]byte(document(schemas...)))
	require.NoError(t, err)
	// Entering through a $ref is how the package builder reaches a payload, so
	// the pointers a rejection names are the ones it would report.
	return runnable.ProjectJSONSchema(doc, &runnable.JSONSchema{Ref: "#/components/schemas/Root"})
}

func componentOf(t *testing.T, path, name string) (*runnable.OpenAPIDocument, *runnable.JSONSchema) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	doc, err := runnable.ParseOpenAPIDocument(raw)
	require.NoError(t, err)
	schema := doc.Components.Schemas[name]
	require.NotNil(t, schema, "document defines no component %s", name)
	return doc, schema
}

func TestProjectJSONSchemaCoversTheBoundedProfile(t *testing.T) {
	schema, err := projectRoot(t,
		`"Root":{"type":"object","additionalProperties":false,"properties":{
			"text":{"type":"string"},
			"tagged":{"type":"string","format":"uuid"},
			"flag":{"type":"boolean"},
			"plain":{"type":"integer"},
			"i32":{"type":"integer","format":"int32"},
			"i64":{"type":"integer","format":"int64"},
			"nested":{"$ref":"#/components/schemas/Inner"},
			"tags":{"type":"array","items":{"type":"string"}},
			"many":{"type":"array","items":{"$ref":"#/components/schemas/Inner"}},
			"note":{"type":"string"}
		},"required":["text","tagged","flag","plain","i32","i64","tags","many"]}`,
		`"Inner":`+object(`"label":{"type":"string"}`))
	require.NoError(t, err)

	inner := []*basev0.RunnableField{{Name: "label", Type: basev0.RunnableField_STRING}}
	require.True(t, proto.Equal(&basev0.RunnableSchema{Fields: []*basev0.RunnableField{
		{Name: "text", Type: basev0.RunnableField_STRING},
		{Name: "tagged", Type: basev0.RunnableField_STRING},
		{Name: "flag", Type: basev0.RunnableField_BOOLEAN},
		{Name: "plain", Type: basev0.RunnableField_INTEGER},
		{Name: "i32", Type: basev0.RunnableField_INTEGER},
		{Name: "i64", Type: basev0.RunnableField_INTEGER},
		// A name outside "required" is a key that may be absent.
		{Name: "nested", Type: basev0.RunnableField_OBJECT, Optional: true, Fields: inner},
		// An array's items carry neither the field's name nor its optionality.
		{Name: "tags", Type: basev0.RunnableField_ARRAY, Items: &basev0.RunnableField{Type: basev0.RunnableField_STRING}},
		{Name: "many", Type: basev0.RunnableField_ARRAY, Items: &basev0.RunnableField{Type: basev0.RunnableField_OBJECT, Fields: inner}},
		{Name: "note", Type: basev0.RunnableField_STRING, Optional: true},
	}}, schema), "projected %v", schema)

	contract, err := resources.RunnableContractFromProto(&basev0.RunnableContract{
		Protocol: resources.RunnableServiceProtocolV1, Input: schema, Output: schema})
	require.NoError(t, err)
	require.NoError(t, contract.Validate())
}

// TestProjectJSONSchemaKeepsTheDocumentsOwnOrder guards the one reading a map
// would lose: the profile's fields are an ordered list, and a projection that
// sorted them would give the derived package a digest of a shape the owner
// never wrote.
func TestProjectJSONSchemaKeepsTheDocumentsOwnOrder(t *testing.T) {
	for range 8 {
		schema, err := projectRoot(t, `"Root":`+object(
			`"zulu":{"type":"string"}`, `"alpha":{"type":"string"}`, `"mike":{"type":"string"}`))
		require.NoError(t, err)
		names := make([]string, 0, len(schema.GetFields()))
		for _, field := range schema.GetFields() {
			names = append(names, field.GetName())
		}
		require.Equal(t, []string{"zulu", "alpha", "mike"}, names)
	}
}

func TestProjectJSONSchemaReadsBothSpellingsOfNullable(t *testing.T) {
	for _, spelling := range []struct {
		name   string
		schema string
	}{
		{"openapi 3.0 nullable", `{"type":"string","nullable":true}`},
		{"json schema null member", `{"type":["string","null"]}`},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			schema, err := projectRoot(t, member(spelling.schema))
			require.NoError(t, err)
			require.True(t, proto.Equal(&basev0.RunnableSchema{Fields: []*basev0.RunnableField{
				{Name: "value", Type: basev0.RunnableField_STRING, Nullable: true},
			}}, schema), "projected %v", schema)
		})
	}
}

func TestProjectJSONSchemaRejectsEveryShapeOutsideTheProfile(t *testing.T) {
	const value = "#/components/schemas/Root/properties/value"
	for _, test := range []struct {
		name    string
		schemas []string
		at      string
		because string
	}{
		{"number", []string{member(`{"type":"number"}`)}, value, `type "number"`},
		{"enum", []string{member(`{"type":"string","enum":["a","b"]}`)}, value, "declares enum"},
		{"oneOf", []string{member(`{"oneOf":[{"type":"string"},{"type":"integer"}]}`)}, value, "declares oneOf"},
		{"anyOf", []string{member(`{"anyOf":[{"type":"string"}]}`)}, value, "declares anyOf"},
		{"allOf", []string{member(`{"allOf":[{"$ref":"#/components/schemas/Root"}]}`)}, value, "declares allOf"},
		{"union", []string{member(`{"type":["string","integer"]}`)}, value, "no union"},
		{"untyped", []string{member(`{}`)}, value, "declares no type"},
		{"only null", []string{member(`{"type":["null"]}`)}, value, "declares no type"},
		{"format byte", []string{member(`{"type":"string","format":"byte"}`)}, value, `format "byte"`},
		{"format binary", []string{member(`{"type":"string","format":"binary"}`)}, value, `format "binary"`},
		{"format date-time", []string{member(`{"type":"string","format":"date-time"}`)}, value, `format "date-time"`},
		{"format date", []string{member(`{"type":"string","format":"date"}`)}, value, `format "date"`},
		{"integer width", []string{member(`{"type":"integer","format":"uint64"}`)}, value, `format "uint64"`},
		{"open object", []string{member(`{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":true}`)}, value, "additionalProperties"},
		{"map", []string{member(`{"type":"object","properties":{},"additionalProperties":{"type":"string"}}`)}, value, "additionalProperties"},
		{"silent object", []string{member(`{"type":"object","properties":{"a":{"type":"string"}}}`)}, value, "additionalProperties"},
		{"free-form object", []string{member(`{"type":"object","additionalProperties":false}`)}, value, "declaring no properties"},
		{"tuple items", []string{member(`{"type":"array","items":[{"type":"string"},{"type":"integer"}]}`)}, value, "positional items"},
		{"untyped array", []string{member(`{"type":"array","additionalProperties":false}`)}, value, "declaring no items"},
		{"$ref beside keywords", []string{member(`{"$ref":"#/components/schemas/Inner","nullable":true}`), `"Inner":` + object(`"label":{"type":"string"}`)}, value, "beside other keywords"},
		{"dangling $ref", []string{member(`{"$ref":"#/components/schemas/Missing"}`)}, value, "does not define"},
		// A null component is a key the map reports as present carrying no
		// schema; reading it as defined would descend into nothing.
		{"null component", []string{member(`{"$ref":"#/components/schemas/Inner"}`), `"Inner":null`}, value, "does not define"},
		{"external $ref", []string{member(`{"$ref":"other.json#/Thing"}`)}, value, "does not define"},
		// A rejection deeper in is named by the whole pointer, not by the leaf:
		// the same component may be reached from several members.
		{"inside a component", []string{member(`{"$ref":"#/components/schemas/Inner"}`), `"Inner":` + object(`"weight":{"type":"number"}`)},
			"#/components/schemas/Inner/properties/weight", `type "number"`},
		// A property name containing a slash is one reference token, not two.
		{"escaped pointer", []string{`"Root":` + object(`"a/b":{"type":"number"}`)}, "#/components/schemas/Root/properties/a~1b", `type "number"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := projectRoot(t, test.schemas...)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.at)
			require.ErrorContains(t, err, test.because)
		})
	}
}

func TestProjectJSONSchemaRejectsAPayloadThatIsNotAnObject(t *testing.T) {
	for _, test := range []struct {
		name    string
		root    string
		because string
	}{
		{"scalar", `{"type":"string"}`, "is not an object"},
		{"array", `{"type":"array","items":{"type":"string"}}`, "is not an object"},
		{"nullable object", `{"type":["object","null"],"additionalProperties":false,"properties":{}}`, "is nullable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := projectRoot(t, `"Root":`+test.root)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
		})
	}
}

func TestProjectJSONSchemaRejectsRecursionAndExcessiveDepth(t *testing.T) {
	_, err := projectRoot(t, member(`{"$ref":"#/components/schemas/Root"}`))
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "reaches #/components/schemas/Root again")

	// A cycle through a second component is the same unbounded payload.
	_, err = projectRoot(t,
		member(`{"$ref":"#/components/schemas/Other"}`),
		`"Other":`+object(`"back":{"$ref":"#/components/schemas/Root"}`))
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "reaches #/components/schemas/Root again")
	require.ErrorContains(t, err, "#/components/schemas/Other/properties/back")

	// The same component reached from two members is not a cycle.
	_, err = projectRoot(t,
		`"Root":`+object(`"left":{"$ref":"#/components/schemas/Inner"}`, `"right":{"$ref":"#/components/schemas/Inner"}`),
		`"Inner":`+object(`"label":{"type":"string"}`))
	require.NoError(t, err)

	nested := func(depth int) string {
		schema := object(`"leaf":{"type":"string"}`)
		for range depth - 1 {
			schema = object(`"next":` + schema)
		}
		return `"Root":` + schema
	}
	_, err = projectRoot(t, nested(runnable.MaxProjectionDepth))
	require.NoError(t, err)
	_, err = projectRoot(t, nested(runnable.MaxProjectionDepth+1))
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, fmt.Sprintf("nests more than %d objects deep", runnable.MaxProjectionDepth))
}

func TestProjectJSONSchemaRequiresASchema(t *testing.T) {
	_, err := runnable.ProjectJSONSchema(nil, nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	// A schema carrying a $ref with no document to resolve it against is a
	// dangling reference, not an empty contract.
	_, err = runnable.ProjectJSONSchema(nil, &runnable.JSONSchema{Ref: "#/components/schemas/Root"})
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "does not define")
}

// TestTheTwoReadersProjectOneProfile is the gate that keeps the proto table and
// the JSON Schema table from drifting: a method's messages and the OpenAPI
// document transcoded from them describe the same payload, so they have to
// project to the same RunnableSchema.
//
// The transcription is not free to spell things as it likes, and this is what
// pins it. A proto3 field with implicit presence has a key that is always
// there, so it is `required` in the document; a message-typed or `optional`
// field has one that may be absent, so it is not. An `int64` is an `integer` of
// format `int64` and never the string proto3 JSON would carry it as — the
// bounded profile has a 64-bit integer of its own and does not borrow that
// encoding.
func TestTheTwoReadersProjectOneProfile(t *testing.T) {
	item := message("Item", scalar("label", 1, tString), scalar("weight", 2, tInt64))
	payload := message("Payload",
		scalar("name", 1, tString),
		scalar("count", 2, tInt32),
		scalar("active", 3, tBool),
		named("item", 4, tMessage, ".shapes.Item"),
		repeated(scalar("tags", 5, tString)),
		repeated(named("items", 6, tMessage, ".shapes.Item")),
		presence(scalar("note", 7, tString), 0),
	)
	payload.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_note")}}

	for _, pair := range []struct {
		name      string
		spec      fileSpec
		message   string
		document  string
		component string
	}{
		{"shapes", fileSpec{name: "shapes.proto", pkg: "shapes",
			messages: []*descriptorpb.DescriptorProto{item, payload}},
			"Payload", "testdata/openapi/shapes.json", "Payload"},
		{"ingestion request", ingestion(declaredOperation()),
			"ApplyTextRequest", "testdata/openapi/ingestion.json", "ApplyTextRequest"},
		{"ingestion response", ingestion(declaredOperation()),
			"ApplyTextResponse", "testdata/openapi/ingestion.json", "ApplyTextResponse"},
	} {
		t.Run(pair.name, func(t *testing.T) {
			md := compile(t, pair.spec).Messages().ByName(protoreflect.Name(pair.message))
			require.NotNil(t, md)
			fromProto, err := runnable.ProjectMessage(md)
			require.NoError(t, err)

			doc, schema := componentOf(t, pair.document, pair.component)
			fromOpenAPI, err := runnable.ProjectJSONSchema(doc, schema)
			require.NoError(t, err)

			require.True(t, proto.Equal(fromProto, fromOpenAPI),
				"the two readers disagree:\nproto   %v\nopenapi %v", fromProto, fromOpenAPI)
		})
	}
}

// TestParseOpenAPIDocumentRejectsAMalformedSchema covers the keywords whose
// shape the reader decides on: a schema the document spells as something other
// than a schema is a read error, never a shape quietly taken as absent.
func TestParseOpenAPIDocumentRejectsAMalformedSchema(t *testing.T) {
	for _, test := range []struct {
		name string
		root string
	}{
		{"properties is not an object", `{"type":"object","properties":[]}`},
		{"a property is not a schema", `{"type":"object","properties":{"value":3}}`},
		{"type is not a name", `{"type":3}`},
		{"type list is not names", `{"type":[3]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runnable.ParseOpenAPIDocument([]byte(document(`"Root":` + test.root)))
			require.Error(t, err)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}

	// items is read as written, so what it is not a schema is decided where the
	// element type is, not where the document is parsed.
	_, err := projectRoot(t, member(`{"type":"array","items":"a string"}`))
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "items is not a schema")
}
