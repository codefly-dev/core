package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/graph"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// Phases fixture:
//
//	api       -- runtime    --> database
//	migration -- runtime    --> database
//	database  -- schema     --> api         (the bootstrap reads the API contract at build time)
//	worker    -- completion --> migration
//	worker    -- build      --> api
//	worker    -- external   --> vendor/stripe
func phasesWorkspace(t *testing.T) (*resources.Workspace, map[string]string) {
	t.Helper()
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/phases")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	unique := make(map[string]string)
	for _, name := range []string{"database", "api", "migration", "worker"} {
		svc := shared.Must(workspace.FindUniqueServiceByName(ctx, name))
		require.NotNil(t, svc)
		unique[name] = svc.MustUnique()
	}
	return workspace, unique
}

func TestTwoPhaseGraphIsNotACycle(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	require.NoError(t, dep.VerifyAcyclic(ctx))

	// The untyped union of both edges IS a cycle: that is the misclassification
	// phase selection removes.
	_, err = dep.OrderTo(ctx, unique["worker"])
	require.Error(t, err)
}

func TestBuildPhaseClosure(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	build := dep.ForPhase(resources.PhaseBuild)

	// The database bootstrap needs the API contract, nothing else.
	order, err := build.OrderTo(ctx, unique["database"])
	require.NoError(t, err)
	require.Equal(t, createServices(unique["api"]), order)

	// The worker generates a client from the API. The completion prerequisite
	// on the migration and the runtime database are irrelevant to building it.
	order, err = build.OrderTo(ctx, unique["worker"])
	require.NoError(t, err)
	require.Equal(t, createServices(unique["api"]), order)

	// Nothing constrains building the API itself.
	order, err = build.OrderTo(ctx, unique["api"])
	require.NoError(t, err)
	require.Empty(t, order)
}

func TestRunPhaseClosure(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	run := dep.ForPhase(resources.PhaseRun)

	// The API waits on the database, not on its own schema consumer.
	order, err := run.OrderTo(ctx, unique["api"])
	require.NoError(t, err)
	require.Equal(t, createServices(unique["database"]), order)

	// The worker waits for the migration to COMPLETE, which itself waits for
	// the database to be up.
	order, err = run.OrderTo(ctx, unique["worker"])
	require.NoError(t, err)
	require.Equal(t, createServices(unique["database"], unique["migration"]), order)

	// Nothing runs the database bootstrap's schema producer first.
	order, err = run.OrderTo(ctx, unique["database"])
	require.NoError(t, err)
	require.Empty(t, order)
}

func TestExternalDependencyOrdersNoPhase(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	// The capability is declared, so it stays visible in the untyped graph.
	require.Contains(t, dep.Services(), architecture.Service{Unique: "vendor/stripe"})

	for _, phase := range resources.Phases() {
		requires, err := dep.ForPhase(phase).DirectRequires(ctx, unique["worker"])
		require.NoError(t, err)
		require.NotContains(t, requires, architecture.Service{Unique: "vendor/stripe"}, "phase %s", phase)
	}
}

func TestRuntimeCycleFailsWithPath(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/runtime-cycle")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	left := shared.Must(workspace.FindUniqueServiceByName(ctx, "left")).MustUnique()
	right := shared.Must(workspace.FindUniqueServiceByName(ctx, "right")).MustUnique()

	err = dep.VerifyAcyclic(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), string(resources.PhaseRun))
	require.Contains(t, err.Error(), left)
	require.Contains(t, err.Error(), right)
}

func TestLegacyWorkspaceIsAcyclicInEveryPhase(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	require.NoError(t, dep.VerifyAcyclic(ctx))

	organization := shared.Must(workspace.FindUniqueServiceByName(ctx, "organization")).MustUnique()
	accounts := shared.Must(workspace.FindUniqueServiceByName(ctx, "accounts")).MustUnique()
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "gateway")).MustUnique()
	frontend := shared.Must(workspace.FindUniqueServiceByName(ctx, "frontend")).MustUnique()

	// An undeclared kind constrains every phase, so the legacy order is the
	// same one every phase sees.
	expected := createServices(organization, accounts, gateway)
	full, err := dep.OrderTo(ctx, frontend)
	require.NoError(t, err)
	require.Equal(t, expected, full)

	for _, phase := range resources.Phases() {
		order, err := dep.ForPhase(phase).OrderTo(ctx, frontend)
		require.NoError(t, err, "phase %s", phase)
		require.Equal(t, expected, order, "phase %s", phase)
	}
}

func TestRestrictPreservesLookupAndEdgeKinds(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	restricted, err := dep.ForPhase(resources.PhaseRun).Restrict(ctx, unique["worker"])
	require.NoError(t, err)

	// Services kept by the restriction stay resolvable...
	for _, name := range []string{"worker", "migration", "database"} {
		svc, err := restricted.ServiceFromUnique(unique[name])
		require.NoError(t, err, name)
		require.Equal(t, unique[name], svc.MustUnique())
	}
	// ...and the ones it removed are gone rather than silently resolvable.
	_, err = restricted.ServiceFromUnique(unique["api"])
	require.Error(t, err)

	kinds := restricted.Graph().OutEdges(unique["migration"])
	require.Len(t, kinds, 1)
	require.Equal(t, graph.EdgeCompletion, kinds[0].Kind)
	require.Equal(t, unique["worker"], kinds[0].To)
}

func TestPhaseGraphKeepsEdgeKinds(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	g := dep.ForPhase(resources.PhaseBuild).Graph()
	edges := g.OutEdges(unique["api"])
	byTarget := make(map[string]string, len(edges))
	for _, e := range edges {
		byTarget[e.To] = e.Kind
	}
	require.Equal(t, graph.EdgeSchema, byTarget[unique["database"]])
	require.Equal(t, graph.EdgeBuildInput, byTarget[unique["worker"]])
}

func TestPhasesFixtureKeepsVisibilityEnforcement(t *testing.T) {
	ctx := context.Background()
	workspace, _ := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	require.NoError(t, dep.VerifyVisibility(ctx))
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}

func TestDependenciesCarryKinds(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	kinds := make(map[string][]resources.DependencyKind)
	for _, edge := range dep.Dependencies() {
		kinds[edge.From.Unique+" -> "+edge.To.Unique] = edge.Kinds
	}
	require.Equal(t, []resources.DependencyKind{resources.DependencyKindRuntime}, kinds[unique["database"]+" -> "+unique["api"]])
	require.Equal(t, []resources.DependencyKind{resources.DependencyKindSchema}, kinds[unique["api"]+" -> "+unique["database"]])
	require.Equal(t, []resources.DependencyKind{resources.DependencyKindCompletion}, kinds[unique["migration"]+" -> "+unique["worker"]])
	require.Equal(t, []resources.DependencyKind{resources.DependencyKindExternal}, kinds["vendor/stripe -> "+unique["worker"]])
}

func TestLegacyDependenciesCarryLegacyKind(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	for _, edge := range dep.Dependencies() {
		require.Equal(t, []resources.DependencyKind{resources.DependencyKindLegacy}, edge.Kinds, "%s -> %s", edge.From.Unique, edge.To.Unique)
	}
}
