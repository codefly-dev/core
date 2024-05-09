package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/stretchr/testify/require"
)

func TestREST(t *testing.T) {
	ctx := context.Background()
	rest, err := resources.LoadRestAPI(ctx, shared.Pointer("testdata/endpoints/basic/openapi/api.json"))
	require.NoError(t, err)
	require.Equal(t, 2, len(rest.Groups)) // 2 Paths
	var routes []*basev0.RestRoute
	for _, group := range rest.Groups {
		routes = append(routes, group.Routes...)
	}
	require.Equal(t, 3, len(routes)) // 3 Routes (1 path with 2 Methods)
}

func TestGRPC(t *testing.T) {
	ctx := context.Background()
	grpc, err := resources.LoadGrpcAPI(ctx, shared.Pointer("testdata/endpoints/basic/proto/api.proto"))
	require.NoError(t, err)
	require.Equal(t, "management.organization", grpc.Package)
	require.Equal(t, 4, len(grpc.Rpcs)) // 4 RPCs
	for _, rpc := range grpc.Rpcs {
		require.Equal(t, "OrganizationService", rpc.ServiceName)
	}
}
