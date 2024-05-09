package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestGRPCLoading(t *testing.T) {
	ctx := context.Background()
	r, err := resources.LoadGRPCRoute(ctx, "testdata/grpcs/app/svc/Version.grpc.codefly.yaml")
	require.NoError(t, err)
	require.Equal(t, "Version", r.Name)
}

type extendedGRPCRoute = resources.ExtendedGRPCRoute[Auth]

func TestGRPCExtendedLoading(t *testing.T) {
	ctx := context.Background()
	r, err := resources.LoadExtendedGRPCRoute[Auth](ctx, "testdata/grpcs/app/svc/Version.grpc.codefly.yaml")
	require.NoError(t, err)
	require.Equal(t, "Version", r.Name)
	require.Equal(t, "working", r.Extension.Protected)
}

func TestGRPCRouteLoader(t *testing.T) {
	loader, err := resources.NewGRPCRouteLoader(context.Background(), "testdata/grpcs")
	require.NoError(t, err)
	require.NotNil(t, loader)
	err = loader.Load(context.Background())
	require.NoError(t, err)
	routes := loader.All()
	require.Equal(t, 1, len(routes))

}
