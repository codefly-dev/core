package registry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
)

// fakeToolbox is a minimal Tooler used to drive Base's behavior.
type fakeToolbox struct {
	defs    []*registry.ToolDefinition
	calls   []string // captured tool invocations for assertion
	*registry.Base
}

func (f *fakeToolbox) Tools() []*registry.ToolDefinition { return f.defs }

func newFake(defs []*registry.ToolDefinition) *fakeToolbox {
	f := &fakeToolbox{defs: defs}
	f.Base = registry.NewBase(f)
	return f
}

func TestBase_ListTools_ProjectsAllDefinitions(t *testing.T) {
	f := newFake([]*registry.ToolDefinition{
		{Name: "a.one", SummaryDescription: "first"},
		{Name: "a.two", SummaryDescription: "second"},
	})

	resp, err := f.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Tools, 2)
	require.Equal(t, "a.one", resp.Tools[0].Name)
}

func TestBase_ListToolSummaries_AppliesTagsFilter(t *testing.T) {
	f := newFake([]*registry.ToolDefinition{
		{Name: "ro", SummaryDescription: "ro", Tags: []string{"read-only"}},
		{Name: "rw", SummaryDescription: "rw", Tags: []string{"destructive"}},
	})

	resp, err := f.ListToolSummaries(context.Background(),
		&toolboxv0.ListToolSummariesRequest{TagsFilter: []string{"read-only"}})
	require.NoError(t, err)
	require.Len(t, resp.Tools, 1)
	require.Equal(t, "ro", resp.Tools[0].Name)
}

func TestBase_DescribeTool_KnownTool(t *testing.T) {
	schema, _ := structpb.NewStruct(map[string]any{"type": "object"})
	f := newFake([]*registry.ToolDefinition{
		{Name: "x.do", SummaryDescription: "short", LongDescription: "long", InputSchema: schema},
	})

	resp, err := f.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: "x.do"})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.NotNil(t, resp.Tool)
	require.Equal(t, "x.do", resp.Tool.Name)
	require.Equal(t, "long", resp.Tool.Description, "ToolSpec uses LongDescription when present")
}

func TestBase_DescribeTool_UnknownTool_ReturnsInBandError(t *testing.T) {
	f := newFake([]*registry.ToolDefinition{
		{Name: "exists", SummaryDescription: "x"},
	})

	resp, err := f.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: "missing"})
	require.NoError(t, err, "unknown tool surfaces as response.error, not gRPC error")
	require.Nil(t, resp.Tool)
	require.Contains(t, resp.Error, "missing")
	require.Contains(t, resp.Error, "ListToolSummaries",
		"error message points the caller at the discovery RPC")
}

func TestBase_CallTool_DispatchesToHandler(t *testing.T) {
	var captured *toolboxv0.CallToolRequest
	f := newFake([]*registry.ToolDefinition{
		{
			Name: "x.do",
			Handler: func(_ context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
				captured = req
				return &toolboxv0.CallToolResponse{}
			},
		},
	})

	args, _ := structpb.NewStruct(map[string]any{"k": "v"})
	resp, err := f.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "x.do",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.NotNil(t, captured, "handler ran")
	require.Equal(t, "x.do", captured.Name)
}

func TestBase_CallTool_UnknownTool_ReturnsInBandError(t *testing.T) {
	f := newFake([]*registry.ToolDefinition{
		{Name: "exists", Handler: func(_ context.Context, _ *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
			return &toolboxv0.CallToolResponse{}
		}},
	})

	resp, err := f.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "missing"})
	require.NoError(t, err)
	require.Contains(t, resp.Error, "missing")
}

func TestBase_CallTool_NilHandler_SurfacesPluginBug(t *testing.T) {
	// Tool defined but Handler not wired — surfaces as a clear error
	// instead of a nil-deref panic. Catches a real plugin-author bug
	// (defining a tool but forgetting to set Handler).
	f := newFake([]*registry.ToolDefinition{
		{Name: "broken", Handler: nil},
	})

	resp, err := f.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "broken"})
	require.NoError(t, err, "no panic, no gRPC error — clean in-band response")
	require.Contains(t, strings.ToLower(resp.Error), "no handler")
}
