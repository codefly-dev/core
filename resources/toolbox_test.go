package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func writeToolboxManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, resources.ToolboxConfigurationName),
		[]byte(body), 0o600))
	return dir
}

func TestLoadToolboxFromDir_HappyPath(t *testing.T) {
	dir := writeToolboxManifest(t, `
name: git
version: 0.0.1
description: Git repository operations as typed RPCs.
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
	tb, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.Equal(t, "git", tb.Name)
	require.Equal(t, "0.0.1", tb.Version)
	require.Equal(t, resources.ToolboxAgent, tb.Agent.Kind)
	require.Equal(t, []string{"git"}, tb.CanonicalFor)
	require.Equal(t, policy.NetworkDeny, tb.Sandbox.Network)
	require.Equal(t, []string{"${WORKSPACE}"}, tb.Sandbox.ReadPaths)
	require.Equal(t, "git@0.0.1", tb.Identity())
	require.Equal(t, dir, tb.Dir())
}

func TestLoadToolboxFromDir_MissingFile_Errors(t *testing.T) {
	dir := t.TempDir()
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), resources.ToolboxConfigurationName,
		"error must name the missing file so devs know what to create")
}

func TestLoadToolboxFromDir_MalformedYAML_Errors(t *testing.T) {
	dir := writeToolboxManifest(t, "not: valid: yaml: oops")
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
}

func TestToolbox_Validate_RequiresName(t *testing.T) {
	dir := writeToolboxManifest(t, `
version: 0.0.1
agent:
  kind: codefly:toolbox
  name: git
  publisher: codefly.dev
  version: 0.0.1
`)
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "name")
}

func TestToolbox_Validate_RequiresVersion(t *testing.T) {
	dir := writeToolboxManifest(t, `
name: git
agent:
  kind: codefly:toolbox
  name: git
  publisher: codefly.dev
  version: 0.0.1
`)
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "version")
}

func TestToolbox_Validate_RequiresAgentKind_Toolbox(t *testing.T) {
	dir := writeToolboxManifest(t, `
name: git
version: 0.0.1
agent:
  kind: codefly:service
  name: git
  publisher: codefly.dev
  version: 0.0.1
`)
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "codefly:toolbox",
		"a service-kind agent in a toolbox manifest is a typo or copy-paste error; surface it loudly")
}

func TestToolbox_Validate_RejectsBogusNetworkPolicy(t *testing.T) {
	dir := writeToolboxManifest(t, `
name: git
version: 0.0.1
agent:
  kind: codefly:toolbox
  name: git
  publisher: codefly.dev
  version: 0.0.1
sandbox:
  network: maybe
`)
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "maybe")
}

func TestToolbox_BuildCanonicalRegistry_HappyPath(t *testing.T) {
	gitTb := &resources.Toolbox{
		Name:         "git-toolbox",
		Version:      "0.0.1",
		Agent:        &resources.Agent{Kind: resources.ToolboxAgent, Name: "git", Version: "0.0.1"},
		CanonicalFor: []string{"git"},
	}
	dockerTb := &resources.Toolbox{
		Name:         "docker-toolbox",
		Version:      "0.0.1",
		Agent:        &resources.Agent{Kind: resources.ToolboxAgent, Name: "docker", Version: "0.0.1"},
		CanonicalFor: []string{"docker"},
	}
	reg, err := resources.BuildCanonicalRegistry(gitTb, dockerTb)
	require.NoError(t, err)

	// Claims registered: git-toolbox owns git, docker-toolbox owns docker.
	d := reg.Lookup("git")
	require.NotNil(t, d)
	require.Equal(t, "git-toolbox", d.Owner)

	d = reg.Lookup("docker")
	require.NotNil(t, d)
	require.Equal(t, "docker-toolbox", d.Owner)

	// Built-in fallback still covers what nobody claimed.
	d = reg.Lookup("nix")
	require.NotNil(t, d, "unclaimed canonical-fallback binary still routes")
	require.Empty(t, d.Owner)
}

func TestToolbox_BuildCanonicalRegistry_DoubleClaimSurfaces(t *testing.T) {
	a := &resources.Toolbox{
		Name:         "alpha",
		Version:      "0.0.1",
		Agent:        &resources.Agent{Kind: resources.ToolboxAgent, Name: "a", Version: "0.0.1"},
		CanonicalFor: []string{"git"},
	}
	b := &resources.Toolbox{
		Name:         "beta",
		Version:      "0.0.1",
		Agent:        &resources.Agent{Kind: resources.ToolboxAgent, Name: "b", Version: "0.0.1"},
		CanonicalFor: []string{"git"},
	}
	_, err := resources.BuildCanonicalRegistry(a, b)
	require.Error(t, err, "two toolboxes claiming the same binary must fail at registry-build time")
	require.Contains(t, err.Error(), "alpha", "error must name the existing claimant")
	require.Contains(t, err.Error(), "git", "error must name the contested binary")
	require.Contains(t, err.Error(), "beta@0.0.1",
		"error must name the second-claiming toolbox via Identity so operators can find the offending manifest")
}

func TestToolbox_Validate_RejectsEmptyCanonicalFor(t *testing.T) {
	dir := writeToolboxManifest(t, `
name: git
version: 0.0.1
agent:
  kind: codefly:toolbox
  name: git
  publisher: codefly.dev
  version: 0.0.1
canonical_for:
  - git
  - ""
`)
	_, err := resources.LoadToolboxFromDir(context.Background(), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "canonical_for")
}
