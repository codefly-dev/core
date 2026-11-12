package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

const edgeModule = `kind: module
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
`

func providerNames(ctx context.Context, t *testing.T, workspace *resources.Workspace) []string {
	t.Helper()
	providers, err := workspace.InterfaceProviders(ctx)
	require.NoError(t, err)
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.String())
	}
	sort.Strings(names)
	return names
}

// The providers are remembered across service loads, and every way the
// composition or a module declaration changes is seen by the next call: no
// mutation has to remember to clear anything.
func TestInterfaceProvidersFollowEveryMutation(t *testing.T) {
	ctx := context.Background()
	root := bindingWorkspace(t, "", map[string]string{
		"modules/edge/module.codefly.yaml":                    edgeModule,
		"modules/edge/services/memcache/service.codefly.yaml": bindingService("memcache", "tcp", ""),
	})
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	web := &resources.ServiceWithModule{Module: "apps", Name: "web"}
	platform := []string{
		"platform/api/grpc (example.dev/widgets@1.2.0)",
		"platform/redis (codefly.dev/cache@0.3.0)",
	}
	require.Equal(t, platform, providerNames(ctx, t, workspace))
	loaded, err := workspace.LoadService(ctx, web)
	require.NoError(t, err)
	requireBound(t, loaded.ServiceDependencies[0], "platform", "redis")

	// What a caller does to the providers it was handed stays with it.
	handed, err := workspace.InterfaceProviders(ctx)
	require.NoError(t, err)
	handed[0].Module = "elsewhere"
	handed[0].Identity.Version = "9.9.9"
	require.Equal(t, platform, providerNames(ctx, t, workspace))

	// A module added to the composition provides from the next call, and the
	// consumer it makes ambiguous is refused on the same workspace.
	require.NoError(t, workspace.AddModuleReference(&resources.ModuleReference{Name: "edge"}))
	require.Equal(t, append([]string{"edge/memcache (codefly.dev/cache@0.3.2)"}, platform...), providerNames(ctx, t, workspace))
	_, err = workspace.LoadService(ctx, web)
	require.ErrorContains(t, err, "several providers satisfy it")

	// A module removed stops providing.
	require.NoError(t, workspace.DeleteModule(ctx, "edge"))
	require.Equal(t, platform, providerNames(ctx, t, workspace))

	// A declaration rewritten on disk, by this process or any other, is read
	// again.
	declaration := filepath.Join(root, "modules/platform/module.codefly.yaml")
	require.NoError(t, os.WriteFile(declaration, []byte(strings.Replace(platformModule, "widgets@1.2.0", "widgets@1.3.0", 1)), 0o600))
	require.Equal(t, []string{
		"platform/api/grpc (example.dev/widgets@1.3.0)",
		"platform/redis (codefly.dev/cache@0.3.0)",
	}, providerNames(ctx, t, workspace))

	// A reference edited in place locates another declaration: the exported
	// fields are anyone's to change.
	elsewhere := filepath.Join(root, "modules/edge")
	workspace.Modules[0].PathOverride = &elsewhere
	require.Equal(t, []string{"platform/memcache (codefly.dev/cache@0.3.2)"}, providerNames(ctx, t, workspace))

	// The list replaced outright.
	workspace.Modules = workspace.Modules[1:]
	require.Empty(t, providerNames(ctx, t, workspace))
}
