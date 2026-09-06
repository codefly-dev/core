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

// A reference carrying an explicit coordinate (here a path override) identifies
// the module by that coordinate, so its name is a workspace-local alias and the
// module's own declared name may differ — a module renamed at its source must
// still resolve for consumers whose coordinate still points at it. The composed
// module and its services then answer to the alias, not the declared name, so
// dependency wiring keys off the handle the workspace believes it composed.
func TestWorkspaceComposesCoordinateModuleUnderAlias(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/out-of-repo/name-mismatch-solution")
	require.NoError(t, err)

	mod, err := workspace.LoadModuleFromName(ctx, "aliased")
	require.NoError(t, err)
	require.Equal(t, "aliased", mod.Name)

	svc, err := mod.LoadServiceFromName(ctx, "api")
	require.NoError(t, err)
	identity, err := svc.Identity()
	require.NoError(t, err)
	require.Equal(t, "aliased", identity.Module)
}

// A bare-name reference locates its module purely by that name (the in-repo
// layout default), so a module declaring a different name is a genuine
// inconsistency the workspace cannot key off coherently. Loading it must fail
// loudly rather than return a module answering to an unexpected name.
func TestWorkspaceRejectsBareNameModuleWithMismatchedName(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/bare-name-mismatch")
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "foo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bar")
	require.Contains(t, err.Error(), "foo")
}
