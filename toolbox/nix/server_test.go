package nix_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/nix"
)

// Default-build (no tag) tests cover the schema, dispatch, and
// argument-validation surface — every code path that does NOT need a
// real `nix` binary on PATH.
//
// Tests requiring a real nix binary live in server_integration_test.go
// behind `//go:build nix_required`. Run them with
// `go test -tags nix_required ./...` on a host that has nix installed.

func TestNix_Identity(t *testing.T) {
	srv := nix.New("0.0.1")
	resp, err := srv.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "nix", resp.Name)
	require.Equal(t, "0.0.1", resp.Version)
	require.Equal(t, []string{"nix"}, resp.CanonicalFor,
		"nix toolbox owns the `nix` binary in the canonical-routing layer")
	require.NotEmpty(t, resp.SandboxSummary)
}

func TestNix_ListTools_Stable(t *testing.T) {
	srv := nix.New("0.0.1")
	resp, err := srv.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	require.ElementsMatch(t, []string{
		"nix.flake_metadata",
		"nix.flake_show",
		"nix.eval",
	}, names, "if the surface changes, pin it here")

	for _, tl := range resp.Tools {
		require.NotEmpty(t, tl.Description, "every tool needs a description")
		require.NotNil(t, tl.InputSchema, "every tool needs an input schema")
	}
}

func TestNix_UnknownTool_ActionableError(t *testing.T) {
	srv := nix.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "nix.bogus"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "nix.bogus")
	require.Contains(t, resp.Error, "ListTools")
}

func TestNix_Eval_RequiresExpr(t *testing.T) {
	srv := nix.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "nix.eval",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "expr is required",
		"missing expr must be surfaced cleanly, not a binary error")
}

func TestNix_BinaryNotFound_ProducesActionableError(t *testing.T) {
	// Point the toolbox at a nonexistent binary path; the LookPath
	// guard should produce an "install nix" hint rather than a
	// confusing exec error.
	srv := nix.New("0.0.1").WithBinary("/no/such/nix/binary")
	args, _ := structpb.NewStruct(map[string]any{"expr": "1 + 1"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "nix.eval",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "nix binary not found",
		"missing-binary error must be self-explanatory")
}
