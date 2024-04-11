package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/shared"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
)

func TestREST(t *testing.T) {
	ctx := context.Background()
	rest, err := configurations.LoadRestAPI(ctx, shared.Pointer("testdata/endpoints/basic/openapi/api.json"))
	assert.NoError(t, err)
	assert.Equal(t, 2, len(rest.Groups)) // 2 Paths
	var routes []*basev0.RestRoute
	for _, group := range rest.Groups {
		routes = append(routes, group.Routes...)
	}
	assert.Equal(t, 3, len(routes)) // 3 Routes (1 path with 2 Methods)
}

func TestGRPC(t *testing.T) {
	ctx := context.Background()
	grpc, err := configurations.LoadGrpcAPI(ctx, shared.Pointer("testdata/endpoints/basic/proto/api.proto"))
	assert.NoError(t, err)
	assert.Equal(t, "management.organization", grpc.Package)
	assert.Equal(t, 4, len(grpc.Rpcs)) // 4 RPCs
	for _, rpc := range grpc.Rpcs {
		assert.Equal(t, "OrganizationService", rpc.ServiceName)
	}
}
