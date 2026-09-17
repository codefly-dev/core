package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestResolveModuleClosureDerivesParticipationFromDeclarations(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"wiki"})
	require.NoError(t, err)

	// wiki declares saas and documents; saas reaches accounts within itself and
	// pulls in nothing further. analytics is pinned but nothing reaches it.
	require.Equal(t, []string{"wiki", "saas", "documents"}, closure.Names())
	require.True(t, closure.Contains("documents"))
	require.False(t, closure.Contains("analytics"))
	require.Contains(t, workspace.ModulesNames(), "analytics")
}

func TestResolveModuleClosureRecordsEdges(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"wiki"})
	require.NoError(t, err)

	// The same-module auth-gateway -> accounts dependency crosses no module
	// boundary, so it contributes no participation edge. wiki-api declares two
	// dependencies into saas (auth-gateway and sessions); an edge says a service
	// pulls a module in, so the pair collapses to one rather than surfacing the
	// same participation fact twice.
	require.Equal(t, []resources.ModuleEdge{
		{From: "wiki", FromService: "wiki-api", To: "saas"},
		{From: "wiki", FromService: "wiki-api", To: "documents"},
	}, closure.Edges)
}

func TestResolveModuleClosureSkipsExternalDependencies(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	// wiki-api declares an external dependency on module "stripe", which the
	// workspace does not pin. External capabilities live outside the workspace,
	// so they must not demand a pin.
	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"wiki"})
	require.NoError(t, err)
	require.False(t, closure.Contains("stripe"))
}

// A dependency pulls a module in only for the stages its kind constrains.
// wiki-api consumes saas and documents at runtime and reads codegen's contract
// at build time; a run that demanded a pin for codegen would be asking for a
// module it never touches.
func TestResolveModuleClosureScopesToTheStage(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	run, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"wiki"})
	require.NoError(t, err)
	require.False(t, run.Contains("codegen"))

	build, err := workspace.ResolveModuleClosure(ctx, resources.StageBuild, []string{"wiki"})
	require.NoError(t, err)
	require.True(t, build.Contains("codegen"))
}

func TestResolveModuleClosureRejectsUnknownStage(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	_, err = workspace.ResolveModuleClosure(ctx, resources.Stage("deploy"), []string{"wiki"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown stage")
}

func TestResolveModuleClosureSeedsAreIndependent(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"analytics"})
	require.NoError(t, err)
	require.Equal(t, []string{"analytics", "saas"}, closure.Names())

	both, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"wiki", "analytics"})
	require.NoError(t, err)
	require.Equal(t, []string{"wiki", "analytics", "saas", "documents"}, both.Names())
}

func TestResolveModuleClosureRejectsUnpinnedSeed(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	_, err = workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"ghost"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `module "ghost" is not pinned`)
	require.Contains(t, err.Error(), "analytics, codegen, documents, saas, wiki")
}

// A declaration reaching a module the pin set does not cover is the drift the
// hand-maintained module list used to hide: the run came up with no endpoints
// instead of saying which declaration went unanswered.
func TestResolveModuleClosureRejectsUnpinnedProducer(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/unresolved-dependency-visibility")
	require.NoError(t, err)

	_, err = workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"platform"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `service platform/api depends on module "billing"`)
	require.Contains(t, err.Error(), "does not pin")
}

func TestModuleClosureValidateServiceDependenciesScopesToTheRun(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/derived-module-closure")
	require.NoError(t, err)

	// analytics consumes a private saas endpoint, so the workspace-wide pass
	// fails. That edge belongs to no run seeded from wiki, and scoping the same
	// check to the closure is what keeps validation and the run in agreement.
	require.Error(t, workspace.ValidateServiceDependencies(ctx))

	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"wiki"})
	require.NoError(t, err)
	require.NoError(t, closure.ValidateServiceDependencies(ctx))
}

func TestModuleClosureValidateServiceDependenciesRejectsDeniedEdge(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/denied-dependency-visibility")
	require.NoError(t, err)

	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"platform"})
	require.NoError(t, err)
	err = closure.ValidateServiceDependencies(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), `private to module "saas"`)
}
