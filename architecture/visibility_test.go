package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
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
