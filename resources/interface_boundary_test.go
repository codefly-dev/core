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
	// "private" is blanked on save and read back as private, as it always was.
	require.Equal(t, map[string]string{
		"public":     resources.VisibilityPublic,
		"restricted": "",
		"omitted":    resources.VisibilityPublic,
	}, authored)
}
