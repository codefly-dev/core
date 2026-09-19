//go:build nix_required

package ciguard

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Evaluate the package the companion actually includes, rather than searching
// flake.nix for a version string: an unused pin alongside pkgs.buf still builds
// the old compiler. The shell and both image outputs share protoTools.
func TestNixProtoCompanionBufMatchesTheGate(t *testing.T) {
	flake := strconv.Quote("path:" + filepath.Join(repoRoot(t), "companions", "proto"))
	for _, system := range []string{"x86_64-linux", "aarch64-linux"} {
		t.Run(system, func(t *testing.T) {
			expression := fmt.Sprintf(`let
  flake = builtins.getFlake %s;
  tools = flake.devShells.%s.default.nativeBuildInputs;
in map (tool: tool.version) (builtins.filter (tool: (tool.pname or "") == "buf") tools)`, flake, system)
			cmd := exec.CommandContext(t.Context(), "nix", "eval", "--json", "--impure", "--expr", expression)
			output, err := cmd.Output()
			if failure, ok := err.(*exec.ExitError); ok {
				t.Fatalf("evaluate the companion's Nix buf: %v\n%s", err, failure.Stderr)
			}
			require.NoError(t, err, "nix is required to verify the companion's resolved compiler")
			var versions []string
			require.NoError(t, json.Unmarshal(output, &versions))
			require.Equal(t, []string{makefileBufVersion(t)}, versions,
				"the Nix companion must include exactly the buf the gate runs, not the older one from nixpkgs")
		})
	}
}
