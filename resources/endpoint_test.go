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

func TestEndpointVisibilityInterpretation(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/visibility")
	require.NoError(t, err)

	// Deprecated "module" is preserved on the model (not rewritten) and
	// interpreted as reachable from every module.
	grpc := endpointByName(service.Endpoints, "grpc")
	require.Equal(t, resources.VisibilityModule, grpc.Visibility)
	require.True(t, grpc.AllowsModule("anything"))

	// Deprecated "external" is preserved and treated as external + reachable.
	rest := endpointByName(service.Endpoints, "rest")
	require.Equal(t, resources.VisibilityExternal, rest.Visibility)
	require.True(t, rest.External())
	require.True(t, rest.AllowsModule("anything"))

	// Explicit internal keeps its allow-list and is enforced.
	http := endpointByName(service.Endpoints, "http")
	require.Equal(t, resources.VisibilityInternal, http.Visibility)
	require.Equal(t, []string{"platform"}, http.AllowModules)
	require.True(t, http.AllowsModule("platform"))
	require.False(t, http.AllowsModule("web"))

	// Unset stays private.
	tcp := endpointByName(service.Endpoints, "tcp")
	require.Equal(t, resources.VisibilityPrivate, tcp.Visibility)
	require.False(t, tcp.External())
}

// TestEndpointVisibilityRoundTrip guards against silently migrating a user's
// authored visibility on save: loading and saving must leave the deprecated
// values exactly as written.
func TestEndpointVisibilityRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "vault")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	yaml := "kind: service\nname: vault\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nendpoints:\n  - name: http\n    api: http\n    visibility: module\n"
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.codefly.yaml"), []byte(yaml), 0o644))

	service, err := resources.LoadServiceFromDir(ctx, svcDir)
	require.NoError(t, err)
	require.NoError(t, service.Save(ctx))

	saved, err := os.ReadFile(filepath.Join(svcDir, "service.codefly.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(saved), "visibility: module")
	require.NotContains(t, string(saved), "visibility: internal")
	require.NotContains(t, string(saved), "allow-modules")
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
