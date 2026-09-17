package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A declared interface is the module's export boundary, so an endpoint it omits
// is not consumable across module lines however the service declares it. The
// module graph has always excluded such an endpoint; a check that still
// permitted the edge would narrow the graph and authorize what it left out.
func TestInterfaceOmittedEndpointIsNotConsumable(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-omits-endpoint-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	staticErr := workspace.ValidateServiceDependencies(ctx)
	require.Error(t, staticErr, "an endpoint the interface omits must not be consumable across modules")
	require.Contains(t, staticErr.Error(), "omitted")

	consumer := loadService(ctx, t, dir, "platform", "api")
	_, runtimeErr := resources.ResolveDependencyNetworkMappings(
		"platform",
		consumer.ServiceDependencies,
		runtimeMappingsFor(ctx, t, dir, "saas", "gateway"),
	)
	require.Error(t, runtimeErr, "a run must refuse the edge the static pass refused; static said: %v", staticErr)
}

// The interface entry is the export declaration on its own: a service that keeps
// an endpoint private to itself and a module that exports it are not in
// disagreement, so the fact is stated once.
func TestInterfaceExportsEndpointThePrivateServiceKeeps(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx),
		"the interface exports accounts/grpc and gateway/restricted, whatever the services declare")

	consumer := loadService(ctx, t, dir, "platform", "api")
	resolved, err := resources.ResolveDependencyNetworkMappings(
		"platform",
		consumer.ServiceDependencies,
		runtimeMappingsFor(ctx, t, dir, "saas", "gateway"),
	)
	require.NoError(t, err, "a run must permit the edge the static pass permitted")
	names := make([]string, 0, len(resolved))
	for _, mapping := range resolved {
		names = append(names, mapping.GetEndpoint().GetName())
	}
	require.ElementsMatch(t, []string{"public", "restricted"}, names,
		"the exported set is what the interface lists, not what the services declare")
}

// The visibility on the interface entry is the one enforced, so an endpoint
// exported at "internal" answers to the allow-list rather than to the service's
// own value.
func TestInterfaceVisibilityIsWhatEndpointsCarry(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)

	exposed, err := mod.ExposedEndpoints(ctx)
	require.NoError(t, err)
	visibility := make(map[string]string, len(exposed))
	for _, endpoint := range exposed {
		visibility[endpoint.GetName()] = endpoint.GetVisibility()
	}
	require.Equal(t, map[string]string{
		"grpc":       resources.VisibilityModule,
		"public":     resources.VisibilityPublic,
		"restricted": resources.VisibilityInternal,
		// Exported, but its visibility records a location the interface cannot
		// restate, so the entry grants export without moving it inside.
		"listed": resources.VisibilityExternal,
	}, visibility)

	gateway, err := mod.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	byName := make(map[string]*resources.Endpoint, len(gateway.Endpoints))
	for _, endpoint := range gateway.Endpoints {
		byName[endpoint.Name] = endpoint
	}
	// "restricted" is exported at internal, so only its allow-list reaches it.
	require.True(t, byName["restricted"].AllowsModule("platform"))
	require.False(t, byName["restricted"].AllowsModule("other"))
	// "omitted" is public on the service and exported by nothing.
	require.False(t, byName["omitted"].AllowsModule("platform"))
	// Visibility never restricts reachability inside the owning module.
	require.True(t, byName["omitted"].AllowsModule("saas"))
}

// "external" is a location written as a visibility, and the only record that an
// endpoint lives outside the system. Exporting over it would move the endpoint
// inside, turning an address resolved from DNS into an allocated port — for
// every endpoint of the module, since an interface entry can only grant
// internal, module or public.
func TestInterfaceBoundaryKeepsExternalEndpointsExternal(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	vendor, err := mod.LoadServiceFromName(ctx, "vendor")
	require.NoError(t, err)

	for _, endpoint := range vendor.Endpoints {
		require.Truef(t, endpoint.External(), "endpoint %q stopped being external", endpoint.Name)
		require.Equalf(t, resources.VisibilityExternal, endpoint.Visibility,
			"endpoint %q lost the location its visibility records", endpoint.Name)
	}

	endpoints, err := vendor.LoadEndpoints(ctx)
	require.NoError(t, err)
	require.Len(t, endpoints, 2)
	for _, endpoint := range endpoints {
		require.Truef(t, resources.IsExternalEndpoint(endpoint),
			"endpoint %q would be given an allocated port instead of a DNS address", endpoint.GetName())
	}
}

// A service loaded by directory rather than through its module — what an agent
// does with the service it serves, and what a lookup from a working directory
// does — must observe the same boundary. Reporting an endpoint at its authored
// visibility would hand a consumer an endpoint the validators refuse.
func TestModuleInterfaceAppliesToServicesLoadedByDirectory(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"
	gatewayDir := filepath.Join(dir, "modules/saas/services/gateway")

	direct, err := resources.LoadServiceFromDir(ctx, gatewayDir)
	require.NoError(t, err)
	require.NoError(t, resources.ApplyModuleInterface(ctx, direct))

	byName := make(map[string]string, len(direct.Endpoints))
	for _, endpoint := range direct.Endpoints {
		byName[endpoint.Name] = endpoint.Visibility
	}
	require.Equal(t, map[string]string{
		"public":     resources.VisibilityPublic,
		"restricted": resources.VisibilityInternal,
		"omitted":    resources.VisibilityPrivate,
	}, byName)

	// The same service reached through the module has to agree, or the run path
	// and the static passes are reading two different contracts.
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	viaModule, err := mod.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	for _, endpoint := range viaModule.Endpoints {
		require.Equalf(t, byName[endpoint.Name], endpoint.Visibility,
			"endpoint %q differs by load path", endpoint.Name)
	}
}

// Reloading a service must not hand back a different contract than the one it
// was reloaded from: it kept neither the module identity nor the boundary.
func TestReloadServiceKeepsModuleAndBoundary(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"

	gateway := loadService(ctx, t, dir, "saas", "gateway")
	reloaded, err := resources.ReloadService(ctx, gateway)
	require.NoError(t, err)
	require.Equal(t, gateway.MustUnique(), reloaded.MustUnique(), "reload lost the module identity")

	before := make(map[string]string, len(gateway.Endpoints))
	for _, endpoint := range gateway.Endpoints {
		before[endpoint.Name] = endpoint.Visibility
	}
	for _, endpoint := range reloaded.Endpoints {
		require.Equalf(t, before[endpoint.Name], endpoint.Visibility,
			"endpoint %q changed visibility across a reload", endpoint.Name)
	}
}

// Applying the boundary must not rewrite what the author wrote: the exported
// visibility is a property of the module, and saving a service for an unrelated
// reason would otherwise migrate its endpoints.
func TestInterfaceBoundaryDoesNotRewriteAuthoredVisibility(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := "testdata/workspaces/interface-boundary-visibility"
	require.NoError(t, os.CopyFS(dir, os.DirFS(source)))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := mod.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.NoError(t, gateway.Save(ctx))

	saved, err := os.ReadFile(filepath.Join(dir, "modules/saas/services/gateway/service.codefly.yaml"))
	require.NoError(t, err)
	var declaration struct {
		Endpoints []struct {
			Name       string `yaml:"name"`
			Visibility string `yaml:"visibility"`
		} `yaml:"endpoints"`
	}
	require.NoError(t, yaml.Unmarshal(saved, &declaration))
	authored := make(map[string]string, len(declaration.Endpoints))
	for _, endpoint := range declaration.Endpoints {
		authored[endpoint.Name] = endpoint.Visibility
	}
	// "restricted" is exported at internal and "omitted" not exported at all,
	// yet both were authored public and must save as they were written.
	require.Equal(t, map[string]string{
		"public":     resources.VisibilityPublic,
		"restricted": resources.VisibilityPublic,
		"omitted":    resources.VisibilityPublic,
	}, authored)
}
