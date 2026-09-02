package configurations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"
)

// A composed host declares workspace-configuration-dependencies (e.g.
// telemetry -> observability) whose configurations live in the host's own
// workspace, not the consuming solution's. The Manager must resolve them from
// the composed module so a solution-as-root boot does not die on the first
// host config it needs.
func TestManagerResolvesComposedModuleWorkspaceDependencies(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host
    path: ../host
`)
	writeConfigurationFile(t, root, "solution/configurations/local/internal-auth.secret.env", "TOKEN=solution-token\n")

	writeConfigurationFile(t, root, "host/module.codefly.yaml", `kind: module
name: host
services:
  - name: telemetry
`)
	writeConfigurationFile(t, root, "host/configurations/local/observability.env", "OBSERVABILITY_URL=host-observability\n")
	writeConfigurationFile(t, root, "host/services/telemetry/service.codefly.yaml", `kind: service
name: telemetry
version: 0.0.0
agent:
  kind: runtime::service
  name: go-grpc
  version: 0.0.1
  publisher: codefly.ai
`)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)

	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	confs, err := manager.GetWorkspaceDependenciesConfigurations(ctx, "observability")
	require.NoError(t, err)
	require.Len(t, confs, 1)
	url, err := resources.GetConfigurationValue(ctx, confs[0], "observability", "OBSERVABILITY_URL")
	require.NoError(t, err)
	require.Equal(t, "host-observability", url)
}

// A composition root wires a cross-module URL as a workspace configuration value
// by referencing a composed endpoint. The Manager resolves that ${endpoint:…}
// reference against the run's network mappings, so the consumer reads a plain URL.
func TestManagerInterpolatesEndpointInWorkspaceConfiguration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
`)
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env",
		"authority-jwks-url=${endpoint:saas-starter/auth-sidecar/http}/v1/auth/.well-known/jwks.json\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	mappings := []*basev0.NetworkMapping{
		{
			Endpoint: &basev0.Endpoint{Module: "saas-starter", Service: "auth-sidecar", Name: "http", Api: standards.HTTP},
			Instances: []*basev0.NetworkInstance{
				{Address: "http://localhost:45123", Access: resources.NewNativeNetworkAccess()},
			},
		},
	}

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithNetworkMappings(mappings, resources.NewNativeNetworkAccess())

	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	confs, err := manager.GetWorkspaceDependenciesConfigurations(ctx, "work-context")
	require.NoError(t, err)
	require.Len(t, confs, 1)
	url, err := resources.GetConfigurationValue(ctx, confs[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:45123/v1/auth/.well-known/jwks.json", url)
}

// An unknown endpoint reference fails with a clear error instead of emitting a
// broken URL.
func TestManagerErrorsOnUnknownEndpointReference(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
`)
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env",
		"authority-jwks-url=${endpoint:saas-starter/auth-sidecar/grpc}/v1/auth/.well-known/jwks.json\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	mappings := []*basev0.NetworkMapping{
		{
			Endpoint: &basev0.Endpoint{Module: "saas-starter", Service: "auth-sidecar", Name: "http", Api: standards.HTTP},
			Instances: []*basev0.NetworkInstance{
				{Address: "http://localhost:45123", Access: resources.NewNativeNetworkAccess()},
			},
		},
	}

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithNetworkMappings(mappings, resources.NewNativeNetworkAccess())

	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	_, err = manager.GetWorkspaceConfigurations(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestLoaderFlatLayout(t *testing.T) {
	testLoader(t, "testdata/flat")
}

func TestLoaderModuleLayout(t *testing.T) {
	testLoader(t, "testdata/module")
}

func testLoader(t *testing.T, dir string) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)

	manager.WithLoader(loader)

	env := resources.LocalEnvironment()

	require.NoError(t, manager.Load(ctx, env))

	confs, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	// - auth0/frontend
	// - global
	// - other_global
	require.Equal(t, 3, len(confs))

	// Get  configuration value for some key
	conf, err := resources.FindWorkspaceConfiguration(ctx, confs, "global")
	require.NoError(t, err)
	require.NotNil(t, conf)
	require.Equal(t, "value", shared.Must(resources.GetConfigurationValue(ctx, conf, "global", "key")))

	confs, err = manager.GetServiceConfigurations(ctx)

	require.NoError(t, err)
	// mod/ServiceWithModule
	// - something
	require.Equal(t, 1, len(confs))

	// For a service
	svc, err := workspace.FindUniqueServiceByName(ctx, "svc")
	require.NoError(t, err)

	identity, err := svc.Identity()
	require.NoError(t, err)

	conf, err = manager.GetServiceConfiguration(ctx, identity)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Get DNS for service and endpoint name
	dns, err := manager.GetDNS(ctx, identity, "rest")
	require.NoError(t, err)
	require.Equal(t, "localhost", dns.Host)

	// Get DNS for service and endpoint name
	svc2, err := workspace.FindUniqueServiceByName(ctx, "svc2")
	require.NoError(t, err)

	identity2, err := svc2.Identity()
	require.NoError(t, err)

	dns, err = manager.GetDNS(ctx, identity2, "rest")
	require.NoError(t, err)
	require.Equal(t, "aws.magic", dns.Host)
}
