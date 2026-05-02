package git_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/git"
)

// TestGit_ListToolSummaries_Lightweight verifies the catalog response
// is the SHORT description shape — no schemas, just one-liner +
// tags. This is what gets loaded into Mind's context every turn;
// the size discipline comes from this list staying small.
func TestGit_ListToolSummaries_Lightweight(t *testing.T) {
	srv := git.New(t.TempDir(), "0.0.1")
	resp, err := srv.ListToolSummaries(context.Background(), &toolboxv0.ListToolSummariesRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Tools, 3, "git toolbox has three tools today (status/log/diff)")

	for _, sum := range resp.Tools {
		require.NotEmpty(t, sum.Name)
		require.NotEmpty(t, sum.Description, "every tool needs a one-line description for routing")
		require.LessOrEqual(t, len(sum.Description), 250,
			"summary description should be terse (~120 chars) — found %d for %q", len(sum.Description), sum.Name)
		require.Contains(t, sum.Tags, "git",
			"every tool tags its parent toolbox name for catalog filtering")
		require.Contains(t, sum.Tags, "read-only",
			"git toolbox today is entirely read-only; if that changes, update this assertion")
	}
}

// TestGit_ListToolSummaries_TagsFilter_ANDSemantics confirms the
// filter applies — every tool that survives the filter must have
// EVERY tag in the filter.
func TestGit_ListToolSummaries_TagsFilter_ANDSemantics(t *testing.T) {
	srv := git.New(t.TempDir(), "0.0.1")
	// All git tools are read-only → all should pass.
	resp, err := srv.ListToolSummaries(context.Background(), &toolboxv0.ListToolSummariesRequest{
		TagsFilter: []string{"read-only"},
	})
	require.NoError(t, err)
	require.Len(t, resp.Tools, 3)

	// No git tool tags itself "destructive" → empty.
	resp, err = srv.ListToolSummaries(context.Background(), &toolboxv0.ListToolSummariesRequest{
		TagsFilter: []string{"destructive"},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Tools, "git toolbox has no destructive tools today")
}

// TestGit_DescribeTool_KnownAndUnknown — known names return the
// full ToolSpec with examples + error_modes; unknown names surface
// the error envelope with an actionable message (no panic).
func TestGit_DescribeTool_KnownAndUnknown(t *testing.T) {
	srv := git.New(t.TempDir(), "0.0.1")

	// Known tool — full spec.
	resp, err := srv.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.NotNil(t, resp.Tool)
	require.Equal(t, "git.status", resp.Tool.Name)
	require.NotEmpty(t, resp.Tool.Description, "ToolSpec carries the LONG description")
	require.Equal(t, "idempotent", resp.Tool.Idempotency)
	require.NotEmpty(t, resp.Tool.ErrorModes,
		"every tool must document failure modes — the LLM uses these to diagnose")
	require.NotEmpty(t, resp.Tool.Examples,
		"every tool must include at least one example — the load-bearing addition of the two-phase design")

	// Unknown tool — error envelope.
	resp, err = srv.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: "git.nope"})
	require.NoError(t, err, "unknown tool surfaces as response.error, not transport error")
	require.Empty(t, resp.Tool)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "git.nope", "error must name the bad tool")
	require.Contains(t, resp.Error, "ListToolSummaries", "error hints at the discovery RPC")
}

// TestGit_TwoPhase_RoundTrip is the integration shape: catalog →
// pick a tool → fetch spec → call. Confirms the data flowing through
// each step is consistent (the name in the summary matches the spec
// matches the CallTool dispatch).
func TestGit_TwoPhase_RoundTrip(t *testing.T) {
	srv := git.New(t.TempDir(), "0.0.1")

	// Phase 1: load catalog.
	cat, err := srv.ListToolSummaries(context.Background(), &toolboxv0.ListToolSummariesRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, cat.Tools)

	// Pick the first tool (LLM would pick by description; we just
	// take whatever's there for the round-trip assertion).
	picked := cat.Tools[0].Name

	// Phase 2: fetch its full spec.
	desc, err := srv.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: picked})
	require.NoError(t, err)
	require.Equal(t, picked, desc.Tool.Name)
	require.NotNil(t, desc.Tool.InputSchema, "spec carries the full schema; summary did not")

	// Phase 3: call it. (Doesn't assert the result content — just
	// that the dispatch path the spec describes is wired.)
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: picked})
	require.NoError(t, err, "tool listed in catalog must be callable: %s", picked)
	_ = resp // result content varies per tool; not asserting here
}
