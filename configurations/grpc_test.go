package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestGRPCLoading(t *testing.T) {
	ctx := context.Background()
	r, err := configurations.LoadGRPCRoute(ctx, "testdata/grpcs/app/svc/Version.grpc.codefly.yaml")
	assert.NoError(t, err)
	assert.Equal(t, "Version", r.Name)
}

type extendedGRPCRoute = configurations.ExtendedGRPCRoute[Auth]

func TestGRPCExtendedLoading(t *testing.T) {
	ctx := context.Background()
	r, err := configurations.LoadExtendedGRPCRoute[Auth](ctx, "testdata/grpcs/app/svc/Version.grpc.codefly.yaml")
	assert.NoError(t, err)
	assert.Equal(t, "Version", r.Name)
	assert.Equal(t, "working", r.Extension.Protected)
}

func TestGRPCRouteLoader(t *testing.T) {
	loader, err := configurations.NewGRPCRouteLoader(context.Background(), "testdata/grpcs")
	assert.NoError(t, err)
	assert.NotNil(t, loader)
	err = loader.Load(context.Background())
	assert.NoError(t, err)
	routes := loader.All()
	assert.Equal(t, 1, len(routes))

}
