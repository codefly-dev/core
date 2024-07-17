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
	service.WithModule("mod")
	endpoints, err := service.LoadEndpoints(ctx)

	require.NoError(t, err)
	require.Equal(t, 2, len(endpoints))
}

func TestEnvironmentVariables(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	require.NoError(t, err)
	service.WithModule("mod")

	// Endpoints require a complete identification
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(endpoints))

	instance := resources.NewNetworkInstance("localhost", 8080)

	rest, err := resources.FindRestEndpoint(ctx, endpoints)
	require.NoError(t, err)

	env := resources.EndpointAsEnvironmentVariable(rest, instance)
	require.Equal(t, fmt.Sprintf("CODEFLY__ENDPOINT__MOD__ORGANIZATION__REST__REST=%s", instance.Address), env.String())

}
