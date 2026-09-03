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

// The resolved endpoint address is consumer-specific: a native consumer and a
// container consumer of the same workspace value must each get an address in
// their own access family, so the first read must not bake one into the shared
// configuration for the other.
func TestManagerInterpolatesEndpointPerConsumerAccess(t *testing.T) {
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
				{Address: "http://host.docker.internal:45123", Access: resources.NewContainerNetworkAccess()},
			},
		},
	}

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithNetworkMappings(mappings, resources.NewNativeNetworkAccess())
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	native, err := manager.GetWorkspaceDependenciesConfigurations(ctx, "work-context")
	require.NoError(t, err)
	nativeURL, err := resources.GetConfigurationValue(ctx, native[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:45123/v1/auth/.well-known/jwks.json", nativeURL)

	manager.WithNetworkMappings(mappings, resources.NewContainerNetworkAccess())
	container, err := manager.GetWorkspaceDependenciesConfigurations(ctx, "work-context")
	require.NoError(t, err)
	containerURL, err := resources.GetConfigurationValue(ctx, container[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "http://host.docker.internal:45123/v1/auth/.well-known/jwks.json", containerURL)
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

// The composition root provides a workspace configuration that a composed
// module's service reads at runtime without declaring it as a dependency
// (codefly.For(ctx).WorkspaceValue). The root injects such configurations into
// every service in the run, so they must appear in the composition-root set —
// while a composed module's own configuration, scoped to the services that
// declare it, must not.
func TestManagerCompositionRootConfigurationsReachEveryService(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host
    path: ../host
`)
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env", "authority-jwks-url=https://root/jwks.json\n")

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

	rootConfs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, rootConfs, 1)
	url, err := resources.GetConfigurationValue(ctx, rootConfs[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "https://root/jwks.json", url)

	// The composed module's own configuration is not a composition-root
	// configuration: it reaches only the services that declare it.
	_, err = resources.FindWorkspaceConfiguration(ctx, rootConfs, "observability")
	require.Error(t, err)

	// Both remain reachable through the full workspace set.
	all, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// A composition root wires a cross-module URL as a workspace configuration by
// referencing a composed endpoint. Injected run-wide, it must be endpoint
// interpolated for the consumer's access in the composition-root set too.
func TestManagerCompositionRootConfigurationsInterpolateEndpoints(t *testing.T) {
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
				{Address: "http://host.docker.internal:45123", Access: resources.NewContainerNetworkAccess()},
			},
		},
	}

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithNetworkMappings(mappings, resources.NewNativeNetworkAccess())
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	native, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, native, 1)
	nativeURL, err := resources.GetConfigurationValue(ctx, native[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:45123/v1/auth/.well-known/jwks.json", nativeURL)

	manager.WithNetworkMappings(mappings, resources.NewContainerNetworkAccess())
	container, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	containerURL, err := resources.GetConfigurationValue(ctx, container[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "http://host.docker.internal:45123/v1/auth/.well-known/jwks.json", containerURL)
}

// An invocation-scoped workspace override (SDK --set of a workspace value) is a
// composition-root configuration too: it is provided by the run, not a composed
// module, so it reaches every service.
func TestManagerCompositionRootConfigurationsIncludeInvocationOverrides(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
`)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)

	encoded, err := resources.EncodeWorkspaceConfigurationOverrides([]resources.WorkspaceConfigurationOverride{
		{Name: "work-context", Key: "authority-jwks-url", Value: "https://override/jwks.json"},
	})
	require.NoError(t, err)
	t.Setenv(resources.WorkspaceConfigurationOverridesEnvironment, encoded)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	rootConfs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, rootConfs, 1)
	url, err := resources.GetConfigurationValue(ctx, rootConfs[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "https://override/jwks.json", url)
}

// When the composition root and a composed module both declare a configuration
// of the same name, the root wins at load (mirror of #374) and the name is
// composition-root origin — the run-wide value is the root's, never the
// module's suppressed one.
func TestManagerCompositionRootWinsOnNameConflict(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host
    path: ../host
`)
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env", "authority-jwks-url=https://root/jwks.json\n")

	writeConfigurationFile(t, root, "host/module.codefly.yaml", `kind: module
name: host
services:
  - name: telemetry
`)
	writeConfigurationFile(t, root, "host/configurations/local/work-context.env", "authority-jwks-url=https://module/jwks.json\n")
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

	rootConfs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, rootConfs, 1)
	url, err := resources.GetConfigurationValue(ctx, rootConfs[0], "work-context", "authority-jwks-url")
	require.NoError(t, err)
	require.Equal(t, "https://root/jwks.json", url)
}

// An invocation-scoped override that lands on a name a composed module also
// provides is still a composition-root configuration: the run supplies the value
// via --set, so it must reach every service, not only the services that declared
// the composed module's configuration. Attributing the override to the name it
// touched is what keeps it from silently staying composed-scoped.
func TestManagerCompositionRootConfigurationsIncludeOverrideOfComposedName(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host
    path: ../host
`)

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

	encoded, err := resources.EncodeWorkspaceConfigurationOverrides([]resources.WorkspaceConfigurationOverride{
		{Name: "observability", Key: "OBSERVABILITY_URL", Value: "https://override/obs"},
	})
	require.NoError(t, err)
	t.Setenv(resources.WorkspaceConfigurationOverridesEnvironment, encoded)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	// The overridden composed-module name is now in the run-wide set...
	rootConfs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, rootConfs, 1)
	conf, err := resources.FindWorkspaceConfiguration(ctx, rootConfs, "observability")
	require.NoError(t, err)
	url, err := resources.GetConfigurationValue(ctx, conf, "observability", "OBSERVABILITY_URL")
	require.NoError(t, err)
	require.Equal(t, "https://override/obs", url)

	// ...and the override value is what the declaring service sees too.
	depConfs, err := manager.GetWorkspaceDependenciesConfigurations(ctx, "observability")
	require.NoError(t, err)
	require.Len(t, depConfs, 1)
	depURL, err := resources.GetConfigurationValue(ctx, depConfs[0], "observability", "OBSERVABILITY_URL")
	require.NoError(t, err)
	require.Equal(t, "https://override/obs", depURL)
}

// A composition-root secret is injected run-wide like any other root
// configuration: it must appear in the composition-root set, resolved, so every
// service in the run receives it. This locks the security-relevant path — the
// feature broadens a root secret's reach from the declaring services to the whole
// run, and that must stay covered.
func TestManagerCompositionRootConfigurationsIncludeSecrets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
`)
	writeConfigurationFile(t, root, "solution/configurations/local/ops.secret.env", "TOKEN=ops-token\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	rootConfs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, rootConfs, 1)
	conf, err := resources.FindWorkspaceConfiguration(ctx, rootConfs, "ops")
	require.NoError(t, err)
	token, err := resources.GetConfigurationValue(ctx, conf, "ops", "TOKEN")
	require.NoError(t, err)
	require.Equal(t, "ops-token", token)
	require.True(t, conf.Infos[0].ConfigurationValues[0].Secret, "root secret must remain marked secret in the run-wide set")
}

// A composition-root secret that references an unconfigured provider is a hard
// error from the run-wide getter, never a silently dropped or unresolved value:
// it is injected into every service, so an unresolvable one must fail the run,
// exactly as the endpoint reference does. Degrading to skip-or-emit-unresolved
// would inject a missing secret everywhere.
func TestManagerCompositionRootConfigurationsErrorOnUnresolvableSecret(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
`)
	writeConfigurationFile(t, root, "solution/configurations/local/ops.secret.env", "TOKEN=op://vault/item/field\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	_, err = manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not configured")
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
