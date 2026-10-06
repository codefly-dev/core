package runnable_test

import (
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func exposedTool() *runnablev0.ToolExposure {
	return &runnablev0.ToolExposure{Name: "apply_item", Description: "Apply an item change.", Effect: runnablev0.ToolExposure_EFFECT_MUTATION}
}

func TestToolExposureHasNoImplicitEffectOrExposure(t *testing.T) {
	hidden, err := runnable.DecodePrepared(encoded(t, connectBinding()))
	require.NoError(t, err)
	_, err = runnable.ToolFromPrepared(hidden)
	require.ErrorIs(t, err, runnable.ErrNotATool)
	tests := map[string]func(*runnablev0.ToolExposure){
		"empty":                func(v *runnablev0.ToolExposure) { *v = runnablev0.ToolExposure{} },
		"omitted effect":       func(v *runnablev0.ToolExposure) { v.Effect = runnablev0.ToolExposure_EFFECT_UNSPECIFIED },
		"unknown effect":       func(v *runnablev0.ToolExposure) { v.Effect = 99 },
		"missing name":         func(v *runnablev0.ToolExposure) { v.Name = "" },
		"overlong name":        func(v *runnablev0.ToolExposure) { v.Name = strings.Repeat("a", 65) },
		"unicode name":         func(v *runnablev0.ToolExposure) { v.Name = "résumé" },
		"url name":             func(v *runnablev0.ToolExposure) { v.Name = "https://example.com" },
		"numeric prefix":       func(v *runnablev0.ToolExposure) { v.Name = "1tool" },
		"missing description":  func(v *runnablev0.ToolExposure) { v.Description = "" },
		"overlong description": func(v *runnablev0.ToolExposure) { v.Description = strings.Repeat("a", 4097) },
		"nul description":      func(v *runnablev0.ToolExposure) { v.Description = "bad\x00text" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			for _, b := range []*runnablev0.PreparedBinding{connectBinding(), restBinding()} {
				b.Policy.Tool = exposedTool()
				change(b.Policy.Tool)
				_, err := runnable.EncodePrepared(b)
				require.ErrorIs(t, err, runnable.ErrInvalid)
			}
		})
	}
}

func TestToolExposureSurvivesBothDeclarationFormsAndPreparedDelivery(t *testing.T) {
	declared := declaredOperation()
	declared.Tool = exposedTool()
	_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.True(t, proto.Equal(declared.Tool, spec.Tool))
	// The OpenAPI marker is the same protobuf schema with transport-specific codes.
	declared.LookupMethod = ""
	declared.RetryableCodes = []string{"503"}
	marker, err := protojson.Marshal(declared)
	require.NoError(t, err)
	restSpec, err := runnable.OperationFromOpenAPIMarker(marker, restSpelling)
	require.NoError(t, err)
	require.True(t, proto.Equal(declared.Tool, restSpec.Tool))
	for _, b := range []*runnablev0.PreparedBinding{connectBinding(), restBinding()} {
		b.Policy.Tool = exposedTool()
		delivered, err := runnable.DecodePrepared(encoded(t, b))
		require.NoError(t, err)
		tool, err := runnable.ToolFromPrepared(delivered)
		require.NoError(t, err)
		require.Equal(t, runnablev0.ToolExposure_EFFECT_MUTATION, tool.Effect)
		require.True(t, proto.Equal(exposedTool(), tool))
		tool.Name = "changed"
		require.Equal(t, "apply_item", delivered.Policy.Tool.Name)
		// A schema change with the original digest is refused even if tool metadata
		// is valid. Projection cannot bypass prepared-contract validation.
		delivered.Contract.Input.Fields[0].Type = basev0.RunnableField_BOOLEAN
		_, err = runnable.ToolFromPrepared(delivered)
		require.ErrorIs(t, err, runnable.ErrInvalid)
	}
}

func TestReadOnlyRequiresExplicitOwnerDeclaration(t *testing.T) {
	b := connectBinding()
	b.Policy.Tool = exposedTool()
	// Scope and method spellings are unchanged; only the explicit effect decides.
	b.Policy.Tool.Effect = runnablev0.ToolExposure_EFFECT_READ_ONLY
	delivered, err := runnable.DecodePrepared(encoded(t, b))
	require.NoError(t, err)
	tool, err := runnable.ToolFromPrepared(delivered)
	require.NoError(t, err)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, tool.Effect)
	raw, err := protojson.Marshal(tool)
	require.NoError(t, err)
	require.NotContains(t, string(raw), b.Call.Address)
	require.NotContains(t, string(raw), b.Policy.Audience)
}
