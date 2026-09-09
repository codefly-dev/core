package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func uniques(services []architecture.Service) []string {
	out := make([]string, 0, len(services))
	for _, service := range services {
		out = append(out, service.Unique)
	}
	return out
}

// The workspace composes a module that has no checkout. Loading the whole
// workspace graph fails, but a target whose closure never reaches that module
// must still plan.
func TestSelectClosureIgnoresUnavailableUnrelatedModule(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/unavailable-module")
	require.NoError(t, err)

	_, err = architecture.NewServiceDependencies(ctx, workspace)
	require.Error(t, err, "the eager whole-workspace graph cannot be built")

	closure, err := architecture.SelectClosure(ctx, workspace, "billing/accounts")
	require.NoError(t, err)
	require.NoError(t, closure.Verify(ctx))

	order, err := closure.Order(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"management/organization", "billing/accounts"}, uniques(order))

	_, err = closure.ServiceFromUnique("web/portal")
	require.Error(t, err, "a service outside the closure is not selected")
}

// Depending on the unavailable module is a resolution failure, not a silently
// narrower closure: the node stays, carrying why it could not be resolved.
func TestSelectClosureReportsUnavailableModuleOnAnEdge(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/unavailable-module")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "web/portal")
	require.NoError(t, err)

	err = closure.Verify(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vault/secrets")
	require.Contains(t, err.Error(), "required by web/portal")
	require.Contains(t, err.Error(), "cannot load module <vault>")
}

func TestSelectClosureReportsUncomposedModule(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/unavailable-module")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "absent/service")
	require.NoError(t, err)

	err = closure.Verify(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module <absent> is not composed in workspace <codefly-platform>")
	require.Contains(t, err.Error(), resources.WorkspaceConfigurationName)
}

func TestSelectClosureEnforcesVisibility(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-denied")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "web/portal")
	require.NoError(t, err)

	err = closure.Verify(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/portal")
	require.Contains(t, err.Error(), "vault/secrets")
}

func TestSelectClosureEnforcesAcyclicity(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/plan-cycle")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "loop/left")
	require.NoError(t, err)

	err = closure.Verify(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")
}

func TestSelectClosureFlatLayout(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "gateway")
	require.NoError(t, err)
	require.NoError(t, closure.Verify(ctx))

	order, err := closure.Order(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{
		"codefly-platform/organization",
		"codefly-platform/accounts",
		"codefly-platform/gateway",
	}, uniques(order))
}

func TestSelectClosureExcludesServices(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/plan-workspace")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "api/orders",
		architecture.ExcludeServices("data/postgres"))
	require.NoError(t, err)
	require.NoError(t, closure.Verify(ctx))

	order, err := closure.Order(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"api/orders"}, uniques(order))

	_, err = closure.ServiceFromUnique("data/postgres")
	require.Error(t, err, "an excluded service is not selected")
}
