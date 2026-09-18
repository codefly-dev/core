package architecture_test

import (
	"context"
	"strings"
	"testing"

	"github.com/codefly-dev/core/architecture"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestVerifyVisibilityAllowed(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-allowed")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	require.NoError(t, dep.VerifyVisibility(ctx))
}

func TestVerifyVisibilityDenied(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-denied")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	err = dep.VerifyVisibility(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/portal")
	require.Contains(t, err.Error(), "vault/secrets")
}

func TestVerifyVisibilityUnknownEndpoint(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-unknown")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	// The consumer is allow-listed, but it references an endpoint that does not
	// exist on the target: verify must surface it, not silently ignore it.
	err = dep.VerifyVisibility(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nope")
}

func TestVerifyVisibilityDeniedForBuildDependency(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-denied")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	// web/builder consumes the same endpoint as web/portal but declares a build
	// kind. A build-time consumer crosses the same export boundary as a runtime
	// one: declaring a kind must not buy a way out of visibility enforcement.
	err = dep.VerifyVisibility(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/builder")
	require.Contains(t, err.Error(), "vault/secrets")
}

// A module that declares an interface exports only what it lists. api/two is
// public on the service and absent from the interface, so the graph excludes it
// and verify has to refuse the module that consumes it — otherwise the export
// boundary narrows the graph while permitting the edge anyway.
func TestVerifyVisibilityDeniedForEndpointOutsideInterface(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/interface-declared")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	err = dep.VerifyVisibility(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "api/two")
}

// The build edge is judged by the stage that traverses it. web/builder reads
// vault/secrets at build time, so restricting the view to the run stage leaves
// that edge to the build stage rather than failing a run over it, while
// web/portal's untyped edge constrains both and still fails.
func TestVerifyVisibilityScopesToTheRestrictedStage(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-denied")
	require.NoError(t, err)

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)

	run, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)
	err = run.VerifyVisibility(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/portal")
	require.NotContains(t, err.Error(), "web/builder")

	build, err := dep.ForStage(resources.StageBuild)
	require.NoError(t, err)
	err = build.VerifyVisibility(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/builder")
}

// Closure.Verify is reached by Plan, so the phase the plan was asked for is
// what decides whether a build input's edge is judged. Judging it whatever the
// phase is what made the executable-plan path refuse a run over an edge no run
// traverses.
func TestClosureVerifyScopesVisibilityToThePhase(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/visibility-denied")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "web/builder")
	require.NoError(t, err)

	require.NoError(t, closure.Verify(ctx, resources.PhaseRun))

	err = closure.Verify(ctx, resources.PhaseBuild)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web/builder")
	require.Contains(t, err.Error(), "vault/secrets")
}

// A dependency that names no endpoint consumes all it is permitted, and every
// check has to say so: the static workspace pass, the stage closure, the graph
// verify and the addresses a run hands the consumer. consumer/app never
// enumerates, and producer/api keeps "admin" to itself.
func TestVisibilityChecksAgreeOnConsumingAll(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/consumes-all-visibility")
	require.NoError(t, err)

	require.NoError(t, workspace.ValidateServiceDependencies(ctx))

	closure, err := workspace.ResolveModuleClosure(ctx, resources.StageRun, []string{"consumer"})
	require.NoError(t, err)
	require.NoError(t, closure.ValidateServiceDependencies(ctx))

	dep, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	require.NoError(t, dep.VerifyVisibility(ctx))

	consumer, err := dep.ServiceFromUnique("consumer/app")
	require.NoError(t, err)
	producer, err := dep.ServiceFromUnique("producer/api")
	require.NoError(t, err)
	endpoints, err := producer.DependencyEndpoints()
	require.NoError(t, err)
	mappings := make([]*basev0.NetworkMapping, 0, len(endpoints))
	for _, endpoint := range endpoints {
		mappings = append(mappings, &basev0.NetworkMapping{Endpoint: endpoint})
	}
	resolved, err := resources.ResolveDependencyNetworkMappings("consumer", consumer.ServiceDependencies, mappings)
	require.NoError(t, err)
	names := make([]string, 0, len(resolved))
	for _, mapping := range resolved {
		names = append(names, mapping.GetEndpoint().GetName())
	}
	require.Equal(t, []string{"http"}, names)
}

// The plan says what a consumer receives, so it has to be the set the run
// resolves: recording an origin for an endpoint the consumer is not permitted
// would put a key in the plan that never reaches its environment.
func TestPlanCarriesOnlyThePermittedConsumedEndpoints(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/consumes-all-visibility")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "consumer/app")
	require.NoError(t, err)
	plan, err := closure.Plan(ctx, runOptions())
	require.NoError(t, err)

	var keys []string
	for _, origin := range plan.Configurations {
		if origin.Consumer == "consumer/app" {
			keys = append(keys, origin.Key)
		}
	}
	require.Len(t, keys, 1)
	require.Contains(t, keys[0], "HTTP")
	require.NotContains(t, strings.Join(keys, " "), "ADMIN")

	for _, node := range plan.Nodes {
		if node.ID != "producer/api" {
			continue
		}
		required := make([]string, 0, len(node.Endpoints))
		for _, endpoint := range node.Endpoints {
			required = append(required, endpoint.Name)
		}
		require.Equal(t, []string{"http"}, required)
	}
}
