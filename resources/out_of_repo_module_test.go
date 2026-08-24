package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// A composition-root workspace references an out-of-repo module by relative
// path (a sibling checkout, not a vendored copy). Its services must load into
// the graph and wire across the reference boundary like in-repo modules.
func TestWorkspaceReferencesOutOfRepoModuleByPath(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/out-of-repo/solution")
	require.NoError(t, err)

	// Portability is the whole point: the committed reference is a relative
	// sibling path, not an absolute machine path. ModulePath joins it onto the
	// workspace dir, so it resolves the same regardless of invocation cwd.
	var saasRef *resources.ModuleReference
	for _, ref := range workspace.Modules {
		if ref.Name == "saas" {
			saasRef = ref
		}
	}
	require.NotNil(t, saasRef)
	require.NotNil(t, saasRef.PathOverride)
	require.Equal(t, "../host", *saasRef.PathOverride)

	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Len(t, gateway.Endpoints, 1)
	require.Equal(t, "public-api", gateway.Endpoints[0].Name)

	services, err := workspace.LoadServices(ctx)
	require.NoError(t, err)
	var names []string
	for _, svc := range services {
		names = append(names, svc.Name)
	}
	require.Contains(t, names, "gateway")
	require.Contains(t, names, "api")

	// The in-repo consumer depends on the out-of-repo producer's public
	// endpoint; visibility wiring resolves across the reference boundary.
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}
