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

// A service reaching a producer only through a workspace configuration group
// is ordered after it, and its run includes it, exactly as for a declared
// dependency; without the option nothing changes.
func TestConfigurationReferencesOrderTheConsumerAfterTheProducer(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/configuration-references")
	require.NoError(t, err)
	unique := make(map[string]string)
	for _, name := range []string{"host", "worker", "gateway"} {
		unique[name] = shared.Must(workspace.FindUniqueServiceByName(ctx, name)).MustUnique()
	}

	plain, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	order, err := plain.OrderTo(ctx, unique["worker"])
	require.NoError(t, err)
	require.Empty(t, order)

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.WithConfigurationReferences(map[string][]string{
		"platform": {unique["host"], unique["worker"], "elsewhere/absent"},
	}))
	require.NoError(t, err)
	require.NoError(t, dep.VerifyAcyclic(ctx))
	run, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)
	order, err = run.OrderTo(ctx, unique["worker"])
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: unique["host"]}}, order,
		"the referenced producer runs first; the consumer's own name and a producer outside the workspace add nothing")
}

// A reference that would close a cycle with a declared dependency is skipped:
// gateway depends on worker, so worker's group naming gateway adds no edge.
func TestConfigurationReferencesNeverCloseACycle(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/configuration-references")
	require.NoError(t, err)
	gateway := shared.Must(workspace.FindUniqueServiceByName(ctx, "gateway")).MustUnique()
	worker := shared.Must(workspace.FindUniqueServiceByName(ctx, "worker")).MustUnique()

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.WithConfigurationReferences(map[string][]string{
		"platform": {gateway + "/grpc"},
	}))
	require.NoError(t, err)
	require.NoError(t, dep.VerifyAcyclic(ctx))
	order, err := dep.OrderTo(ctx, gateway)
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: worker}}, order)

	// And nothing to wait for either: the edge was dropped because gateway is
	// already waiting for worker, so a readiness requirement the other way would
	// be that same deadlock in the one place ordering cannot show it.
	require.Empty(t, dep.ConfigurationReferenceDependencies(worker),
		"a reference that orders nothing gates nothing")
}

// A `kind: external` declaration names the producer but orders nothing, so it
// must not stand in for the reference: relay declares host as external and
// reaches it through the `platform` group, and the run starts host first. The
// declaration alone orders nothing, as before.
func TestConfigurationReferencesOrderAConsumerDeclaringTheProducerExternal(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/configuration-references")
	require.NoError(t, err)
	host := shared.Must(workspace.FindUniqueServiceByName(ctx, "host")).MustUnique()
	relay := shared.Must(workspace.FindUniqueServiceByName(ctx, "relay")).MustUnique()

	plain, err := architecture.NewServiceDependencies(ctx, workspace)
	require.NoError(t, err)
	plainRun, err := plain.ForStage(resources.StageRun)
	require.NoError(t, err)
	order, err := plainRun.OrderTo(ctx, relay)
	require.NoError(t, err)
	require.Empty(t, order, "an external declaration alone never starts or orders the producer")

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.WithConfigurationReferences(map[string][]string{
		"platform": {host + "/grpc"},
	}))
	require.NoError(t, err)
	require.NoError(t, dep.VerifyAcyclic(ctx))
	run, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)
	order, err = run.OrderTo(ctx, relay)
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: host}}, order,
		"the referenced producer runs first even though the consumer declares it external")

	// The reference supersedes the `external` declaration of the same pair, so the
	// edge carries one kind and not two that contradict each other — which showed
	// up as two parallel edges, labelled `external` and `runtime`, between the same
	// two services.
	var kinds []string
	for _, edge := range dep.Graph().OutEdges(host) {
		if edge.To == relay {
			kinds = append(kinds, edge.Kind)
		}
	}
	require.Equal(t, []string{graph.EdgeRuntime}, kinds,
		"the superseded external kind is taken off the edge")
}

// Ordering a producer is only half of what a runtime dependency means: the
// consumer must also wait for the endpoint it is about to call. The reference
// carries the endpoint, so it plans exactly like a declared runtime dependency —
// where the `external` declaration on its own waits for nothing, which is what
// would have let relay start before host could serve.
func TestConfigurationReferencesGateReadinessOnTheReferencedEndpoint(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/configuration-references")
	require.NoError(t, err)
	hostService := shared.Must(workspace.FindUniqueServiceByName(ctx, "host"))
	relayService := shared.Must(workspace.FindUniqueServiceByName(ctx, "relay"))
	host, relay := hostService.MustUnique(), relayService.MustUnique()
	hostEndpoints, err := hostService.DependencyEndpoints()
	require.NoError(t, err)

	declared, err := resources.PlanReadiness(relayService.ServiceDependencies, hostEndpoints)
	require.NoError(t, err)
	require.Empty(t, declared, "an external declaration waits for nothing, by itself")

	dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.WithConfigurationReferences(map[string][]string{
		"platform": {host + "/grpc"},
	}))
	require.NoError(t, err)
	referenced := dep.ConfigurationReferenceDependencies(relay)
	require.Len(t, referenced, 1)
	require.Equal(t, host, referenced[0].Unique())
	require.Equal(t, resources.DependencyKindRuntime, referenced[0].Kind)

	planned, err := resources.PlanReadiness(append(relayService.ServiceDependencies, referenced...), hostEndpoints)
	require.NoError(t, err)
	require.Len(t, planned, 1, "the referenced endpoint is waited for")
	require.Equal(t, host, planned[0].Dependency)
	require.Equal(t, "grpc", planned[0].Endpoint)
	require.Equal(t, resources.PrerequisiteEndpointHealth, resources.DependencyKindRuntime.Prerequisite())

	// A reference naming the endpoint by its API resolves to the producer's own
	// name for it, and the same endpoint referenced twice is waited for once.
	twice, err := architecture.NewServiceDependencies(ctx, workspace, architecture.WithConfigurationReferences(map[string][]string{
		"platform": {host + "/grpc", host + "::grpc", host},
	}))
	require.NoError(t, err)
	referenced = twice.ConfigurationReferenceDependencies(relay)
	require.Len(t, referenced, 1)
	require.Len(t, referenced[0].Endpoints, 1, "one endpoint, however many references name it")
	require.Equal(t, "grpc", referenced[0].Endpoints[0].Name)
}
