package resources_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestValidateEndpointVisibility(t *testing.T) {
	cases := []struct {
		name         string
		consumer     string
		producer     string
		visibility   resources.Visibility
		exposure     string
		location     string
		allowModules []string
		deny         bool
		errorText    string
	}{
		{name: "private cross-module denied", consumer: "platform", producer: "saas", visibility: resources.VisibilityPrivate, deny: true, errorText: "private to module"},
		{name: "empty visibility cross-module denied", consumer: "platform", producer: "saas", visibility: "", deny: true, errorText: "private to module"},
		{name: "private same-module allowed", consumer: "saas", producer: "saas", visibility: resources.VisibilityPrivate},
		// Internal names nobody: it is reachable by whatever composes the
		// workspace, and every module judged here does.
		{name: "internal cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal},
		{name: "internal any module allowed", consumer: "web", producer: "saas", visibility: resources.VisibilityInternal},
		{name: "public cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityPublic},
		{name: "exposed public allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityPublic, exposure: resources.ExposurePublic},
		{name: "the former module spelling is an invalid declaration", consumer: "platform", producer: "saas", visibility: "module", deny: true, errorText: `unsupported visibility "module"`},
		{name: "the former external spelling is an invalid declaration", consumer: "platform", producer: "saas", visibility: "external", deny: true, errorText: `unsupported visibility "external"`},
		{name: "an invalid declaration is refused for the owning module too", consumer: "saas", producer: "saas", visibility: "module", deny: true, errorText: `unsupported visibility "module"`},
		// An authored allow-list is an invalid declaration, not a grant — for
		// the module it names as much as for any other.
		{name: "an authored allow-list is an invalid declaration", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal, allowModules: []string{"platform"}, deny: true, errorText: `authors allow-modules ["platform"]`},
		{name: "the wildcard is an invalid declaration", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal, allowModules: []string{"*"}, deny: true, errorText: `authors allow-modules ["*"]`},
		{name: "an authored allow-list is refused for the owning module too", consumer: "saas", producer: "saas", visibility: resources.VisibilityInternal, allowModules: []string{"saas"}, deny: true, errorText: "authors allow-modules"},
		{name: "an unknown exposure is an invalid declaration", consumer: "platform", producer: "saas", visibility: resources.VisibilityPublic, exposure: "ingress", deny: true, errorText: `unsupported exposure "ingress"`},
		{name: "exposure on an internal endpoint is an invalid declaration", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal, exposure: resources.ExposurePublic, deny: true, errorText: `exposure "public" with visibility "internal"`},
		{name: "exposure on an external endpoint is an invalid declaration", consumer: "platform", producer: "saas", visibility: resources.VisibilityPublic, location: resources.LocationExternal, exposure: resources.ExposurePublic, deny: true, errorText: `exposure "public" with location "external"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resources.ValidateEndpointVisibility(tc.consumer, tc.producer, resources.EndpointDeclaration{
				Service: "accounts", Name: "connect", Visibility: tc.visibility, Location: tc.location, Exposure: tc.exposure, AllowModules: tc.allowModules,
			})
			if tc.deny {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorText)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateServiceDependenciesAllowed(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/allowed-dependency-visibility")
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}

func TestValidateServiceDependenciesUnresolvedProducer(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/unresolved-dependency-visibility")
	require.NoError(t, err)
	// Dependencies on a module absent from the workspace and on a service
	// absent from a known module are the resolver's concern, not this pass's:
	// with nothing to judge, visibility validation must not error.
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}

func TestValidateServiceDependenciesDenied(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/denied-dependency-visibility")
	require.NoError(t, err)
	err = workspace.ValidateServiceDependencies(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private to module \"saas\"")
	require.Contains(t, err.Error(), "platform")
}

func TestValidateServiceDependenciesAllowsNamedSameAPIEndpoint(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/named-same-api-dependency")
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))

	module, err := workspace.LoadModuleFromName(ctx, "platform")
	require.NoError(t, err)
	consumer, err := module.LoadServiceFromName(ctx, "meter")
	require.NoError(t, err)
	require.Len(t, consumer.ServiceDependencies, 1)
	require.Equal(t, "usage", consumer.ServiceDependencies[0].Endpoints[0].Name)
}

func TestValidateServiceDependenciesRejectsAmbiguousAPIReference(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata/workspaces/named-same-api-dependency")))
	servicePath := filepath.Join(dir, "modules", "platform", "services", "meter", resources.ServiceConfigurationName)
	content, err := os.ReadFile(servicePath)
	require.NoError(t, err)
	content = bytes.Replace(content, []byte("      - name: usage"), []byte("      - api: grpc"), 1)
	require.NoError(t, os.WriteFile(servicePath, content, 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	err = workspace.ValidateServiceDependencies(context.Background())
	require.ErrorContains(t, err, "multiple grpc endpoints")
	require.ErrorContains(t, err, "specify an endpoint name")
}
