package resources_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

const bindingAgent = `agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.1
    publisher: codefly.ai
`

// platformModule implements a capability on redis and an endpoint interface on
// api, and exports both endpoints.
const platformModule = `kind: module
name: platform
services:
    - name: redis
    - name: api
interface:
    endpoints:
        - service: redis
          endpoint: tcp
          visibility: public
        - service: api
          endpoint: grpc
          visibility: public
          implements:
              - example.dev/widgets@1.2.0
    capabilities:
        - service: redis
          implements:
              - codefly.dev/cache@0.3.0
`

func bindingService(name, api, dependencies string) string {
	return "kind: service\nname: " + name + "\nversion: 0.0.0\n" + bindingAgent +
		"endpoints:\n    - name: " + api + "\n      api: " + api + "\n" + dependencies
}

const webDependencies = `service-dependencies:
    - interface: codefly.dev/cache@^0.3
    - interface: example.dev/widgets@^1.1
`

// bindingWorkspace writes a workspace composing platform and apps, where
// apps/web requires the interfaces platform implements. extra adds or
// replaces files.
func bindingWorkspace(t *testing.T, workspaceExtra string, extra map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"workspace.codefly.yaml":                               "name: product\nlayout: modules\nmodules:\n    - name: platform\n    - name: apps\n" + workspaceExtra,
		"modules/platform/module.codefly.yaml":                 platformModule,
		"modules/platform/services/redis/service.codefly.yaml": bindingService("redis", "tcp", ""),
		"modules/platform/services/api/service.codefly.yaml":   bindingService("api", "grpc", ""),
		"modules/apps/module.codefly.yaml":                     "kind: module\nname: apps\nservices:\n    - name: web\n",
		"modules/apps/services/web/service.codefly.yaml":       bindingService("web", "http", webDependencies),
	}
	for name, content := range extra {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return root
}

func loadWeb(ctx context.Context, t *testing.T, root string) (*resources.Service, error) {
	t.Helper()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	return workspace.LoadService(ctx, &resources.ServiceWithModule{Module: "apps", Name: "web"})
}

func requireBound(t *testing.T, dep *resources.ServiceDependency, module, name string, endpoints ...string) {
	t.Helper()
	require.Equal(t, module, dep.Module, dep.Interface)
	require.Equal(t, name, dep.Name, dep.Interface)
	var names []string
	for _, endpoint := range dep.Endpoints {
		names = append(names, endpoint.Name)
	}
	require.Equal(t, endpoints, names, dep.Interface)
}

func TestInterfaceDependencyBindsToTheOneProviderInScope(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)

	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
	requireBound(t, web.ServiceDependencies[1], "platform", "api", "grpc")

	// The bound edges are ordinary edges from here on, so the cross-module
	// boundary judges them as it judges any other.
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}

func TestBoundDependencySavesAsDeclared(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)

	require.NoError(t, web.Save(ctx))
	saved, err := os.ReadFile(filepath.Join(root, "modules/apps/services/web", resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(saved), "interface: codefly.dev/cache@^0.3")
	require.NotContains(t, string(saved), "redis", "the provider this workspace bound is not the author's declaration")
	require.NotContains(t, string(saved), "platform")

	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
	requireBound(t, web.ServiceDependencies[1], "platform", "api", "grpc")
}

func TestSecondProviderRequiresABinding(t *testing.T) {
	ctx := context.Background()
	edge := map[string]string{
		"modules/edge/module.codefly.yaml": `kind: module
name: edge
services:
    - name: memcache
interface:
    endpoints:
        - service: memcache
          endpoint: tcp
          visibility: public
    capabilities:
        - service: memcache
          implements:
              - codefly.dev/cache@0.3.2
`,
		"modules/edge/services/memcache/service.codefly.yaml": bindingService("memcache", "tcp", ""),
	}

	root := bindingWorkspace(t, "    - name: edge\n", edge)
	_, err := loadWeb(ctx, t, root)
	require.ErrorContains(t, err, "several providers satisfy it (edge/memcache (codefly.dev/cache@0.3.2), platform/redis (codefly.dev/cache@0.3.0))")

	root = bindingWorkspace(t, "    - name: edge\ninterface-bindings:\n    - interface: codefly.dev/cache\n      module: edge\n      service: memcache\n", edge)
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "edge", "memcache")

	// A binding chooses among providers; it cannot admit one outside the
	// consumer's range.
	edge["modules/edge/module.codefly.yaml"] = strings.Replace(edge["modules/edge/module.codefly.yaml"], "cache@0.3.2", "cache@0.4.0", 1)
	root = bindingWorkspace(t, "    - name: edge\ninterface-bindings:\n    - interface: codefly.dev/cache\n      module: edge\n      service: memcache\n", edge)
	_, err = loadWeb(ctx, t, root)
	require.ErrorContains(t, err, "the workspace binding edge/memcache provides no version in range")
}

func TestInterfaceDependencyOutsideEveryProviderFails(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http", "service-dependencies:\n    - interface: codefly.dev/cache@^1\n"),
	})
	_, err := loadWeb(ctx, t, root)
	require.ErrorContains(t, err, "requires codefly.dev/cache@^1: no provider in scope satisfies it; implementations in scope: platform/redis (codefly.dev/cache@0.3.0)")
}

func TestNamedServiceMustProvideTheInterfaceItIsRequiredFor(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - name: api\n      module: platform\n      interface: codefly.dev/cache@^0.3\n"),
	})
	_, err := loadWeb(ctx, t, root)
	require.ErrorContains(t, err, "the named service platform/api provides no version in range")

	root = bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - name: redis\n      module: platform\n      interface: codefly.dev/cache@^0.3\n"),
	})
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
}

func TestUnboundInterfaceDependencyNeverResolvesSilently(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	dir := filepath.Join(root, "modules/apps/services/web")

	// Loaded by directory, as an agent loads the service it serves: nothing has
	// bound it, and the runtime paths refuse it rather than wiring nothing.
	web, err := resources.LoadServiceFromDir(ctx, dir)
	require.NoError(t, err)
	web.WithModule("apps")
	_, err = resources.ResolveDependencyNetworkMappings("apps", web.ServiceDependencies, nil)
	require.ErrorContains(t, err, "service dependency on interface codefly.dev/cache@^0.3 is not bound to a provider")

	require.NoError(t, resources.ApplyInterfaceBindings(ctx, web, root))
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")

	reloaded, err := resources.ReloadService(ctx, web)
	require.NoError(t, err)
	requireBound(t, reloaded.ServiceDependencies[0], "platform", "redis")
	requireBound(t, reloaded.ServiceDependencies[1], "platform", "api", "grpc")

	// A module loaded on its own has no workspace to bind with. Loading still
	// works, so reading its endpoints does; using the dependency does not.
	apps, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/apps"))
	require.NoError(t, err)
	standalone, err := apps.LoadServiceFromName(ctx, "web")
	require.NoError(t, err)
	require.Empty(t, standalone.ServiceDependencies[0].Name)
	_, err = resources.ResolveDependencyNetworkMappings("apps", standalone.ServiceDependencies, nil)
	require.ErrorContains(t, err, "is not bound to a provider")
}

// Exporting a module's contracts reads its endpoints, never its dependencies,
// so a standalone module whose exported service requires an interface still
// exports.
func TestStandaloneModuleExportsWithoutBinding(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/apps/module.codefly.yaml": "kind: module\nname: apps\nservices:\n    - name: web\ninterface:\n    endpoints:\n        - service: web\n          endpoint: http\n          visibility: public\n",
	})
	apps, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/apps"))
	require.NoError(t, err)
	exported, err := apps.ExportedEndpointsForPackage(ctx)
	require.NoError(t, err)
	require.Len(t, exported, 1)
}

// A service found by directory from inside a workspace, as the SDK finds the
// service a session runs, is bound against that workspace.
func TestDirectoryLookupBindsAgainstTheWorkspaceAbove(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	_, web, err := resources.LoadModuleAndServiceUpFrom(ctx, filepath.Join(root, "modules/apps/services/web"))
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
	requireBound(t, web.ServiceDependencies[1], "platform", "api", "grpc")
}

// An agent loads by directory, sets the module, binds and may save. The
// requirement never picks up the consumer's module on the way.
func TestAgentPathSavesTheRequirementAsDeclared(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	dir := filepath.Join(root, "modules/apps/services/web")
	web, err := resources.LoadServiceFromDir(ctx, dir)
	require.NoError(t, err)
	web.WithModule("apps")
	require.Empty(t, web.ServiceDependencies[0].Module)
	require.NoError(t, resources.ApplyInterfaceBindings(ctx, web, root))
	require.NoError(t, web.Save(ctx))
	saved, err := os.ReadFile(filepath.Join(dir, resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.NotContains(t, string(saved), "module:")
}

func TestModuleImplementsEachInterfaceVersionOnce(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": platformModule + "        - service: api\n          implements: [codefly.dev/cache@0.3.0]\n",
	})
	_, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.ErrorContains(t, err, "module platform implements codefly.dev/cache@0.3.0 twice: by platform/redis and by platform/api")
}

func TestModuleConformsToThePublishedDefinitions(t *testing.T) {
	ctx := resources.WithInterfaceResolver(context.Background(), definitionResolver)

	root := bindingWorkspace(t, "", nil)
	platform, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.NoError(t, err)
	require.NoError(t, platform.ValidateInterfaceConformance(ctx))

	moduleWith := func(entries string) string {
		return "kind: module\nname: platform\nservices:\n    - name: redis\n    - name: api\ninterface:\n" + entries
	}
	for message, module := range map[string]string{
		"platform/redis/tcp serves tcp but implements example.dev/widgets@1.2.0, a grpc interface": moduleWith(
			"    endpoints:\n        - service: redis\n          endpoint: tcp\n          visibility: public\n          implements: [example.dev/widgets@1.2.0]\n"),
		"platform/redis declares capability example.dev/widgets@1.2.0, which is a grpc interface": moduleWith(
			"    capabilities:\n        - service: redis\n          implements: [example.dev/widgets@1.2.0]\n"),
		"platform/api/grpc implements codefly.dev/cache@0.3.0, which is a capability": moduleWith(
			"    endpoints:\n        - service: api\n          endpoint: grpc\n          visibility: public\n          implements: [codefly.dev/cache@0.3.0]\n"),
	} {
		root := bindingWorkspace(t, "", map[string]string{"modules/platform/module.codefly.yaml": module})
		platform, err := resources.LoadModuleFromDir(context.Background(), filepath.Join(root, "modules/platform"))
		require.NoError(t, err, message)
		require.ErrorContains(t, platform.ValidateInterfaceConformance(ctx), message)
	}
}

func TestProvidedConfigurationIsCheckedAgainstTheCapability(t *testing.T) {
	ctx := resources.WithInterfaceResolver(context.Background(), definitionResolver)
	root := bindingWorkspace(t, "", nil)
	platform, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.NoError(t, err)

	conforming := connection(
		&basev0.ConfigurationValue{Key: "url", Value: "redis://redis:6379"},
		&basev0.ConfigurationValue{Key: "password", Value: "s3cret", Secret: true},
	)
	require.NoError(t, platform.ValidateProvidedConfiguration(ctx, "redis", conforming))
	require.ErrorContains(t, platform.ValidateProvidedConfiguration(ctx, "redis", connection(&basev0.ConfigurationValue{Key: "url", Value: "redis://redis:6379"})),
		`platform/redis: interface codefly.dev/cache@0.3.0: configuration group "connection" from "platform/redis" does not conform: required key "password" is missing`)
	require.NoError(t, platform.ValidateProvidedConfiguration(ctx, "api", connection()), "api provides no capability")
}

// A capability entry states what a service provides, not which endpoints cross
// module lines: declaring one leaves the module's endpoints as their services
// declare them.
func TestCapabilityOnlyInterfaceIsNoExportBoundary(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": "kind: module\nname: platform\nservices:\n    - name: redis\n    - name: api\n" +
			"interface:\n    capabilities:\n        - service: redis\n          implements: [codefly.dev/cache@0.3.0]\n",
		"modules/platform/services/redis/service.codefly.yaml": strings.Replace(bindingService("redis", "tcp", ""), "api: tcp\n", "api: tcp\n      visibility: public\n", 1),
	})
	platform, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.NoError(t, err)
	require.False(t, platform.HasInterface())
	redis, err := platform.LoadServiceFromName(ctx, "redis")
	require.NoError(t, err)
	require.Equal(t, resources.VisibilityPublic, redis.Endpoints[0].Visibility)
	providers, err := platform.InterfaceProviders()
	require.NoError(t, err)
	require.Len(t, providers, 1)
}

// Only services are bound, so the other resources that share the dependency
// type refuse an interface instead of carrying an edge with no producer.
func TestUnboundResourcesRefuseInterfaceDependencies(t *testing.T) {
	ctx := context.Background()
	const requirement = "service-dependencies:\n    - interface: codefly.dev/cache@^0.3\n"
	root := t.TempDir()
	write := func(dir, file, content string) string {
		path := filepath.Join(root, dir)
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, file), []byte(content), 0o600))
		return path
	}

	job := write("job", resources.JobConfigurationName, "kind: job\nname: seed\nversion: 0.0.1\n"+requirement)
	_, err := resources.LoadJobFromDir(ctx, job)
	require.ErrorContains(t, err, `job "seed" requires interface codefly.dev/cache@^0.3`)

	application := write("application", resources.ApplicationConfigurationName, "kind: application\nname: desk\nversion: 0.0.1\n"+requirement)
	_, err = resources.LoadApplicationFromDir(ctx, application)
	require.ErrorContains(t, err, `application "desk" requires interface codefly.dev/cache@^0.3`)

	runnable, err := resources.NewRunnable(ctx, "report", &resources.Agent{Kind: resources.RunnableAgent, Name: "go", Version: "0.0.1", Publisher: "codefly.dev"}, "handler.go")
	require.NoError(t, err)
	runnable.ServiceDependencies = []*resources.ServiceDependency{{Interface: "codefly.dev/cache@^0.3"}}
	require.ErrorContains(t, runnable.Validate(), `runnable "report" requires interface codefly.dev/cache@^0.3`)
}

// Removing the service an interface is bound to removes named edges onto it,
// never the requirement: the next load binds another provider or says none is
// in scope.
func TestDeletingTheProviderKeepsTheRequirement(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	require.NoError(t, workspace.DeleteServiceDependencies(ctx, &resources.ServiceReference{Name: "redis", Module: "platform"}))
	saved, err := os.ReadFile(filepath.Join(root, "modules/apps/services/web", resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(saved), "interface: codefly.dev/cache@^0.3")
	require.Contains(t, string(saved), "interface: example.dev/widgets@^1.1")
}

// A named edge onto the provider an interface is bound to is a separate
// declaration: it is written as the author asked, and both bind on reload. A
// dependency that names its provider keeps what is added to it.
func TestAddingADependencyOnABoundProvider(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	require.NoError(t, web.AddDependency(ctx, &resources.ServiceIdentity{Name: "api", Module: "platform"}, []*resources.Endpoint{{Name: "grpc"}}))
	require.NoError(t, web.Save(ctx))
	saved, err := os.ReadFile(filepath.Join(root, "modules/apps/services/web", resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(saved), "interface: example.dev/widgets@^1.1")
	require.Contains(t, string(saved), "- name: api\n      module: platform")
	reloaded, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	require.Len(t, reloaded.ServiceDependencies, 3)

	root = bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - name: api\n      module: platform\n      interface: example.dev/widgets@^1.1\n"),
	})
	web, err = loadWeb(ctx, t, root)
	require.NoError(t, err)
	require.NoError(t, web.AddDependency(ctx, &resources.ServiceIdentity{Name: "api", Module: "platform"}, []*resources.Endpoint{{Name: "grpc"}}))
	require.NoError(t, web.Save(ctx))
	saved, err = os.ReadFile(filepath.Join(root, "modules/apps/services/web", resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(saved), "endpoints:\n        - api: \"\"\n          name: grpc")
}

// One gRPC endpoint serves several interfaces, and two major lines of one
// side by side. Each requirement binds to the version in its range, on the
// same endpoint, and the requirements stay separate dependencies.
func TestOneEndpointImplementsSeveralInterfaces(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": strings.Replace(platformModule, "              - example.dev/widgets@1.2.0\n",
			"              - example.dev/widgets@1.2.0\n              - example.dev/widgets@2.0.0\n              - example.dev/admin@1.0.0\n", 1),
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: example.dev/widgets@^2\n    - interface: example.dev/admin@^1\n"),
	})
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "api", "grpc")
	requireBound(t, web.ServiceDependencies[1], "platform", "api", "grpc")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
	providers, err := workspace.InterfaceProviders(ctx)
	require.NoError(t, err)
	var onGRPC []string
	for _, provider := range providers {
		if provider.Endpoint == "grpc" {
			onGRPC = append(onGRPC, provider.Identity.String())
		}
	}
	require.ElementsMatch(t, []string{"example.dev/widgets@1.2.0", "example.dev/widgets@2.0.0", "example.dev/admin@1.0.0"}, onGRPC)
}

// One service provides several capabilities from one entry.
func TestOneServiceProvidesSeveralCapabilities(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": strings.Replace(platformModule, "              - codefly.dev/cache@0.3.0\n",
			"              - codefly.dev/cache@0.3.0\n              - codefly.dev/queue@1.0.0\n", 1),
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: codefly.dev/cache@^0.3\n    - interface: codefly.dev/queue@^1\n"),
	})
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
	requireBound(t, web.ServiceDependencies[1], "platform", "redis")
}

// What a module interface refuses, each with the reason it gives.
func TestModuleInterfaceDeclarationsAreUnambiguous(t *testing.T) {
	ctx := context.Background()
	module := func(entries string) string {
		return "kind: module\nname: platform\nservices:\n    - name: redis\n    - name: api\ninterface:\n" + entries
	}
	for message, declaration := range map[string]string{
		"platform/api/grpc implements example.dev/widgets at both 1.2.0 and 1.4.0, one compatible line; list only the higher version": module(
			"    endpoints:\n        - service: api\n          endpoint: grpc\n          visibility: public\n          implements: [example.dev/widgets@1.2.0, example.dev/widgets@1.4.0]\n"),
		`declares the capabilities of service "redis" twice; list them in one entry`: module(
			"    capabilities:\n        - service: redis\n          implements: [codefly.dev/cache@0.3.0]\n        - service: redis\n          implements: [codefly.dev/queue@1.0.0]\n"),
		`capability entry for service "redis" implements nothing`: module(
			"    capabilities:\n        - service: redis\n"),
		"cannot unmarshal": module(
			"    endpoints:\n        - service: api\n          endpoint: grpc\n          visibility: public\n          implements: example.dev/widgets@1.2.0\n"),
	} {
		root := bindingWorkspace(t, "", map[string]string{"modules/platform/module.codefly.yaml": declaration})
		_, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
		require.ErrorContains(t, err, message)
	}
}

// One service may require the same interface twice: from whichever provider is
// bound, and from a service it names. A reload keeps each on its own provider.
func TestReloadKeepsEachRequirementOnItsProvider(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "    - name: edge\ninterface-bindings:\n    - interface: codefly.dev/cache\n      module: platform\n      service: redis\n", map[string]string{
		"modules/edge/module.codefly.yaml":                    "kind: module\nname: edge\nservices:\n    - name: memcache\ninterface:\n    endpoints:\n        - service: memcache\n          endpoint: tcp\n          visibility: public\n    capabilities:\n        - service: memcache\n          implements: [codefly.dev/cache@0.3.2]\n",
		"modules/edge/services/memcache/service.codefly.yaml": bindingService("memcache", "tcp", ""),
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: codefly.dev/cache@^0.3\n    - name: memcache\n      module: edge\n      interface: codefly.dev/cache@^0.3\n"),
	})
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
	requireBound(t, web.ServiceDependencies[1], "edge", "memcache")

	reloaded, err := resources.ReloadService(ctx, web)
	require.NoError(t, err)
	requireBound(t, reloaded.ServiceDependencies[0], "platform", "redis")
	requireBound(t, reloaded.ServiceDependencies[1], "edge", "memcache")
}

// A completion edge consumes no endpoint, and binding must not give it one.
func TestCompletionDependencyIsNotNarrowedToAnEndpoint(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: example.dev/widgets@^1.1\n      kind: completion\n"),
	})
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "api")
	require.NoError(t, web.ServiceDependencies[0].Validate())
}

// Every declared binding is checked, including one no dependency uses.
func TestInterfaceBindingsAreValidated(t *testing.T) {
	ctx := context.Background()
	for message, binding := range map[string]string{
		"interface binding for codefly.dev/cach chooses platform/redis, which implements no version of it; implementations in scope: none": "codefly.dev/cach\n      module: platform\n      service: redis",
		`interface binding "codefly.dev/cache@0.3.0": name "cache@0.3.0"`:                                                                  "codefly.dev/cache@0.3.0\n      module: platform\n      service: redis",
		"interface binding for codefly.dev/cache chooses platform/api, which implements no version of it":                                  "codefly.dev/cache\n      module: platform\n      service: api",
	} {
		root := bindingWorkspace(t, "interface-bindings:\n    - interface: "+binding+"\n", nil)
		_, err := loadWeb(ctx, t, root)
		require.ErrorContains(t, err, message)
	}
}

// With a resolver attached, the implementation a consumer is bound to is
// checked against its published definition — and only that one: a definition
// the host cannot resolve fails its consumers, not every load of the module.
func TestBindingChecksTheBoundImplementation(t *testing.T) {
	misdeclared := "kind: module\nname: platform\nservices:\n    - name: redis\n    - name: api\ninterface:\n    endpoints:\n        - service: redis\n          endpoint: tcp\n          visibility: public\n    capabilities:\n        - service: redis\n          implements: [codefly.dev/cache@0.3.0, example.dev/widgets@1.2.0]\n"
	ctx := resources.WithInterfaceResolver(context.Background(), definitionResolver)
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml":           misdeclared,
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http", "service-dependencies:\n    - interface: example.dev/widgets@^1.1\n"),
	})
	_, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.NoError(t, err, "loading a module does not resolve what it implements")
	_, err = loadWebWith(ctx, t, root)
	require.ErrorContains(t, err, "platform/redis declares capability example.dev/widgets@1.2.0, which is a grpc interface")

	// A definition the host cannot resolve fails only its consumers.
	unavailable := resources.WithInterfaceResolver(context.Background(), func(ctx context.Context, identity *resources.InterfaceIdentity) (*resources.Interface, error) {
		if identity.Name == "widgets" {
			return nil, fmt.Errorf("release withdrawn")
		}
		return definitionResolver(ctx, identity)
	})
	root = bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http", "service-dependencies:\n    - interface: codefly.dev/cache@^0.3\n"),
	})
	web, err := loadWebWith(unavailable, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")
	root = bindingWorkspace(t, "", nil)
	_, err = loadWebWith(unavailable, t, root)
	require.ErrorContains(t, err, "release withdrawn")
}

func loadWebWith(ctx context.Context, t *testing.T, root string) (*resources.Service, error) {
	t.Helper()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	return workspace.LoadService(ctx, &resources.ServiceWithModule{Module: "apps", Name: "web"})
}

// apiWithTwoGRPCEndpoints serves widgets 1.2.0 on grpc and 1.4.0 on grpc2.
func apiWithTwoGRPCEndpoints() map[string]string {
	return map[string]string{
		"modules/platform/module.codefly.yaml": strings.Replace(platformModule, "    capabilities:",
			"        - service: api\n          endpoint: grpc2\n          visibility: public\n          implements: [example.dev/widgets@1.4.0]\n    capabilities:", 1),
		"modules/platform/services/api/service.codefly.yaml": "kind: service\nname: api\nversion: 0.0.0\n" + bindingAgent +
			"endpoints:\n    - name: grpc\n      api: grpc\n    - name: grpc2\n      api: grpc\n",
	}
}

// The endpoints a dependency names select the implementation: they
// disambiguate one service implementing a line on two endpoints, and they
// can never wire the consumer to an endpoint that does not implement it.
func TestNamedEndpointsSelectTheImplementation(t *testing.T) {
	ctx := context.Background()
	for name, dependency := range map[string]string{
		"named service":  "    - name: api\n      module: platform\n      interface: example.dev/widgets@^1.2\n      endpoints:\n        - name: grpc2\n",
		"interface only": "    - interface: example.dev/widgets@^1.2\n      endpoints:\n        - name: grpc2\n",
	} {
		files := apiWithTwoGRPCEndpoints()
		files["modules/apps/services/web/service.codefly.yaml"] = bindingService("web", "http", "service-dependencies:\n"+dependency)
		web, err := loadWeb(ctx, t, bindingWorkspace(t, "", files))
		require.NoError(t, err, name)
		requireBound(t, web.ServiceDependencies[0], "platform", "api", "grpc2")
	}

	files := apiWithTwoGRPCEndpoints()
	files["modules/apps/services/web/service.codefly.yaml"] = bindingService("web", "http",
		"service-dependencies:\n    - name: api\n      module: platform\n      interface: example.dev/widgets@^1.2\n")
	_, err := loadWeb(ctx, t, bindingWorkspace(t, "", files))
	require.ErrorContains(t, err, "implements it more than once")

	// The endpoint named does not implement the interface: refused, not wired.
	files = map[string]string{
		"modules/platform/services/api/service.codefly.yaml": apiWithTwoGRPCEndpoints()["modules/platform/services/api/service.codefly.yaml"],
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: example.dev/widgets@^1.2\n      endpoints:\n        - name: grpc2\n"),
	}
	_, err = loadWeb(ctx, t, bindingWorkspace(t, "", files))
	require.ErrorContains(t, err, "none of the endpoints it names (grpc2) implements a version in range; implementations in scope: platform/api/grpc (example.dev/widgets@1.2.0)")

	// Naming the endpoint a capability consumer connects to does not narrow
	// which service provides the capability.
	web, err := loadWeb(ctx, t, bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: codefly.dev/cache@^0.3\n      endpoints:\n        - name: tcp\n"),
	}))
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "redis", "tcp")
}

// A consumer migrating between major lines requires both at once; only the
// same requirement declared twice is a duplicate.
func TestConsumerRequiresTwoLinesOfOneInterface(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": strings.Replace(platformModule, "              - example.dev/widgets@1.2.0\n",
			"              - example.dev/widgets@1.2.0\n              - example.dev/widgets@2.0.0\n", 1),
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: example.dev/widgets@^1\n    - interface: example.dev/widgets@^2\n"),
	})
	web, err := loadWeb(ctx, t, root)
	require.NoError(t, err)
	requireBound(t, web.ServiceDependencies[0], "platform", "api", "grpc")
	requireBound(t, web.ServiceDependencies[1], "platform", "api", "grpc")

	root = bindingWorkspace(t, "", map[string]string{
		"modules/apps/services/web/service.codefly.yaml": bindingService("web", "http",
			"service-dependencies:\n    - interface: example.dev/widgets@^1\n    - interface: example.dev/widgets@^1\n"),
	})
	_, err = loadWeb(ctx, t, root)
	require.ErrorContains(t, err, `duplicate interface requirement "example.dev/widgets@^1": declare it once`)
}

// A module whose interface declares only capabilities has declared one: it
// exports no endpoint contract, and is not told it declares no interface.
func TestCapabilityOnlyModuleExportsNoEndpoints(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": "kind: module\nname: platform\nservices:\n    - name: redis\n    - name: api\ninterface:\n    capabilities:\n        - service: redis\n          implements: [codefly.dev/cache@0.3.0]\n",
	})
	platform, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.NoError(t, err)
	exported, err := platform.ExportedEndpointsForPackage(ctx)
	require.NoError(t, err)
	require.Empty(t, exported)
}

// Binding without a resolver must not be silent. An agent loading the service it
// serves has no resolver, so skipping conformance cannot be an error — but
// conformance is what makes `implements:` mean anything, and a host that never
// calls WithInterfaceResolver would otherwise get green binding with zero
// verification and no way to tell afterwards. The skip therefore leaves a DEBUG
// breadcrumb naming each implementation that went unchecked.
func TestBindingWithoutAResolverSaysWhatWentUnverified(t *testing.T) {
	baseCtx := context.Background()
	capture := &warningCapture{}
	ctx := wool.New(baseCtx, &wool.Resource{Kind: "test", Unique: "interface-unverified"}).WithLogger(capture).Inject(baseCtx)
	previous := wool.GlobalLogLevel()
	wool.SetGlobalLogLevel(wool.TRACE)
	t.Cleanup(func() { wool.SetGlobalLogLevel(previous) })

	root := bindingWorkspace(t, "", nil)
	web, err := resources.LoadServiceFromDir(ctx, filepath.Join(root, "modules/apps/services/web"))
	require.NoError(t, err)
	web.WithModule("apps")
	require.NoError(t, resources.ApplyInterfaceBindings(ctx, web, root))
	requireBound(t, web.ServiceDependencies[0], "platform", "redis")

	var said string
	for _, log := range capture.logs {
		if strings.Contains(log.String(), "without conformance checks") {
			said = log.String()
		}
	}
	require.NotEmpty(t, said, "the skip must leave a breadcrumb; logs=%v", capture.logs)
	require.Contains(t, said, "platform/redis", "the breadcrumb must name the unverified implementation")
}
