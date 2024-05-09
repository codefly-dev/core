package resources_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestLoadEndpoints(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	require.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(endpoints))
}

//
//func TestEndpointSwaggerChange(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Module: "app", Service: "svc", Name: "rest"}
//	endpoint.WithDefault()
//
//	e, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/original.swagger.json")
//require.NoError(t, err)
//	hash, err := configurations.EndpointHash(ctx, e)
//require.NoError(t, err)
//
//	// Removed path swagger
//	e, err = configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/path_removed.swagger.json")
//require.NoError(t, err)
//	updatedHash, err := configurations.EndpointHash(ctx, e)
//require.NoError(t, err)
//	require.NotEqual(t, hash, updatedHash)
//
//	// Changed path swagger
//	e, err = configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/path_name_changed.swagger.json")
//require.NoError(t, err)
//	updatedHash, err = configurations.EndpointHash(ctx, e)
//require.NoError(t, err)
//	require.NotEqual(t, hash, updatedHash)
//}

func TestEnvironmentVariables(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	require.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(endpoints))

	instance := resources.NewNetworkInstance("localhost", 8080)

	rest, err := resources.FindRestEndpoint(ctx, endpoints)
	require.NoError(t, err)

	env := resources.EndpointAsEnvironmentVariable(rest, instance)
	require.Equal(t, fmt.Sprintf("CODEFLY__ENDPOINT__MANAGEMENT__ORGANIZATION__REST__REST=%s", instance.Address), env.String())

}
