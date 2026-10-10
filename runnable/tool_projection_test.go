package runnable_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

func projectionFixture(t *testing.T, spelling string) *runnablev0.ToolProjection {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "tool-projection", spelling+".json"))
	require.NoError(t, err)
	projection, err := runnable.DecodeToolProjection(raw)
	require.NoError(t, err)
	return projection
}

func projectedBinding(t *testing.T) *runnablev0.PreparedBinding {
	t.Helper()
	binding := connectBinding()
	binding.Contract.Input.Fields = []*basev0.RunnableField{
		{Name: "operation_code", Type: basev0.RunnableField_STRING},
		{Name: "input_json", Type: basev0.RunnableField_STRING},
	}
	binding.Contract.Output.Fields = []*basev0.RunnableField{{Name: "output_json", Type: basev0.RunnableField_STRING}}
	delivered, err := runnable.DecodePrepared(encoded(t, binding))
	require.NoError(t, err)
	return delivered
}

// Like TestPolicyRoundTripsEveryField, the schema itself is the field list.
// Every owned message must set every field to a nonzero value, recursively;
// extending the schema without extending this fixture makes the test fail.
func assertProjectionFieldsPopulated(t *testing.T, message protoreflect.Message) {
	t.Helper()
	fields := message.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		require.Truef(t, message.Has(field), "%s is zero; the fixture must expose dropped fields", field.FullName())
		if field.Kind() != protoreflect.MessageKind || !strings.HasPrefix(string(field.Message().FullName()), "codefly.runnable.v0.") {
			continue
		}
		if field.IsList() {
			list := message.Get(field).List()
			for j := 0; j < list.Len(); j++ {
				assertProjectionFieldsPopulated(t, list.Get(j).Message())
			}
		} else {
			assertProjectionFieldsPopulated(t, message.Get(field).Message())
		}
	}
}

func TestToolProjectionRoundTripsEveryField(t *testing.T) {
	binding := projectedBinding(t)
	snake := projectionFixture(t, "snake_case")
	camel := projectionFixture(t, "lowerCamelCase")
	require.True(t, proto.Equal(snake, camel), "both spellings must carry the exact same declaration")
	for _, projection := range []*runnablev0.ToolProjection{snake, camel} {
		assertProjectionFieldsPopulated(t, projection.ProtoReflect())
		canonical, err := runnable.EncodeToolProjection(projection)
		require.NoError(t, err)
		delivered, err := runnable.DecodeToolProjection(canonical)
		require.NoError(t, err)
		require.True(t, proto.Equal(projection, delivered), "projection delivery dropped a field")
		again, err := runnable.EncodeToolProjection(delivered)
		require.NoError(t, err)
		require.Equal(t, canonical, again)
		tools, err := runnable.ToolsFromProjection(binding, delivered)
		require.NoError(t, err)
		require.Len(t, tools, 2)
		for i := range tools {
			require.True(t, proto.Equal(projection.Tools[i], tools[i]), "tool extraction dropped a field")
		}
		// The complete result is detached, including schemas, lists, selectors.
		tools[0].Name = "changed"
		tools[0].InputSchema.Fields["required"].GetListValue().Values[0] = structpb.NewStringValue("changed")
		tools[0].OutputSchema.Fields["type"] = structpb.NewStringValue("boolean")
		tools[0].Selector.Value = "changed"
		require.True(t, proto.Equal(projection, delivered))
	}
}

func TestToolProjectionDoesNotRequireDirectMethodExposure(t *testing.T) {
	binding := projectedBinding(t)
	require.Nil(t, binding.Policy.Tool)
	require.NoError(t, runnable.VerifyPrepared(binding))
	_, err := runnable.ToolFromPrepared(binding)
	require.ErrorIs(t, err, runnable.ErrNotATool)
	tools, err := runnable.ToolsFromProjection(binding, projectionFixture(t, "snake_case"))
	require.NoError(t, err)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, tools[0].Effect)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_MUTATION, tools[1].Effect)
	// A generic exposure does not classify or replace declared tool effects.
	binding.Policy.Tool = exposedTool()
	tools, err = runnable.ToolsFromProjection(binding, projectionFixture(t, "snake_case"))
	require.NoError(t, err)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, tools[0].Effect)
}

func TestToolProjectionRefusesDirectToolNameCollision(t *testing.T) {
	for _, name := range []string{"inspect_item", "update_item"} {
		t.Run(name, func(t *testing.T) {
			binding := projectedBinding(t)
			binding.Policy.Tool = exposedTool()
			binding.Policy.Tool.Name = name
			projection := projectionFixture(t, "snake_case")
			require.NoError(t, runnable.VerifyPrepared(binding))
			require.NoError(t, runnable.ValidateToolProjection(projection))
			tools, err := runnable.ToolsFromProjection(binding, projection)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, "collides with direct tool exposure")
			require.Nil(t, tools)
		})
	}
}

func TestToolProjectionPermitsAliasesWithDistinctNames(t *testing.T) {
	for name, change := range map[string]func(*runnablev0.ToolProjection){
		"shared selector": func(p *runnablev0.ToolProjection) {
			p.Tools[1].Selector = proto.CloneOf(p.Tools[0].Selector)
		},
		"shared declaration digest": func(p *runnablev0.ToolProjection) {
			p.Tools[1].Digest = p.Tools[0].Digest
		},
		"alias": func(p *runnablev0.ToolProjection) {
			p.Tools[1] = proto.CloneOf(p.Tools[0])
			p.Tools[1].Name = "inspect_item_alias"
		},
	} {
		t.Run(name, func(t *testing.T) {
			projection := projectionFixture(t, "snake_case")
			change(projection)
			tools, err := runnable.ToolsFromProjection(projectedBinding(t), projection)
			require.NoError(t, err)
			require.Len(t, tools, 2)
			for i := range tools {
				require.True(t, proto.Equal(projection.Tools[i], tools[i]))
			}
		})
	}
}

func TestToolProjectionRejectsMalformedDeclarations(t *testing.T) {
	tests := map[string]func(*runnablev0.ToolProjection){
		"no binding":            func(p *runnablev0.ToolProjection) { p.BindingId = "" },
		"binding whitespace":    func(p *runnablev0.ToolProjection) { p.BindingId = "binding two" },
		"binding control":       func(p *runnablev0.ToolProjection) { p.BindingId = "binding\x00two" },
		"long binding":          func(p *runnablev0.ToolProjection) { p.BindingId = strings.Repeat("b", 513) },
		"invalid UTF-8 binding": func(p *runnablev0.ToolProjection) { p.BindingId = "\xff" },
		"no contract digest":    func(p *runnablev0.ToolProjection) { p.ContractDigest = "" },
		"uppercase digest":      func(p *runnablev0.ToolProjection) { p.ContractDigest = "sha256:" + strings.Repeat("A", 64) },
		"no tools":              func(p *runnablev0.ToolProjection) { p.Tools = nil },
		"nil tool":              func(p *runnablev0.ToolProjection) { p.Tools[0] = nil },
		"too many tools": func(p *runnablev0.ToolProjection) {
			for len(p.Tools) <= runnable.MaxProjectedTools {
				p.Tools = append(p.Tools, proto.CloneOf(p.Tools[0]))
			}
		},
		"duplicate name":           func(p *runnablev0.ToolProjection) { p.Tools[1].Name = p.Tools[0].Name },
		"no name":                  func(p *runnablev0.ToolProjection) { p.Tools[0].Name = "" },
		"invalid name":             func(p *runnablev0.ToolProjection) { p.Tools[0].Name = "1bad" },
		"long name":                func(p *runnablev0.ToolProjection) { p.Tools[0].Name = strings.Repeat("a", 65) },
		"no description":           func(p *runnablev0.ToolProjection) { p.Tools[0].Description = "" },
		"untrimmed description":    func(p *runnablev0.ToolProjection) { p.Tools[0].Description = " trailing " },
		"no effect":                func(p *runnablev0.ToolProjection) { p.Tools[0].Effect = runnablev0.ToolExposure_EFFECT_UNSPECIFIED },
		"unknown effect":           func(p *runnablev0.ToolProjection) { p.Tools[0].Effect = 99 },
		"no declaration digest":    func(p *runnablev0.ToolProjection) { p.Tools[0].Digest = "" },
		"short declaration digest": func(p *runnablev0.ToolProjection) { p.Tools[0].Digest = "sha256:bad" },
		"no selector":              func(p *runnablev0.ToolProjection) { p.Tools[0].Selector = nil },
		"no selector field":        func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Field = "" },
		"selector path":            func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Field = "input.operation" },
		"selector numeric prefix":  func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Field = "1operation" },
		"long selector field":      func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Field = strings.Repeat("a", 65) },
		"no selector value":        func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Value = "" },
		"long selector value":      func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Value = strings.Repeat("a", 513) },
		"selector control":         func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Value = "read\nitem" },
		"selector whitespace":      func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Value = " read" },
		"missing input":            func(p *runnablev0.ToolProjection) { p.Tools[0].InputSchema = nil },
		"missing output":           func(p *runnablev0.ToolProjection) { p.Tools[0].OutputSchema = nil },
		"empty schema":             func(p *runnablev0.ToolProjection) { p.Tools[0].OutputSchema = &structpb.Struct{} },
		"non-object input": func(p *runnablev0.ToolProjection) {
			p.Tools[0].InputSchema.Fields["type"] = structpb.NewStringValue("string")
		},
		"malformed schema": func(p *runnablev0.ToolProjection) {
			p.Tools[0].OutputSchema.Fields["type"] = structpb.NewStringValue("not-a-type")
		},
		"external schema": func(p *runnablev0.ToolProjection) {
			p.Tools[0].InputSchema.Fields["$ref"] = structpb.NewStringValue("https://example.invalid/schema.json")
		},
		"filesystem schema": func(p *runnablev0.ToolProjection) {
			p.Tools[0].OutputSchema.Fields["$ref"] = structpb.NewStringValue("file:///schema.json")
		},
		"oversized schema": func(p *runnablev0.ToolProjection) {
			p.Tools[0].InputSchema.Fields["description"] = structpb.NewStringValue(strings.Repeat("a", runnable.MaxToolSchemaBytes))
		},
		"oversized projection": func(p *runnablev0.ToolProjection) {
			p.Tools[0].InputSchema.Fields["description"] = structpb.NewStringValue(strings.Repeat("a", runnable.MaxToolProjectionBytes))
		},
		"unknown binary field": func(p *runnablev0.ToolProjection) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1}) },
		"unknown nested field": func(p *runnablev0.ToolProjection) {
			p.Tools[0].Selector.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			p := projectionFixture(t, "snake_case")
			change(p)
			require.ErrorIs(t, runnable.ValidateToolProjection(p), runnable.ErrInvalid)
			_, err := runnable.EncodeToolProjection(p)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			_, err = runnable.ToolsFromProjection(projectedBinding(t), p)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}
	require.ErrorIs(t, runnable.ValidateToolProjection(nil), runnable.ErrInvalid)
}

func TestToolProjectionChecksTheEntirePreparedBinding(t *testing.T) {
	tests := map[string]func(*runnablev0.PreparedBinding, *runnablev0.ToolProjection){
		"different contract": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) { p.ContractDigest = digestA },
		"tampered contract": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			b.Contract.Input.Fields[0].Type = basev0.RunnableField_BOOLEAN
		},
		"changed route": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			b.Call.GetConnect().Procedure = "/acme.items.v1.Items/Cancel"
		},
		"invalid policy": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) { b.Policy.Audience = "" },
		"unresolved slots": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			b.Policy.RequiredScopeSlots = []*runnablev0.ScopeSlot{{Name: "model"}}
		},
		"invalid generic exposure": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			b.Policy.Tool = &runnablev0.ToolExposure{}
		},
		"absent selector field": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			p.Tools[1].Selector.Field = "missing"
		},
		"non-string selector field": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			b.Contract.Input.Fields[0].Type = basev0.RunnableField_BOOLEAN
			var err error
			b.ContractDigest, err = runnable.ContractDigest(b.Contract)
			require.NoError(t, err)
			p.ContractDigest = b.ContractDigest
		},
		"nested selector field": func(b *runnablev0.PreparedBinding, p *runnablev0.ToolProjection) {
			b.Contract.Input.Fields = []*basev0.RunnableField{{Name: "envelope", Type: basev0.RunnableField_OBJECT, Fields: b.Contract.Input.Fields}}
			var err error
			b.ContractDigest, err = runnable.ContractDigest(b.Contract)
			require.NoError(t, err)
			p.ContractDigest = b.ContractDigest
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			b, p := projectedBinding(t), projectionFixture(t, "snake_case")
			change(b, p)
			tools, err := runnable.ToolsFromProjection(b, p)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.Nil(t, tools)
		})
	}
	_, err := runnable.ToolsFromProjection(nil, projectionFixture(t, "snake_case"))
	require.ErrorIs(t, err, runnable.ErrInvalid)
	_, err = runnable.ToolsFromProjection(projectedBinding(t), nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func TestToolProjectionJSONIsStrict(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", "{", strings.Repeat(" ", runnable.MaxToolProjectionBytes+1)} {
		_, err := runnable.DecodeToolProjection([]byte(raw))
		require.ErrorIs(t, err, runnable.ErrInvalid)
	}
	p := projectionFixture(t, "snake_case")
	raw, err := runnable.EncodeToolProjection(p)
	require.NoError(t, err)
	for _, bad := range []string{
		strings.Replace(string(raw), `"binding_id":`, `"unknown":true,"binding_id":`, 1),
		strings.Replace(string(raw), `"selector":{`, `"selector":{"unknown":true,`, 1),
		strings.Replace(string(raw), `"binding_id":`, `"bindingId":"duplicate","binding_id":`, 1),
	} {
		_, err := runnable.DecodeToolProjection([]byte(bad))
		require.ErrorIs(t, err, runnable.ErrInvalid)
	}
	// Ordinary protobuf JSON spelling is a supported producer as well.
	camel, err := protojson.Marshal(p)
	require.NoError(t, err)
	again, err := runnable.DecodeToolProjection(camel)
	require.NoError(t, err)
	require.True(t, proto.Equal(p, again))
}

func TestToolProjectionAdmissionMustFreezeMoreThanDigests(t *testing.T) {
	binding := projectedBinding(t)
	admitted := projectionFixture(t, "snake_case")
	frozen, err := runnable.EncodeToolProjection(admitted)
	require.NoError(t, err)
	for name, change := range map[string]func(*runnablev0.ToolProjection){
		"binding reference": func(p *runnablev0.ToolProjection) { p.BindingId = "another__invoke" },
		"name":              func(p *runnablev0.ToolProjection) { p.Tools[0].Name = "inspect_other" },
		"description":       func(p *runnablev0.ToolProjection) { p.Tools[0].Description = "A changed description." },
		"effect":            func(p *runnablev0.ToolProjection) { p.Tools[0].Effect = runnablev0.ToolExposure_EFFECT_MUTATION },
		"input schema": func(p *runnablev0.ToolProjection) {
			p.Tools[0].InputSchema.Fields["additionalProperties"] = structpb.NewBoolValue(true)
		},
		"output schema": func(p *runnablev0.ToolProjection) {
			p.Tools[0].OutputSchema.Fields["additionalProperties"] = structpb.NewBoolValue(true)
		},
		"selector field":     func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Field = "input_json" },
		"selector value":     func(p *runnablev0.ToolProjection) { p.Tools[0].Selector.Value = "inspect.v2" },
		"declaration digest": func(p *runnablev0.ToolProjection) { p.Tools[0].Digest = digestC },
	} {
		t.Run(name, func(t *testing.T) {
			changed := proto.CloneOf(admitted)
			change(changed)
			_, err := runnable.ToolsFromProjection(binding, changed)
			require.NoError(t, err, "data validation does not authenticate an installation or its publisher")
			require.Equal(t, admitted.ContractDigest, changed.ContractDigest)
			raw, err := runnable.EncodeToolProjection(changed)
			require.NoError(t, err)
			require.NotEqual(t, frozen, raw, "admission must compare every field, not just digests")
		})
	}
	// A second binding can have identical schemas and a valid different policy.
	// The projection does not certify it as the binding admitted by the caller.
	other := proto.CloneOf(binding)
	other.Policy.Audience = "acme.other"
	_, err = runnable.ToolsFromProjection(other, admitted)
	require.NoError(t, err)
	require.Equal(t, binding.ContractDigest, other.ContractDigest)
	require.False(t, proto.Equal(binding, other))
}

func TestToolProjectionCannotLoadAnExistingSchemaFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"type":"string"}`), 0600))
	projection := projectionFixture(t, "snake_case")
	projection.Tools[0].OutputSchema.Fields["$ref"] = structpb.NewStringValue("file://" + file)
	err := runnable.ValidateToolProjection(projection)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	var loadError *jsonschema.LoadURLError
	require.ErrorAs(t, err, &loadError)
	require.Contains(t, loadError.Error(), "no URLLoader registered")
}
