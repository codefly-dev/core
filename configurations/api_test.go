package configurations_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
)

func TestREST(t *testing.T) {
	ctx := context.Background()
	endpoint := &configurations.Endpoint{Application: "app", Service: "svc", Name: "rest"}
	endpoint.WithDefault()
	e, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/one/org.swagger.json")
	assert.NoError(t, err)
	rest := configurations.EndpointRestAPI(e)
	assert.NotNil(t, rest)
	assert.Equal(t, 2, len(rest.Groups)) // 2 Paths
	var routes []*basev0.RestRoute
	for _, group := range rest.Groups {
		routes = append(routes, group.Routes...)
	}
	assert.Equal(t, 3, len(routes)) // 3 Routes (1 path with 2 Methods)
}

func TestGRPC(t *testing.T) {
	ctx := context.Background()
	endpoint := &configurations.Endpoint{Application: "app", Service: "svc", Name: "gprc"}
	endpoint.WithDefault()
	e, err := configurations.NewGrpcAPI(ctx, endpoint, "testdata/api/grpc/api.proto")
	assert.NoError(t, err)
	grpc := configurations.EndpointGRPCAPI(e)
	assert.NotNil(t, grpc)
	assert.Equal(t, "management.organization", grpc.Package)
	assert.Equal(t, 4, len(grpc.Rpcs)) // 4 RPCs
	for _, rpc := range grpc.Rpcs {
		assert.Equal(t, "OrganizationService", rpc.ServiceName)
	}
}
