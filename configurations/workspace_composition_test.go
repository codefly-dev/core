package configurations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestComposedWorkspaceProvidesConfigurationWithoutProductCopies(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\n")
	writeConfigurationFile(t, root, "core/configurations/staging/platform.env", "GATEWAY=core-endpoint\n")
	writeConfigurationFile(t, root, "core/configurations/staging/identity.env", "PROVIDER=owner-provider\n")
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: platform-obin\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\n")
	writeConfigurationFile(t, root, "product/configurations/staging/identity.env", "PROVIDER=product-provider\n")
	ws, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "product"))
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)
	require.NoError(t, loader.Load(ctx, &resources.Environment{Name: "staging"}))
	values := make(map[string]string)
	for _, conf := range loader.Configurations() {
		for _, info := range conf.Infos {
			key := "GATEWAY"
			if info.Name == "identity" {
				key = "PROVIDER"
			}
			value, err := resources.GetConfigurationValue(ctx, conf, info.Name, key)
			require.NoError(t, err)
			values[info.Name] = value
		}
	}
	require.Equal(t, map[string]string{"platform": "core-endpoint", "identity": "product-provider"}, values)
	require.ElementsMatch(t, []string{"platform", "identity"}, loader.CompositionRootWorkspaceConfigurationNames())
}
