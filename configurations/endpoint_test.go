package configurations_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestLoadEndpoints(t *testing.T) {
	ctx := context.Background()
	service, err := configurations.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	assert.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(endpoints))
}

//
//func TestEndpointSwaggerChange(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Application: "app", Service: "svc", Name: "rest"}
//	endpoint.WithDefault()
//
//	e, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/original.swagger.json")
//	assert.NoError(t, err)
//	hash, err := configurations.EndpointHash(ctx, e)
//	assert.NoError(t, err)
//
//	// Removed path swagger
//	e, err = configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/path_removed.swagger.json")
//	assert.NoError(t, err)
//	updatedHash, err := configurations.EndpointHash(ctx, e)
//	assert.NoError(t, err)
//	assert.NotEqual(t, hash, updatedHash)
//
//	// Changed path swagger
//	e, err = configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/path_name_changed.swagger.json")
//	assert.NoError(t, err)
//	updatedHash, err = configurations.EndpointHash(ctx, e)
//	assert.NoError(t, err)
//	assert.NotEqual(t, hash, updatedHash)
//}

func TestEnvironmentVariables(t *testing.T) {
	ctx := context.Background()
	service, err := configurations.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	assert.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(endpoints))

	instance := configurations.NewNetworkInstance("localhost", 8080)
	encoded := configurations.EncodeValue(instance.Address)

	rest, err := configurations.FindRestEndpoint(ctx, endpoints)
	assert.NoError(t, err)

	env := configurations.EndpointAsEnvironmentVariable(rest, instance)
	assert.Equal(t, fmt.Sprintf("CODEFLY__ENDPOINT__MANAGEMENT__ORGANIZATION__REST__REST=%s", encoded), env)

}
