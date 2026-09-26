package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
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
          implements: example.dev/widgets@1.2.0
    capabilities:
        - service: redis
          implements: codefly.dev/cache@0.3.0
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
          implements: codefly.dev/cache@0.3.2
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

	// A module loaded on its own has no workspace to bind with.
	apps, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/apps"))
	require.NoError(t, err)
	_, err = apps.LoadServiceFromName(ctx, "web")
	require.ErrorContains(t, err, "only the workspace composing module apps can bind it")
}

func TestModuleImplementsEachInterfaceVersionOnce(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": platformModule + "        - service: api\n          implements: codefly.dev/cache@0.3.0\n",
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
			"    endpoints:\n        - service: redis\n          endpoint: tcp\n          implements: example.dev/widgets@1.2.0\n"),
		"platform/redis declares capability example.dev/widgets@1.2.0, which is a grpc interface": moduleWith(
			"    capabilities:\n        - service: redis\n          implements: example.dev/widgets@1.2.0\n"),
		"platform/api/grpc implements codefly.dev/cache@0.3.0, which is a capability": moduleWith(
			"    endpoints:\n        - service: api\n          endpoint: grpc\n          implements: codefly.dev/cache@0.3.0\n"),
	} {
		root := bindingWorkspace(t, "", map[string]string{"modules/platform/module.codefly.yaml": module})
		platform, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
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

// A capability entry is part of the module's declared interface, so declaring
// one makes the interface the module's export boundary like any other entry.
func TestCapabilityOnlyInterfaceIsTheExportBoundary(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/platform/module.codefly.yaml": "kind: module\nname: platform\nservices:\n    - name: redis\n    - name: api\n" +
			"interface:\n    capabilities:\n        - service: redis\n          implements: codefly.dev/cache@0.3.0\n",
		"modules/platform/services/redis/service.codefly.yaml": strings.Replace(bindingService("redis", "tcp", ""), "api: tcp\n", "api: tcp\n      visibility: public\n", 1),
	})
	platform, err := resources.LoadModuleFromDir(ctx, filepath.Join(root, "modules/platform"))
	require.NoError(t, err)
	require.True(t, platform.HasInterface())
	redis, err := platform.LoadServiceFromName(ctx, "redis")
	require.NoError(t, err)
	require.Equal(t, resources.VisibilityPrivate, redis.Endpoints[0].Visibility,
		"a capability consumed over the network needs its endpoint exported too")
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
