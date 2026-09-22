package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

func TestWorkspacePreservesHostDeclarationsWithoutTransportingThem(t *testing.T) {
	ctx := context.Background()
	ws, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join("testdata", "workspaces", "host-extensions"))
	require.NoError(t, err)
	env := ws.FindEnvironment("staging")
	require.Equal(t, "shared", env.ConfigurationProfile)
	before := env.Extensions["host-binding"]
	ws.Description = "edited by a Core consumer"
	destination := t.TempDir()
	require.NoError(t, ws.SaveToDirUnsafe(ctx, destination))
	restored, err := resources.LoadWorkspaceFromDir(ctx, destination)
	require.NoError(t, err)
	after := restored.FindEnvironment("staging").Extensions["host-binding"]
	var beforeData, afterData any
	require.NoError(t, before.Decode(&beforeData))
	require.NoError(t, after.Decode(&afterData))
	require.Equal(t, beforeData, afterData)
	require.Contains(t, restored.Extensions, "host-policy")
	policy := restored.Extensions["host-policy"]
	var policyData map[string]any
	require.NoError(t, policy.Decode(&policyData))
	require.Equal(t, "source description", policyData["source"])
	require.Equal(t, ws.Description, restored.Description)
	wire, err := env.Proto()
	require.NoError(t, err)
	serialized, err := protojson.Marshal(wire)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "opaque-principal")
	require.NotContains(t, string(serialized), "host-binding")
}

func TestWorkspaceRejectsEmptyEnvironmentEntry(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte("name: empty\nlayout: modules\nenvironments:\n  -\n"), 0o600))
	_, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.ErrorContains(t, err, "empty environment")
}

func TestEnvironmentExtensionCannotShadowRuntimeField(t *testing.T) {
	_, err := yaml.Marshal(resources.Environment{Name: "staging", Extensions: map[string]resources.YAMLValue{"name": {}}})
	require.Error(t, err)
}

func TestHostValuesPreserveScalarSpellingsAcrossWorkspaceSave(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "workspaces", "host-scalars", resources.WorkspaceConfigurationName))
	require.NoError(t, err)
	var expected struct {
		HostValues   map[string]string `yaml:"host-values"`
		Environments []struct {
			HostValues map[string]string `yaml:"host-values"`
		} `yaml:"environments"`
	}
	require.NoError(t, yaml.Unmarshal(data, &expected))
	workspace, err := resources.LoadWorkspaceFromDir(t.Context(), filepath.Join("testdata", "workspaces", "host-scalars"))
	require.NoError(t, err)
	for range 2 {
		destination := t.TempDir()
		require.NoError(t, workspace.SaveToDirUnsafe(t.Context(), destination))
		saved, err := os.ReadFile(filepath.Join(destination, resources.WorkspaceConfigurationName))
		require.NoError(t, err)
		actual := expected
		actual.HostValues = nil
		actual.Environments = nil
		require.NoError(t, yaml.Unmarshal(saved, &actual))
		require.Equal(t, expected, actual)
		workspace, err = resources.LoadWorkspaceFromDir(t.Context(), destination)
		require.NoError(t, err)
	}
}

func TestHostValuesRejectRecursiveAliases(t *testing.T) {
	_, err := resources.LoadFromBytes[resources.Workspace]([]byte("name: test\nhost-value: &recursive [*recursive]\n"))
	require.ErrorContains(t, err, "contains itself")
}
