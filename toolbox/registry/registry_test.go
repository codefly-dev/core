package registry_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
)

func TestToolDefinition_ProjectsToAllThreeShapes(t *testing.T) {
	schema, _ := structpb.NewStruct(map[string]any{"type": "object"})
	exampleArgs, _ := structpb.NewStruct(map[string]any{"key": "value"})

	d := &registry.ToolDefinition{
		Name:               "test.example",
		SummaryDescription: "one-liner for routing",
		LongDescription:    "multi-paragraph explanation\nfor the spec view",
		InputSchema:        schema,
		Destructive:        true,
		Tags:               []string{"test", "destructive"},
		Idempotency:        "side_effecting",
		ErrorModes:         "fails when X",
		Examples: []*toolboxv0.ToolExample{
			{Description: "show usage", Arguments: exampleArgs, ExpectedOutcome: "returns success"},
		},
	}

	// --- ToTool: heavy legacy envelope ---
	tool := d.ToTool()
	require.Equal(t, "test.example", tool.Name)
	require.Equal(t, "multi-paragraph explanation\nfor the spec view", tool.Description,
		"legacy Tool gets the LONG description (it's the everything-in-one envelope)")
	require.True(t, tool.Destructive)

	// --- ToSummary: lightweight catalog ---
	summary := d.ToSummary()
	require.Equal(t, "test.example", summary.Name)
	require.Equal(t, "one-liner for routing", summary.Description,
		"ToolSummary uses the SHORT description")
	require.ElementsMatch(t, []string{"test", "destructive"}, summary.Tags)
	require.True(t, summary.Destructive)

	// --- ToSpec: full per-tool fetch ---
	spec := d.ToSpec()
	require.Equal(t, "multi-paragraph explanation\nfor the spec view", spec.Description)
	require.Equal(t, "side_effecting", spec.Idempotency)
	require.Equal(t, "fails when X", spec.ErrorModes)
	require.Len(t, spec.Examples, 1)
	require.Equal(t, "show usage", spec.Examples[0].Description)
}

func TestToolDefinition_LongDescriptionFallsBackToSummary(t *testing.T) {
	d := &registry.ToolDefinition{
		Name:               "no.long",
		SummaryDescription: "short only",
		LongDescription:    "",
	}
	require.Equal(t, "short only", d.ToTool().Description,
		"empty LongDescription must fall back to SummaryDescription")
	require.Equal(t, "short only", d.ToSpec().Description)
	require.Equal(t, "short only", d.ToSummary().Description)
}

func TestAsSummaries_TagsFilter_ANDSemantics(t *testing.T) {
	defs := []*registry.ToolDefinition{
		{Name: "a", SummaryDescription: "", Tags: []string{"git", "read-only"}},
		{Name: "b", SummaryDescription: "", Tags: []string{"git", "destructive"}},
		{Name: "c", SummaryDescription: "", Tags: []string{"docker", "read-only"}},
	}

	got := registry.AsSummaries(defs, nil)
	require.Len(t, got, 3, "no filter returns every tool")

	got = registry.AsSummaries(defs, []string{"read-only"})
	require.Len(t, got, 2, "tools with read-only tag: a, c")

	got = registry.AsSummaries(defs, []string{"git", "read-only"})
	require.Len(t, got, 1, "AND semantics — only `a` has BOTH git AND read-only")
	require.Equal(t, "a", got[0].Name)

	got = registry.AsSummaries(defs, []string{"nonexistent"})
	require.Empty(t, got, "filter requiring missing tag returns empty list")
}

func TestFindSpec_ReturnsNilOnUnknownName(t *testing.T) {
	defs := []*registry.ToolDefinition{
		{Name: "exists", SummaryDescription: "x"},
	}
	require.NotNil(t, registry.FindSpec(defs, "exists"))
	require.Nil(t, registry.FindSpec(defs, "missing"),
		"FindSpec returns nil for unknown name; caller decides how to surface the error")
}
