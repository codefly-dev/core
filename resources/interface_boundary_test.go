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
		workspace,
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
		workspace,
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

// The visibility on the interface entry is the one enforced: an endpoint
// exported at "internal" is reachable by whatever composes the workspace,
// whatever the service's own value says.
func TestInterfaceVisibilityIsWhatEndpointsCarry(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)

	exposed, err := mod.InterfaceEndpoints(ctx)
	require.NoError(t, err)
	visibility := make(map[string]string, len(exposed))
	for _, endpoint := range exposed {
		visibility[endpoint.GetName()] = endpoint.GetVisibility()
	}
	require.Equal(t, map[string]string{
		"grpc":       resources.VisibilityInternal,
		"public":     resources.VisibilityPublic,
		"restricted": resources.VisibilityInternal,
		// Exported like any other: where it lives is its location, which the
		// entry never touches.
		"listed": resources.VisibilityInternal,
	}, visibility)
	gateway, err := mod.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	byName := make(map[string]*resources.Endpoint, len(gateway.Endpoints))
	for _, endpoint := range gateway.Endpoints {
		byName[endpoint.Name] = endpoint
	}
	// "restricted" is private to its service and exported at internal by the
	// module, so every module of the composition reaches it — the entry names
	// nobody.
	require.True(t, byName["restricted"].AllowsModule("platform"))
	require.True(t, byName["restricted"].AllowsModule("other"))
	// "omitted" is public on the service and exported by nothing.
	require.False(t, byName["omitted"].AllowsModule("platform"))
	// Visibility never restricts reachability inside the owning module.
	require.True(t, byName["omitted"].AllowsModule("saas"))
}

// Where an endpoint lives is its location, and the interface never touches it:
// an external endpoint the interface exports is exported at the entry's
// visibility and still resolves from DNS, and one the interface omits is
// private and still external. Neither is given an allocated port.
func TestInterfaceBoundaryKeepsExternalEndpointsExternal(t *testing.T) {
	ctx := context.Background()
	const dir = "testdata/workspaces/interface-boundary-visibility"

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	mod, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	vendor, err := mod.LoadServiceFromName(ctx, "vendor")
	require.NoError(t, err)

	visibility := make(map[string]string, len(vendor.Endpoints))
	for _, endpoint := range vendor.Endpoints {
		require.Truef(t, endpoint.External(), "endpoint %q stopped being external", endpoint.Name)
		require.Equalf(t, resources.LocationExternal, endpoint.Location, "endpoint %q lost its location", endpoint.Name)
		visibility[endpoint.Name] = endpoint.Visibility
	}
	require.Equal(t, map[string]string{"listed": resources.VisibilityInternal, "unlisted": resources.VisibilityPrivate}, visibility,
		"the interface decides what an external endpoint exports exactly as it does for any other")

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

// Saving writes the authored value to the file and must hand the LIVE one
// back: the visibility the interface exported. A save that left the authored
// value live would narrow what the module exported — here from internal back
// to private — and a reference from another module would stop resolving.
func TestSavingRestoresTheExportedVisibility(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.codefly.yaml"), []byte(
		"kind: module\nname: saas\nservices:\n    - name: accounts\ninterface:\n    endpoints:\n        - service: accounts\n          endpoint: grpc\n          visibility: internal\n"), 0o644))
	svcDir := filepath.Join(dir, "services", "accounts")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.codefly.yaml"), []byte(
		"kind: service\nname: accounts\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nendpoints:\n  - name: grpc\n    api: grpc\n"), 0o644))

	mod, err := resources.LoadModuleFromDir(ctx, dir)
	require.NoError(t, err)
	service, err := mod.LoadServiceFromName(ctx, "accounts")
	require.NoError(t, err)
	grpc := service.Endpoints[0]
	require.Equal(t, resources.VisibilityInternal, grpc.Visibility)

	judge := func(consumer string) error {
		info := &resources.EndpointInformation{Module: "saas", Service: "accounts", Name: "grpc"}
		_, err := resources.SelectEndpointForReference(consumer, info, service.Endpoints)
		return err
	}
	require.NoError(t, judge("payments"))
	require.NoError(t, judge("other"), "internal names nobody: whatever composes the workspace reaches it")

	require.NoError(t, service.Save(ctx))
	require.Equal(t, resources.VisibilityInternal, grpc.Visibility, "the live visibility after a save is the interface's, not the file's")
	require.NoError(t, judge("other"), "a save must not narrow what the module exported")

	saved, err := os.ReadFile(filepath.Join(svcDir, "service.codefly.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(saved), "visibility", "the file keeps what the author wrote: nothing, which is private")
	require.NotContains(t, string(saved), "allow-modules")
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
	// "restricted" was authored private and exported at internal,
	// "omitted" authored public and not exported at all: both save as written,
	// and the entry's allow-list is never written into the service.
	require.Equal(t, map[string]string{
		"public":     resources.VisibilityPublic,
		"restricted": "",
		"omitted":    resources.VisibilityPublic,
	}, authored)
	require.NotContains(t, string(saved), "allow-modules")
}
