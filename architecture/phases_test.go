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

	build, err := dep.ForStage(resources.StageBuild)
	require.NoError(t, err)

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

	run, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)

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

func TestExternalDependencyOrdersNoStage(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	// The capability is declared, so it stays visible as a graph node — but it
	// is not a service (see TestExternalProducerIsNotListedAsAService).
	require.True(t, dep.Graph().HasNode("vendor/stripe"))

	for _, stage := range resources.Stages() {
		restricted, err := dep.ForStage(stage)
		require.NoError(t, err)
		requires, err := restricted.DirectRequires(ctx, unique["worker"])
		require.NoError(t, err)
		require.NotContains(t, requires, architecture.Service{Unique: "vendor/stripe"}, "stage %s", stage)
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

	for _, stage := range resources.Stages() {
		restricted, err := dep.ForStage(stage)
		require.NoError(t, err, "stage %s", stage)
		order, err := restricted.OrderTo(ctx, frontend)
		require.NoError(t, err, "stage %s", stage)
		require.Equal(t, expected, order, "stage %s", stage)
	}

	// Every phase, including the composite ones, sees the same legacy order in
	// each of its stages.
	for _, phase := range resources.Phases() {
		stages, err := dep.OrderFor(ctx, phase, frontend)
		require.NoError(t, err, "phase %s", phase)
		require.NotEmpty(t, stages)
		for _, stage := range stages {
			require.Equal(t, expected, stage.Services, "phase %s stage %s", phase, stage.Stage)
		}
	}
}

func TestRestrictPreservesLookupAndEdgeKinds(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	run, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)
	restricted, err := run.Restrict(ctx, unique["worker"])
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

	build, err := dep.ForStage(resources.StageBuild)
	require.NoError(t, err)
	g := build.Graph()
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

func TestGraphCarriesKinds(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	kinds := make(map[string]string)
	for _, e := range dep.Graph().Edges() {
		kinds[e.From+" -> "+e.To] = e.Kind
	}
	require.Equal(t, graph.EdgeRuntime, kinds[unique["database"]+" -> "+unique["api"]])
	require.Equal(t, graph.EdgeSchema, kinds[unique["api"]+" -> "+unique["database"]])
	require.Equal(t, graph.EdgeCompletion, kinds[unique["migration"]+" -> "+unique["worker"]])
	require.Equal(t, graph.EdgeExternal, kinds["vendor/stripe -> "+unique["worker"]])
}

func TestLegacyDependenciesCarryDependsOnKind(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/flat-layout")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	for _, e := range dep.Graph().Edges() {
		require.Equal(t, graph.EdgeDependsOn, e.Kind, "%s -> %s", e.From, e.To)
	}
}

// A build input constrains the test and deploy that CONTAIN the build. When the
// kind table listed build inputs under PhaseBuild alone, api vanished from
// worker's test and deploy closures and the worker was tested and deployed
// against a stale generated client.
//
// Merging the stages into one graph is NOT the fix: the api/database pair has a
// build edge and a runtime edge pointing opposite ways, so the union is a cycle.
// The phase must decompose into ordered stages instead.
func TestBuildDependencyIsInTestAndDeployClosures(t *testing.T) {
	ctx := context.Background()
	workspace, unique := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	for _, phase := range []resources.Phase{resources.PhaseTest, resources.PhaseDeploy} {
		stages, err := dep.OrderFor(ctx, phase, unique["worker"])
		require.NoError(t, err, "phase %s must not report a false cycle", phase)
		require.Equal(t, []resources.Stage{resources.StageBuild, resources.StageRun},
			[]resources.Stage{stages[0].Stage, stages[1].Stage})

		require.Contains(t, stages[0].Services, architecture.Service{Unique: unique["api"]},
			"phase %s must build the worker's build input", phase)
		require.Contains(t, stages[1].Services, architecture.Service{Unique: unique["migration"]},
			"phase %s must still run the worker's runtime prerequisites", phase)
	}

	// The schema edge is the same shape: the bootstrap cannot be tested or
	// deployed without the contract it generates from.
	for _, phase := range []resources.Phase{resources.PhaseTest, resources.PhaseDeploy} {
		stages, err := dep.OrderFor(ctx, phase, unique["database"])
		require.NoError(t, err)
		require.Contains(t, stages[0].Services, architecture.Service{Unique: unique["api"]}, "phase %s", phase)
	}
}

func TestStageSelectionRejectsUnknownNames(t *testing.T) {
	ctx := context.Background()
	workspace, _ := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	// Returning an edgeless graph here is indistinguishable from "nothing
	// depends on anything", which would silently drop all ordering.
	_, err = dep.ForStage(resources.Stage("bulid"))
	require.ErrorContains(t, err, "unknown stage")

	_, err = dep.OrderFor(ctx, resources.Phase("tset"), "whatever")
	require.ErrorContains(t, err, "unknown phase")
}

func TestExternalProducerIsNotListedAsAService(t *testing.T) {
	ctx := context.Background()
	workspace, _ := phasesWorkspace(t)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	// Services() must not hand out an entry ServiceFromUnique cannot resolve.
	require.NotContains(t, dep.Services(), architecture.Service{Unique: "vendor/stripe"})
	_, err = dep.ServiceFromUnique("vendor/stripe")
	require.Error(t, err)

	// The declaration is still visible as an edge.
	require.True(t, dep.Graph().HasNode("vendor/stripe"))
}

func TestEveryDependencyKindMapsToADistinctEdgeKind(t *testing.T) {
	seen := map[string]resources.DependencyKind{}
	for _, kind := range resources.DeclarableDependencyKinds() {
		g := architecture.NewDAG("mapping")
		g.AddKindedEdge("a", "b", kind)
		edges := architecture.ToGraph(g, "mapping").Edges()
		require.Len(t, edges, 1)
		// An unmapped kind falls back to depends_on, which would silently
		// collapse a typed edge into an untyped one.
		require.NotEqual(t, graph.EdgeDependsOn, edges[0].Kind, "kind %q is not mapped", kind)
		require.NotContains(t, seen, edges[0].Kind, "kind %q collides with %q", kind, seen[edges[0].Kind])
		seen[edges[0].Kind] = kind
	}
}
