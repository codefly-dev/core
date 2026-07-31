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
