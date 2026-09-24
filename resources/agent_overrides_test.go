package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// copyWorkspaceWithOverrides copies a fixture workspace and appends an
// agent-overrides block to its workspace.codefly.yaml.
func copyWorkspaceWithOverrides(t *testing.T, fixture, block string) string {
	t.Helper()
	dst := t.TempDir()
	require.NoError(t, os.CopyFS(dst, os.DirFS(fixture)))
	path := filepath.Join(dst, resources.WorkspaceConfigurationName)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(content, []byte(block)...), 0o600))
	return dst
}

func TestAgentOverrideMovesOnlyTheVersionOfComposedServices(t *testing.T) {
	ctx := context.Background()
	dir := copyWorkspaceWithOverrides(t, "testdata/workspaces/interface-boundary-visibility",
		"agent-overrides:\n  codefly.ai/go-grpc: 0.1.47\n")

	declared, err := resources.LoadServiceFromDir(ctx, filepath.Join(dir, "modules/saas/services/gateway"))
	require.NoError(t, err)
	require.Equal(t, "0.0.1", declared.Agent.Version, "the module's own file is untouched")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := mod.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Equal(t, "0.1.47", gateway.Agent.Version)
	require.Equal(t, declared.Agent.Name, gateway.Agent.Name)
	require.Equal(t, declared.Agent.Publisher, gateway.Agent.Publisher)
	require.Equal(t, declared.Agent.Kind, gateway.Agent.Kind)

	// The block survives a load-and-save of the workspace.
	require.NoError(t, workspace.Save(ctx))
	saved, err := os.ReadFile(filepath.Join(dir, resources.WorkspaceConfigurationName))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(saved, &doc))
	require.Equal(t, map[string]any{"codefly.ai/go-grpc": "0.1.47"}, doc[resources.AgentOverridesKey])
}

func TestAgentOverrideLeavesOtherAgentsAlone(t *testing.T) {
	ctx := context.Background()
	dir := copyWorkspaceWithOverrides(t, "testdata/workspaces/interface-boundary-visibility",
		"agent-overrides:\n  codefly.dev/go-grpc: 0.1.47\n")
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := mod.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Equal(t, "0.0.1", gateway.Agent.Version, "a different publisher is a different agent")
}

func TestAgentOverrideRefusesMalformedEntries(t *testing.T) {
	ctx := context.Background()
	for name, block := range map[string]string{
		"no publisher":  "agent-overrides:\n  go-grpc: 0.1.47\n",
		"extra segment": "agent-overrides:\n  codefly.ai/go/grpc: 0.1.47\n",
		"range":         "agent-overrides:\n  codefly.ai/go-grpc: ^0.1.47\n",
		"partial":       "agent-overrides:\n  codefly.ai/go-grpc: \"0.1\"\n",
		"v prefix":      "agent-overrides:\n  codefly.ai/go-grpc: v0.1.47\n",
		"not a map":     "agent-overrides:\n  - codefly.ai/go-grpc\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := copyWorkspaceWithOverrides(t, "testdata/workspaces/interface-boundary-visibility", block)
			workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
			require.NoError(t, err)
			_, err = workspace.LoadModuleFromName(ctx, "saas")
			require.Error(t, err)
			require.Contains(t, err.Error(), resources.AgentOverridesKey)
		})
	}
}

func TestParseAgentOverridesIsSortedAndKeyed(t *testing.T) {
	overrides, err := resources.ParseAgentOverrides(map[string]string{
		"codefly.dev/nextjs":  "0.0.160",
		"codefly.dev/go-grpc": " 0.1.47 ",
	})
	require.NoError(t, err)
	require.Equal(t, []resources.AgentOverride{
		{Publisher: "codefly.dev", Name: "go-grpc", Version: "0.1.47"},
		{Publisher: "codefly.dev", Name: "nextjs", Version: "0.0.160"},
	}, overrides)
	require.Equal(t, "codefly.dev/go-grpc", overrides[0].Key())
}
