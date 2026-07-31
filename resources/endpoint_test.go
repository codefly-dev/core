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

func endpointByName(endpoints []*resources.Endpoint, name string) *resources.Endpoint {
	for _, ep := range endpoints {
		if ep.Name == name {
			return ep
		}
	}
	return nil
}

func TestEndpointVisibilityNormalization(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/visibility")
	require.NoError(t, err)

	// "module" aliases to internal with a wildcard allow-list.
	grpc := endpointByName(service.Endpoints, "grpc")
	require.Equal(t, resources.VisibilityInternal, grpc.Visibility)
	require.Equal(t, []string{resources.AllowAllModules}, grpc.AllowModules)

	// "external" moves onto the location axis, keeping permissive reachability.
	rest := endpointByName(service.Endpoints, "rest")
	require.Equal(t, resources.VisibilityInternal, rest.Visibility)
	require.Equal(t, resources.LocationExternal, rest.Location)
	require.True(t, rest.External())
	require.Equal(t, []string{resources.AllowAllModules}, rest.AllowModules)

	// Explicit internal keeps its allow-list untouched.
	http := endpointByName(service.Endpoints, "http")
	require.Equal(t, resources.VisibilityInternal, http.Visibility)
	require.Equal(t, []string{"platform"}, http.AllowModules)

	// Unset stays private.
	tcp := endpointByName(service.Endpoints, "tcp")
	require.Equal(t, resources.VisibilityPrivate, tcp.Visibility)
	require.False(t, tcp.External())
}

func TestEndpointAllowsModule(t *testing.T) {
	private := &resources.Endpoint{Module: "vault", Visibility: resources.VisibilityPrivate}
	require.True(t, private.AllowsModule("vault"))
	require.False(t, private.AllowsModule("platform"))

	public := &resources.Endpoint{Module: "vault", Visibility: resources.VisibilityPublic}
	require.True(t, public.AllowsModule("platform"))

	internal := &resources.Endpoint{Module: "vault", Visibility: resources.VisibilityInternal, AllowModules: []string{"platform"}}
	require.True(t, internal.AllowsModule("vault"))
	require.True(t, internal.AllowsModule("platform"))
	require.False(t, internal.AllowsModule("web"))

	wildcard := &resources.Endpoint{Module: "vault", Visibility: resources.VisibilityInternal, AllowModules: []string{resources.AllowAllModules}}
	require.True(t, wildcard.AllowsModule("web"))

	empty := &resources.Endpoint{Module: "vault", Visibility: resources.VisibilityInternal}
	require.False(t, empty.AllowsModule("platform"))
}
