package architecture

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestRestrictKeepsDependencyOptions(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/module-layout")
	require.NoError(t, err)

	dep, err := NewServiceDependencies(ctx, workspace,
		ExcludeServices("billing/accounts"),
		SkipDependencyFor("web/gateway"))
	require.NoError(t, err)

	restricted, err := dep.Restrict(ctx, "web/frontend")
	require.NoError(t, err)

	require.Equal(t, dep.options.ExcludeService, restricted.options.ExcludeService)
	require.Equal(t, dep.options.SkipDependencyFor, restricted.options.SkipDependencyFor)

	// A DependencyOption is an exported func type over an exported struct, so a caller
	// can retain the options it is handed at construction: the views must not share maps.
	restricted.options.ExcludeService["probe/added"] = true
	restricted.options.SkipDependencyFor["probe/added"] = true
	require.NotContains(t, dep.options.ExcludeService, "probe/added")
	require.NotContains(t, dep.options.SkipDependencyFor, "probe/added")

	// withGraph is shared with ForStage, so every derived view gets its own maps.
	staged, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)
	require.Equal(t, dep.options.ExcludeService, staged.options.ExcludeService)
	staged.options.ExcludeService["probe/staged"] = true
	require.NotContains(t, dep.options.ExcludeService, "probe/staged")
}
