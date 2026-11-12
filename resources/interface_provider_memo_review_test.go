package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

// The composition half of the memo is what distinguishes two workspaces that
// read the same files: the locations and read sets are keyed by name and
// unordered, so reordering the composed modules leaves them equal while the
// provider list follows the module order. Only the composition digest sees
// the reorder.
func TestInterfaceProvidersFollowCompositionOrder(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/edge/module.codefly.yaml":                    edgeModule,
		"modules/edge/services/memcache/service.codefly.yaml": bindingService("memcache", "tcp", ""),
	})
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	require.NoError(t, workspace.AddModuleReference(&resources.ModuleReference{Name: "edge"}))

	ordered := func() []string {
		providers, err := workspace.InterfaceProviders(ctx)
		require.NoError(t, err)
		names := make([]string, 0, len(providers))
		for _, provider := range providers {
			names = append(names, provider.String())
		}
		return names
	}
	require.Equal(t, []string{
		"platform/api/grpc (example.dev/widgets@1.2.0)",
		"platform/redis (codefly.dev/cache@0.3.0)",
		"edge/memcache (codefly.dev/cache@0.3.2)",
	}, ordered())

	// Swap platform and edge in place: same names, same files, same digests.
	n := len(workspace.Modules)
	workspace.Modules[0], workspace.Modules[n-1] = workspace.Modules[n-1], workspace.Modules[0]
	require.Equal(t, []string{
		"edge/memcache (codefly.dev/cache@0.3.2)",
		"platform/api/grpc (example.dev/widgets@1.2.0)",
		"platform/redis (codefly.dev/cache@0.3.0)",
	}, ordered())
}

// Loading a module validates its interface against the service declarations
// it names (LoadModuleFromDir -> Module.ValidateInterface ->
// loadDeclaredService), so those are inputs of the memo too: the load records
// every file it read and the memo re-digests them. Removing the service an
// interface entry names makes a fresh load refuse, and the memoized workspace
// refuses the same way instead of serving the providers it computed before
// the service went away.
func TestInterfaceProvidersSeeAServiceTheInterfaceNamesDisappear(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	_, err = workspace.InterfaceProviders(ctx)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(filepath.Join(root, "modules/platform/services/api")))

	fresh, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	_, freshErr := fresh.InterfaceProviders(ctx)
	require.Error(t, freshErr, "a fresh workspace refuses the interface that names a missing service")

	_, memoErr := workspace.InterfaceProviders(ctx)
	require.Error(t, memoErr, "the memoized workspace must refuse the same way, not serve the providers computed before the service was removed")
}

// Twelve concurrent callers, as a render loads services, with a mutation of
// the handed-out copy racing them. Run under -race.
func TestInterfaceProvidersConcurrentCallers(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", nil)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	web := &resources.ServiceWithModule{Module: "apps", Name: "web"}

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				providers, err := workspace.InterfaceProviders(ctx)
				require.NoError(t, err)
				require.Len(t, providers, 2)
				providers[0].Module = "mine"
				loaded, err := workspace.LoadService(ctx, web)
				require.NoError(t, err)
				requireBound(t, loaded.ServiceDependencies[0], "platform", "redis")
			}
		}()
	}
	wg.Wait()
}
