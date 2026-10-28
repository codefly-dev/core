package runnable_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"testing"
)

func compiledProfile(t *testing.T, fields ...*basev0.RunnableField) *jsonschema.Schema {
	t.Helper()
	raw, err := runnable.SchemaJSON(&basev0.RunnableSchema{Fields: fields})
	require.NoError(t, err)
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	require.NoError(t, runnable.ConfigureJSONSchemaCompiler(c))
	require.NoError(t, c.AddResource("urn:profile", value))
	schema, err := c.Compile("urn:profile")
	require.NoError(t, err)
	return schema
}
func profileAccepts(t *testing.T, schema *jsonschema.Schema, document string, accepted bool) {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(document))
	require.NoError(t, err)
	err = schema.Validate(value)
	if accepted {
		require.NoError(t, err, document)
	} else {
		require.Error(t, err, document)
	}
}
func TestCurrentProfileHasExactNamesPresenceAndTypes(t *testing.T) {
	schema := compiledProfile(t,
		&basev0.RunnableField{Name: "recordId", Type: basev0.RunnableField_STRING},
		&basev0.RunnableField{Name: "count", Type: basev0.RunnableField_INTEGER, Optional: true},
		&basev0.RunnableField{Name: "record", Type: basev0.RunnableField_OBJECT, Optional: true},
		&basev0.RunnableField{Name: "choice", Type: basev0.RunnableField_STRING, Values: []string{"ONE", "TWO"}, Nullable: true, Optional: true},
	)
	for _, raw := range []string{`{"recordId":"x"}`, `{"recordId":"x","count":9223372036854775807,"choice":null}`, `{"recordId":"x","record":{},"choice":"ONE"}`} {
		profileAccepts(t, schema, raw, true)
	}
	for _, raw := range []string{`{}`, `{"record_id":"x"}`, `{"recordId":"x","count":"1"}`, `{"recordId":"x","count":9223372036854775808}`, `{"recordId":"x","record":null}`, `{"recordId":"x","record":{"extra":1}}`, `{"recordId":"x","choice":"THREE"}`} {
		profileAccepts(t, schema, raw, false)
	}
}
func TestCurrentProfileJSONNumberAndArrayValues(t *testing.T) {
	for _, test := range []struct {
		field     *basev0.RunnableField
		good, bad []string
	}{
		{&basev0.RunnableField{Type: basev0.RunnableField_NUMBER}, []string{`1.5`, `"NaN"`, `"Infinity"`, `"-Infinity"`}, []string{`"1.5"`, `null`, `true`}},
		{&basev0.RunnableField{Type: basev0.RunnableField_JSON}, []string{`{}`, `[]`, `"text"`, `42`, `false`}, []string{`null`}},
		{&basev0.RunnableField{Type: basev0.RunnableField_JSON, Nullable: true}, []string{`null`, `{"nested":[null,1]}`}, nil},
		{&basev0.RunnableField{Type: basev0.RunnableField_JSON, Format: "object"}, []string{`{"free":[null,1]}`}, []string{`[]`, `null`}},
		{&basev0.RunnableField{Type: basev0.RunnableField_JSON, Format: "array"}, []string{`[null,{}]`}, []string{`{}`, `null`}},
		{&basev0.RunnableField{Type: basev0.RunnableField_ARRAY, Items: &basev0.RunnableField{Type: basev0.RunnableField_BOOLEAN}}, []string{`[]`, `[true,false]`}, []string{`[null]`, `[1]`}},
	} {
		t.Run(fmt.Sprint(test.field), func(t *testing.T) {
			test.field.Name = "value"
			schema := compiledProfile(t, test.field)
			for _, v := range test.good {
				profileAccepts(t, schema, `{"value":`+v+`}`, true)
			}
			for _, v := range test.bad {
				profileAccepts(t, schema, `{"value":`+v+`}`, false)
			}
		})
	}
}
func TestCurrentProfileAssertsCanonicalFormats(t *testing.T) {
	for _, test := range []struct {
		format    string
		good, bad []string
	}{
		{"int64", []string{"0", "-9223372036854775808", "9223372036854775807"}, []string{"9223372036854775808", "-0", "01", "+1", "1e2"}},
		{"uint64", []string{"0", "18446744073709551615"}, []string{"-1", "18446744073709551616", "01"}},
		{"byte", []string{"", "AQI=", "/w=="}, []string{"AQI", "_w==", "not base64"}},
		{"date-time", []string{"1970-01-01T00:00:00Z", "2026-10-28T00:00:00.123Z"}, []string{"2026-02-30T00:00:00Z", "2026-10-28", "1970-01-01T01:00:00+01:00"}},
		{"protobuf-duration", []string{"0s", "-1.500s", "315576000000s"}, []string{"PT1S", "1.5s", "315576000001s"}},
		{"protobuf-field-mask", []string{"", "recordId,nested.displayName"}, []string{"record_id"}},
	} {
		t.Run(test.format, func(t *testing.T) {
			schema := compiledProfile(t, &basev0.RunnableField{Name: "value", Type: basev0.RunnableField_STRING, Format: test.format})
			for _, text := range test.good {
				raw, err := json.Marshal(map[string]string{"value": text})
				require.NoError(t, err)
				profileAccepts(t, schema, string(raw), true)
			}
			for _, text := range test.bad {
				raw, err := json.Marshal(map[string]string{"value": text})
				require.NoError(t, err)
				profileAccepts(t, schema, string(raw), false)
			}
		})
	}
}
func TestCurrentProfileDescribesActualOmittedProtoJSON(t *testing.T) {
	child := message("Record", named("created_at", 1, tMessage, ".google.protobuf.Timestamp"))
	response := message("Response", named("record", 1, tMessage, ".wire.Record"), scalar("source_ordinal", 2, tInt64))
	file := compile(t, fileSpec{name: "profile-wire.proto", pkg: "wire", imports: []string{"google/protobuf/timestamp.proto"}, messages: []*descriptorpb.DescriptorProto{child, response}})
	md := file.Messages().ByName("Response")
	projection, err := runnable.ProjectMessage(md)
	require.NoError(t, err)
	schema := compiledProfile(t, projection.GetFields()...)
	value := dynamicpb.NewMessage(md)
	omitted, err := protojson.Marshal(value)
	require.NoError(t, err)
	profileAccepts(t, schema, string(omitted), true)
	old, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(value)
	require.NoError(t, err)
	profileAccepts(t, schema, string(old), false)
	value.Mutable(md.Fields().ByName("record"))
	value.Set(md.Fields().ByName("source_ordinal"), protoreflect.ValueOfInt64(9223372036854775807))
	populated, err := protojson.Marshal(value)
	require.NoError(t, err)
	profileAccepts(t, schema, string(populated), true)
	old, err = (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(value)
	require.NoError(t, err)
	profileAccepts(t, schema, string(old), false)
}
func TestCurrentProfileRejectsMalformedAndUnboundedSchemas(t *testing.T) {
	_, err := runnable.SchemaJSON(nil)
	require.Error(t, err)
	require.Error(t, runnable.ConfigureJSONSchemaCompiler(nil))
	cycle := &basev0.RunnableField{Name: "cycle", Type: basev0.RunnableField_OBJECT}
	cycle.Fields = []*basev0.RunnableField{cycle}
	unknown := &basev0.RunnableField{Name: "future", Type: basev0.RunnableField_STRING}
	unknown.ProtoReflect().SetUnknown([]byte{0x50, 0x01})
	for _, fields := range [][]*basev0.RunnableField{
		{nil}, {cycle}, {unknown},
		{{Name: "x", Type: basev0.RunnableField_UNKNOWN}},
		{{Name: "x", Type: basev0.RunnableField_STRING, Format: "made-up"}},
		{{Name: "x", Type: basev0.RunnableField_ARRAY}},
		{{Name: "x", Type: basev0.RunnableField_STRING}, {Name: "x", Type: basev0.RunnableField_BOOLEAN}},
	} {
		_, err := runnable.SchemaJSON(&basev0.RunnableSchema{Fields: fields})
		require.Error(t, err)
	}
	wide := &basev0.RunnableSchema{}
	for i := 0; i <= runnable.MaxProjectionFields; i++ {
		wide.Fields = append(wide.Fields, &basev0.RunnableField{Name: fmt.Sprintf("field%d", i), Type: basev0.RunnableField_STRING})
	}
	_, err = runnable.SchemaJSON(wide)
	require.ErrorContains(t, err, "field budget")
}

func TestProfileRegistrationLeavesOrdinarySchemaDialectAndFormatsAlone(t *testing.T) {
	c := jsonschema.NewCompiler()
	require.NoError(t, runnable.ConfigureJSONSchemaCompiler(c))
	for _, test := range []struct {
		name, raw string
		accepted  []string
		rejected  []string
	}{
		{"draft7", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"string","format":"date-time"}`, []string{`"2026-10-28T01:00:00+01:00"`, `"2026-10-28T00:00:00.1Z"`}, []string{`"invalid"`}},
		{"draft2020", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string","format":"date-time"}`, []string{`"2026-10-28T01:00:00+01:00"`, `"2026-10-28T00:00:00.1Z"`, `"invalid"`}, nil},
		{"default", `{"type":"string","format":"date-time"}`, []string{`"invalid"`}, nil},
	} {
		value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(test.raw))
		require.NoError(t, err)
		url := "urn:ordinary:" + test.name
		require.NoError(t, c.AddResource(url, value))
		schema, err := c.Compile(url)
		require.NoError(t, err)
		for _, raw := range test.accepted {
			profileAccepts(t, schema, raw, true)
		}
		for _, raw := range test.rejected {
			profileAccepts(t, schema, raw, false)
		}
	}
	strict := compiledProfile(t, &basev0.RunnableField{Name: "value", Type: basev0.RunnableField_STRING, Format: runnable.FormatDateTime})
	for _, raw := range []string{`{"value":"2026-10-28T01:00:00+01:00"}`, `{"value":"2026-10-28T00:00:00.1Z"}`} {
		profileAccepts(t, strict, raw, false)
	}
}
