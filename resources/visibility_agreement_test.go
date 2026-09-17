package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// runtimeMappingsFor builds the mappings a run hands a consumer: the producer's
// endpoints as its agent reports them from its own configuration, wrapped one
// per mapping. Going through Service.LoadEndpoints rather than hand-writing the
// protos is the point — a divergence in the visibility the runtime observes
// would show up here and nowhere else.
func runtimeMappingsFor(ctx context.Context, t *testing.T, workspaceDir, module, service string) []*basev0.NetworkMapping {
	t.Helper()
	endpoints, err := loadService(ctx, t, workspaceDir, module, service).LoadEndpoints(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, endpoints, "producer reported no endpoints")
	mappings := make([]*basev0.NetworkMapping, 0, len(endpoints))
	for _, endpoint := range endpoints {
		mappings = append(mappings, &basev0.NetworkMapping{Endpoint: endpoint})
	}
	return mappings
}

func loadService(ctx context.Context, t *testing.T, workspaceDir, module, service string) *resources.Service {
	t.Helper()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, workspaceDir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, module)
	require.NoError(t, err)
	svc, err := mod.LoadServiceFromName(ctx, service)
	require.NoError(t, err)
	return svc
}

// The static pass and a run must reach the same verdict on the same edge. An
// endpoint left private by a producer that declares nothing is the case that
// diverged: validation refused it while a run resolved it and came up, because
// nothing on the run path was asking.
func TestStaticAndRuntimeAgreeOnDeniedEdge(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/denied-dependency-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	staticErr := workspace.ValidateServiceDependencies(ctx)
	require.Error(t, staticErr, "static pass must refuse a cross-module dependency on a private endpoint")

	consumer := loadService(ctx, t, dir, "platform", "api")
	_, runtimeErr := resources.ResolveDependencyNetworkMappings(
		"platform",
		consumer.ServiceDependencies,
		runtimeMappingsFor(ctx, t, dir, "saas", "accounts"),
	)
	require.Error(t, runtimeErr, "a run must refuse the edge the static pass refused; static said: %v", staticErr)
	require.Contains(t, runtimeErr.Error(), "private to module \"saas\"")
}

// The agreement has to hold in the permitting direction too, or the runtime
// check would be trivially satisfied by rejecting everything.
func TestStaticAndRuntimeAgreeOnAllowedEdge(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/allowed-dependency-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx), "static pass must permit the edge")

	consumer := loadService(ctx, t, dir, "platform", "api")
	resolved, err := resources.ResolveDependencyNetworkMappings(
		"platform",
		consumer.ServiceDependencies,
		runtimeMappingsFor(ctx, t, dir, "saas", "gateway"),
	)
	require.NoError(t, err, "a run must permit the edge the static pass permitted")
	// What resolves is what the consumer's SDK exposes, so the permitted set
	// and the exposed set have to be the same two endpoints.
	names := make([]string, 0, len(resolved))
	for _, mapping := range resolved {
		names = append(names, mapping.GetEndpoint().GetName())
	}
	require.ElementsMatch(t, []string{"public", "internal"}, names)
}
