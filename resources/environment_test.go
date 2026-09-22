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
	require.Equal(t, before, after)
	require.Contains(t, restored.Extensions, "host-policy")
	require.Equal(t, "source description", restored.Extensions["host-policy"].(map[string]any)["source"])
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
	_, err := yaml.Marshal(resources.Environment{Name: "staging", Extensions: map[string]any{"name": "retargeted"}})
	require.Error(t, err)
}
