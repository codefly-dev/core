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

func compositionFile(t *testing.T, root, relative, content string) {
	t.Helper()
	file := filepath.Join(root, relative)
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
}

func TestWorkspaceCompositionOwnsPinsAndPreservesProductDeclaration(t *testing.T) {
	root := t.TempDir()
	compositionFile(t, root, "core/workspace.codefly.yaml", `name: platform-core
layout: modules
modules:
  - name: accounts
    source: team/accounts
    version: 1.2.3
  - name: documents
environments:
  - name: local
`)
	compositionFile(t, root, "core/modules/documents/module.codefly.yaml", "kind: module\nname: documents\nservices: []\n")
	compositionFile(t, root, "product/workspace.codefly.yaml", `name: platform-obin
layout: modules
workspaces:
  - name: platform-core
    path: ../core
solutions:
  - name: wiki
    source: team/solutions
    module: solutions/wiki
    version: 2.0.0
  - name: lastlogin-go
    source: team/solutions
    module: solutions/lastlogin-go
    version: 2.0.0
environments:
  - name: staging
`)
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "product"))
	require.NoError(t, err)
	require.Equal(t, []string{"accounts", "documents", "wiki", "lastlogin-go"}, workspace.ModulesNames())
	require.Equal(t, "1.2.3", workspace.Modules[0].Version)
	require.Equal(t, filepath.Join(root, "core"), workspace.ModuleDeclarationDir("accounts"))
	require.Equal(t, filepath.Join(root, "product"), workspace.ModuleDeclarationDir("wiki"))
	require.Len(t, workspace.Environments, 1)
	require.Equal(t, "staging", workspace.Environments[0].Name)
	documents, err := workspace.LoadModuleFromName(ctx, "documents")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "core/modules/documents"), documents.Dir())
	require.ErrorContains(t, workspace.DeleteModule(ctx, "accounts"), "owning workspace")
	require.NoError(t, workspace.Save(ctx))
	data, err := os.ReadFile(filepath.Join(root, "product/workspace.codefly.yaml"))
	require.NoError(t, err)
	var declaration map[string]any
	require.NoError(t, yaml.Unmarshal(data, &declaration))
	require.NotContains(t, declaration, "modules")
	require.Len(t, declaration["workspaces"], 1)
	require.Len(t, declaration["solutions"], 2)
	reloaded, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "product"))
	require.NoError(t, err)
	require.Equal(t, workspace.ModulesNames(), reloaded.ModulesNames())
}

func TestWorkspaceCompositionRejectsCyclesAndRepinning(t *testing.T) {
	for _, test := range []struct{ name, child, parent, want string }{
		{"cycle", "workspaces:\n  - name: product\n    path: ../product\n", "", "cycle"},
		{"duplicate", "modules:\n  - name: shared\n", "modules:\n  - name: shared\n", "conflicts"},
		{"solution collision", "modules:\n  - name: wiki\n", "solutions:\n  - name: wiki\n", "conflicts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			compositionFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\n"+test.child)
			compositionFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\n"+test.parent)
			_, err := resources.LoadWorkspaceFromDir(context.Background(), filepath.Join(root, "product"))
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestWorkspaceCompositionCannotSilentlyIgnoreUnresolvedRelease(t *testing.T) {
	root := t.TempDir()
	compositionFile(t, root, "workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: platform-core\n    source: team/platform-core\n    version: 1.0.0\n")
	_, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	require.ErrorContains(t, err, "requires host resolution")
}
