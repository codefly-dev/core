package bash_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/toolbox/bash"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ManifestToBashRefusal walks the entire chain that
// makes the architecture work:
//
//  1. A Toolbox manifest is loaded from disk (real YAML, real loader).
//  2. The manifest's `canonical_for: [git]` populates a CanonicalRegistry
//     via Toolbox.RegisterCanonical.
//  3. A bash.Toolbox constructed with that registry refuses `git status`
//     and surfaces the manifest's plugin name in the error.
//
// This is the single test that proves the layers connect end-to-end.
// If this regresses, the architecture is broken even if the unit tests
// for each individual layer still pass.
func TestIntegration_ManifestToBashRefusal(t *testing.T) {
	// 1. Write a real toolbox manifest to disk.
	dir := t.TempDir()
	manifest := []byte(`
name: my-git-plugin
version: 0.0.1
description: Git operations as typed RPCs.
agent:
  kind: codefly:toolbox
  name: git
  publisher: codefly.dev
  version: 0.0.1
sandbox:
  read_paths:  ["${WORKSPACE}"]
  write_paths: ["${WORKSPACE}"]
  network: deny
canonical_for:
  - git
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, resources.ToolboxConfigurationName),
		manifest, 0o600))

	// 2. Load it. Build the registry from it.
	tb, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.NoError(t, err)

	reg, err := resources.BuildCanonicalRegistry(tb)
	require.NoError(t, err)

	// 3. Bash refuses git, naming the plugin.
	bashTb := bash.New(reg, sandbox.NewNative())
	_, err = bashTb.Exec(context.Background(), "git status")
	require.Error(t, err)

	var routed *bash.CanonicalRoutedError
	require.True(t, errors.As(err, &routed),
		"git invocation must surface as CanonicalRoutedError carrying the manifest-derived owner")
	require.Equal(t, "git", routed.Binary)
	require.Equal(t, "my-git-plugin", routed.Owner,
		"the bash error must point at the toolbox NAME from the manifest, not the agent name or some default")
}

// TestIntegration_TwoToolboxes_ClaimingSameBinary_FailsAtLoad guards
// the "load-time error, not first-invocation surprise" promise the
// architecture makes. An operator dropping two conflicting toolbox
// manifests into the workspace gets a clean diagnostic at registry
// build, not a confusing failure six layers deep when an agent
// happens to invoke the contested binary.
func TestIntegration_TwoToolboxes_ClaimingSameBinary_FailsAtLoad(t *testing.T) {
	mk := func(name string) *resources.Toolbox {
		return &resources.Toolbox{
			Name:    name,
			Version: "0.0.1",
			Agent: &resources.Agent{
				Kind:    resources.ToolboxAgent,
				Name:    name,
				Version: "0.0.1",
			},
			CanonicalFor: []string{"git"},
		}
	}
	_, err := resources.BuildCanonicalRegistry(mk("first"), mk("second"))
	require.Error(t, err, "double-claim must fail at registry build, not at first git invocation")
	require.Contains(t, err.Error(), "first", "first claimant named in the error")
	require.Contains(t, err.Error(), "second", "second claimant named in the error")
	require.Contains(t, err.Error(), "git", "contested binary named in the error")
}
