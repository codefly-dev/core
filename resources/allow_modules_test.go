package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func composed(module, name string, endpoints []*resources.Endpoint, dependencies ...*resources.ServiceDependency) *resources.Service {
	service := &resources.Service{Name: name, Version: "0.0.1", Endpoints: endpoints, ServiceDependencies: dependencies}
	service.WithModule(module)
	return service
}

func asks(module, name string, kind resources.DependencyKind, endpoints ...string) *resources.ServiceDependency {
	dependency := &resources.ServiceDependency{Module: module, Name: name, Kind: kind}
	for _, endpoint := range endpoints {
		dependency.Endpoints = append(dependency.Endpoints, &resources.EndpointReference{Name: endpoint})
	}
	return dependency
}

// The allow-list of an endpoint is the set of asks: every module whose
// services declare a run-stage dependency consuming it, the producer's own
// module included, each once, sorted — and nothing a host wrote about itself.
func TestDeriveAllowModulesIsTheSetOfAsks(t *testing.T) {
	host := composed("saas", "accounts", []*resources.Endpoint{
		{Name: "authority", API: "grpc", Visibility: resources.VisibilityInternal},
		{Name: "rest", API: "rest", Visibility: resources.VisibilityPrivate},
		{Name: "unasked", API: "http", Visibility: resources.VisibilityInternal},
	})
	services := []*resources.Service{
		host,
		// Names the endpoint it consumes.
		composed("wiki", "pages", nil, asks("saas", "accounts", resources.DependencyKindRuntime, "authority")),
		// A second service of the same module: the module is listed once.
		composed("wiki", "search", nil, asks("saas", "accounts", resources.DependencyKindRuntime, "authority")),
		// A legacy (untyped) edge constrains every stage, the run stage included.
		composed("billing", "ledger", nil, asks("saas", "accounts", resources.DependencyKindLegacy, "authority")),
		// The producer's own module asks for the private endpoint, as it may.
		composed("saas", "frontend", nil, asks("saas", "accounts", resources.DependencyKindRuntime, "rest")),
		// A build-time reader of the contract never calls the endpoint.
		composed("codegen", "schemas", nil, asks("saas", "accounts", resources.DependencyKindBuild, "authority")),
		// A dependency on a producer outside the composition reaches nothing here.
		composed("analytics", "reports", nil, asks("vendor", "stripe", resources.DependencyKindExternal)),
	}
	derived, err := resources.DeriveAllowModules(services)
	require.NoError(t, err)

	modules, declared := derived.Of("saas", "accounts", "authority")
	require.True(t, declared)
	require.Equal(t, []string{"billing", "wiki"}, modules, "the asks, sorted, each module once, no build edge")

	modules, declared = derived.Of("saas", "accounts", "rest")
	require.True(t, declared)
	require.Equal(t, []string{"saas"}, modules, "the owning module appears when one of its services asks")

	modules, declared = derived.Of("saas", "accounts", "unasked")
	require.True(t, declared)
	require.Empty(t, modules, "an endpoint nobody asks for grants nobody — internal names no module by itself")

	_, declared = derived.Of("saas", "accounts", "nope")
	require.False(t, declared, "an endpoint the composition does not declare is a different fact from one nobody asks for")
}

// An unnamed dependency consumes every endpoint it is permitted, so each of
// them records the ask.
func TestDeriveAllowModulesUnnamedDependencyAsksForAllItIsPermitted(t *testing.T) {
	host := composed("saas", "accounts", []*resources.Endpoint{
		{Name: "authority", API: "grpc", Visibility: resources.VisibilityInternal},
		{Name: "events", API: "http", Visibility: resources.VisibilityPublic},
		{Name: "rest", API: "rest", Visibility: resources.VisibilityPrivate},
	})
	derived, err := resources.DeriveAllowModules([]*resources.Service{
		host,
		composed("billing", "ledger", nil, asks("saas", "accounts", resources.DependencyKindRuntime)),
	})
	require.NoError(t, err)
	for _, endpoint := range []string{"authority", "events"} {
		modules, _ := derived.Of("saas", "accounts", endpoint)
		require.Equal(t, []string{"billing"}, modules, endpoint)
	}
	modules, _ := derived.Of("saas", "accounts", "rest")
	require.Empty(t, modules, "the private sibling is not consumed, so it is not asked for")
}

// A dependency the export boundary refuses is the same refusal the static
// validation makes: nothing is derived for a composition that does not
// validate, so the rendered list can never grant what the model denies.
func TestDeriveAllowModulesRefusesWhatTheBoundaryRefuses(t *testing.T) {
	host := composed("saas", "accounts", []*resources.Endpoint{{Name: "rest", API: "rest", Visibility: resources.VisibilityPrivate}})
	_, err := resources.DeriveAllowModules([]*resources.Service{
		host,
		composed("wiki", "pages", nil, asks("saas", "accounts", resources.DependencyKindRuntime, "rest")),
	})
	require.ErrorContains(t, err, `is private to module "saas"`)
	require.ErrorContains(t, err, "wiki/pages")

	// And an authored allow-list on the host is the invalid declaration it is
	// everywhere else, not an input to the derivation.
	authored := composed("saas", "accounts", []*resources.Endpoint{{Name: "authority", API: "grpc", Visibility: resources.VisibilityInternal, AllowModules: []string{"wiki"}}})
	_, err = resources.DeriveAllowModules([]*resources.Service{
		authored,
		composed("wiki", "pages", nil, asks("saas", "accounts", resources.DependencyKindRuntime, "authority")),
	})
	require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
	require.ErrorContains(t, err, "authors allow-modules")
}

// The derivation over a workspace on disk, read the way the composition reads
// it: the host declares internal and names nobody; the consumer in another
// module declares its dependency; the join is the list.
func TestDeriveAllowModulesOverAWorkspace(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/allowed-dependency-visibility")
	require.NoError(t, err)
	modules, err := workspace.LoadModules(ctx)
	require.NoError(t, err)
	var services []*resources.Service
	for _, mod := range modules {
		loaded, err := mod.LoadServices(ctx)
		require.NoError(t, err)
		services = append(services, loaded...)
	}
	derived, err := resources.DeriveAllowModules(services)
	require.NoError(t, err)
	internal, declared := derived.Of("saas", "gateway", "internal")
	require.True(t, declared)
	require.Equal(t, []string{"platform"}, internal, "the host's allow-list is the platform module's declared ask, which the host never wrote")
}
