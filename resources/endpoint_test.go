package resources_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
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

func loadMultipleGRPCEndpoints(t *testing.T) []*basev0.Endpoint {
	t.Helper()
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "testdata/workspaces/named-same-api-dependency/modules/saas/services/accounts")
	require.NoError(t, err)
	service.WithModule("saas")
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	return endpoints
}

func TestLoadEndpointsAllowsNamedSameAPIEndpoints(t *testing.T) {
	endpoints := loadMultipleGRPCEndpoints(t)
	require.Len(t, endpoints, 2)
	require.Equal(t, "grpc", endpoints[0].Name)
	require.Equal(t, resources.VisibilityPublic, endpoints[0].Visibility)
	require.Equal(t, "usage", endpoints[1].Name)
	require.Equal(t, resources.VisibilityModule, endpoints[1].Visibility)
	for _, endpoint := range endpoints {
		require.Equal(t, standards.GRPC, endpoint.Api)
		require.Equal(t, "accounts.v1", resources.IsGRPC(context.Background(), endpoint).Package)
	}
}

func TestServiceRejectsDuplicateEndpointNames(t *testing.T) {
	service := &resources.Service{
		Name: "accounts",
		Endpoints: []*resources.Endpoint{
			{Name: "usage", API: standards.GRPC},
			{Name: "usage", API: standards.REST},
		},
	}
	err := service.SaveAtDir(context.Background(), t.TempDir())
	require.ErrorContains(t, err, `duplicate endpoint name "usage"`)
}

func TestFindGRPCEndpointRejectsAmbiguousAPIResolution(t *testing.T) {
	_, err := resources.FindGRPCEndpoint(context.Background(), loadMultipleGRPCEndpoints(t))
	require.ErrorContains(t, err, "multiple grpc endpoints found")
	require.ErrorContains(t, err, "specify an endpoint name")
}

func TestFindGRPCEndpointFromServiceResolvesDeclaredName(t *testing.T) {
	dependency := &resources.ServiceDependency{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "usage"}},
	}
	endpoint, err := resources.FindGRPCEndpointFromService(context.Background(), dependency, loadMultipleGRPCEndpoints(t))
	require.NoError(t, err)
	require.Equal(t, "usage", endpoint.Name)
}

func TestValidateDependencyEndpointsRejectsAmbiguousAndUndeclaredReferences(t *testing.T) {
	endpoints := loadMultipleGRPCEndpoints(t)

	err := resources.ValidateDependencyEndpoints([]*resources.ServiceDependency{{Module: "saas", Name: "accounts"}}, endpoints)
	require.ErrorContains(t, err, "multiple grpc endpoints")

	err = resources.ValidateDependencyEndpoints([]*resources.ServiceDependency{{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "missing"}},
	}}, endpoints)
	require.ErrorContains(t, err, `declares undeclared endpoint "missing"`)
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
