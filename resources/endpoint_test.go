package resources_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
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

func TestLoadEndpointsPrefersDependencyContract(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "proto", "codefly"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, standards.ProtoPath), []byte(`syntax = "proto3"; package legacy;`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, standards.DependencyProtoPath), []byte(`syntax = "proto3"; package current.v1; service IdentityService { rpc Resolve(ResolveRequest) returns (ResolveResponse); } message ResolveRequest {} message ResolveResponse {}`), 0o644))

	service := &resources.Service{
		Name: "accounts",
		Endpoints: []*resources.Endpoint{{
			Name:       standards.GRPC,
			Service:    "accounts",
			Visibility: resources.VisibilityPrivate,
			API:        standards.GRPC,
		}},
	}
	service.WithDir(dir)
	service.WithModule("saas")
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	require.Equal(t, "current.v1", resources.IsGRPC(ctx, endpoints[0]).Package)
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

	env := resources.EndpointAsEnvironmentVariable(&resources.EndpointAccess{Endpoint: rest, NetworkInstance: instance})
	require.Equal(t, fmt.Sprintf("CODEFLY__ENDPOINT__MOD__ORGANIZATION__REST__REST=%s", instance.Address), env.String())

}
