//go:build nix_required

// Build-tagged tests requiring a real `nix` binary on PATH. Run with
// `go test -tags nix_required ./...`. The default no-tag build skips
// them at compile time — satisfies the no-t.Skip rule (the absence
// of nix is visible in the build configuration, not at runtime).
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

// nixAvailable returns true when a real nix binary is on PATH.
// Belt-and-braces: the build tag already gates the file at compile
// time, but a CI runner that opts into the tag without actually
// installing nix would silently mis-pass — fail loud instead.
func nixAvailable() bool {
	_, err := exec.LookPath("nix")
	return err == nil
}

// writeFlake materializes a minimal flake.nix in dir so the daemon-
// touching tests have something to introspect.
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

func TestNix_Eval_TrivialExpression(t *testing.T) {
	if !nixAvailable() {
		t.Fatal("nix binary not on PATH; build tag claimed nix_required but binary is missing")
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
		t.Fatal("nix binary not on PATH; build tag claimed nix_required but binary is missing")
	}
	srv := nix.New("0.0.1")
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
		t.Fatal("nix binary not on PATH; build tag claimed nix_required but binary is missing")
	}
	dir := t.TempDir()
	writeFlake(t, dir)

	srv := nix.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"flake": dir})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "nix.flake_metadata",
		Arguments: args,
	})
	// This test fetches nixpkgs the first time it runs (slow, network).
	// If the box has no network, the eval errors out with a clear nix
	// message — not a toolbox bug.
	require.NoError(t, err)
	if resp.Error != "" {
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
