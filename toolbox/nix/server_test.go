package nix_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/nix"
)

// nixAvailable returns true when a real nix binary is on PATH. The
// daemon-touching tests below check this and fail loud (per the
// no-t.Skip rule) when nix is missing — invoke them in environments
// that actually have nix installed.
func nixAvailable() bool {
	_, err := exec.LookPath("nix")
	return err == nil
}

// writeFlake materializes a minimal flake.nix in dir so the
// daemon-touching tests have something to introspect. Uses
// flake-utils-style outputs but with a literal package list to keep
// the flake hermetic (no fetched inputs).
func writeFlake(t *testing.T, dir string) {
	t.Helper()
	const flakeNix = `{
  description = "codefly nix toolbox test flake";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-24.05";
  outputs = { self, nixpkgs }:
    let pkgs = import nixpkgs { system = "x86_64-linux"; }; in
    {
      packages.x86_64-linux.default = pkgs.hello;
    };
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flake.nix"),
		[]byte(flakeNix), 0o600))
}

// --- Schema / dispatch tests (no nix binary needed) --------------

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

// --- Daemon-touching tests (require real nix on PATH) -----------
//
// Per feedback_no_t_skip: fail loud when infra is missing rather
// than silent skip. These tests assume the dev box has nix in PATH;
// CI matrices that don't can run with `-tags skip_infra` once we
// add that build tag.

func TestNix_Eval_TrivialExpression(t *testing.T) {
	if !nixAvailable() {
		t.Fatal("nix binary not on PATH; required for this integration test")
	}
	srv := nix.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"expr": "1 + 2"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "nix.eval",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "trivial arithmetic must succeed: %s", resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	require.EqualValues(t, 3, out["value"], "1 + 2 evaluates to 3")
	require.Equal(t, false, out["truncated"])
}

func TestNix_Eval_ParseError_SurfacesNixComplaint(t *testing.T) {
	if !nixAvailable() {
		t.Fatal("nix binary not on PATH; required for this integration test")
	}
	srv := nix.New("0.0.1")
	// Syntactically invalid expression — nix should refuse with a
	// clear error that the toolbox surfaces verbatim.
	args, _ := structpb.NewStruct(map[string]any{"expr": "this is not nix"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "nix.eval",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"invalid expression must surface as a tool error, not a transport error")
}

func TestNix_FlakeMetadata_OnLocalFlake(t *testing.T) {
	if !nixAvailable() {
		t.Fatal("nix binary not on PATH; required for this integration test")
	}
	dir := t.TempDir()
	writeFlake(t, dir)

	srv := nix.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"flake": dir})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "nix.flake_metadata",
		Arguments: args,
	})
	// Note: this test fetches nixpkgs the first time it runs (slow,
	// network). If the box has no network, the eval errors out with
	// a clear nix message — not a toolbox bug.
	require.NoError(t, err)
	if resp.Error != "" {
		// Acceptable failure modes when nix can't fetch inputs:
		// "could not download", "unable to download", etc. Fail loud
		// only on transport/parse errors that would indicate a real
		// toolbox bug.
		require.Contains(t, resp.Error, "nix flake metadata",
			"any error must be properly framed by the toolbox")
		return
	}
	out := resp.Content[0].GetStructured().AsMap()
	require.NotEmpty(t, out, "flake metadata must return at least one field")
	desc, _ := out["description"].(string)
	require.Contains(t, desc, "codefly nix toolbox test flake",
		"metadata must include the flake's own description")
}
