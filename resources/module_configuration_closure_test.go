package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestModuleClosureIncludesTransitiveConfigurationProducers(t *testing.T) {
	workspace, err := resources.LoadWorkspaceFromDir(t.Context(), "testdata/workspaces/configuration-module-closure")
	require.NoError(t, err)
	references := resources.WithModuleConfigurationReferences(map[string][]string{
		"provider":  {"provider/api/http", "provider/api::http", "app/api/http", "absent/api/http", "invalid"},
		"catalog":   {"catalog/api/http"},
		"root-only": {"unused/api/http"},
	})
	closure, err := workspace.ResolveModuleClosure(t.Context(), resources.StageRun, []string{"app"}, references)
	require.NoError(t, err)
	require.Equal(t, []string{"app", "provider", "catalog", "storage"}, closure.Names())
	require.Equal(t, []resources.ModuleEdge{
		{From: "app", FromService: "api", To: "provider"},
		{From: "provider", FromService: "api", To: "catalog"},
		{From: "catalog", FromService: "api", To: "storage"},
	}, closure.Edges)
	require.NoError(t, closure.ValidateServiceDependencies(t.Context()))
	require.Len(t, workspace.Modules, 5, "the declared workspace is unchanged")

	build, err := workspace.ResolveModuleClosure(t.Context(), resources.StageBuild, []string{"app"}, references)
	require.NoError(t, err)
	require.Equal(t, []string{"app"}, build.Names(), "runtime configuration never adds build inputs")
	plain, err := workspace.ResolveModuleClosure(t.Context(), resources.StageRun, []string{"app"})
	require.NoError(t, err)
	require.Equal(t, []string{"app"}, plain.Names(), "without references there are no producer demands")
}
