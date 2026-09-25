package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
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
		"platform": {gateway},
	}))
	require.NoError(t, err)
	require.NoError(t, dep.VerifyAcyclic(ctx))
	order, err := dep.OrderTo(ctx, gateway)
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: worker}}, order)
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
		"platform": {host},
	}))
	require.NoError(t, err)
	require.NoError(t, dep.VerifyAcyclic(ctx))
	run, err := dep.ForStage(resources.StageRun)
	require.NoError(t, err)
	order, err = run.OrderTo(ctx, relay)
	require.NoError(t, err)
	require.Equal(t, []architecture.Service{{Unique: host}}, order,
		"the referenced producer runs first even though the consumer declares it external")
}
