package configurations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func extensionWorkspace(t *testing.T, extension, baseFile, base, ownFile, own string) *resources.Workspace {
	t.Helper()
	root := t.TempDir()
	writeConfigurationFile(t, root, "core/workspace.codefly.yaml", "name: core\nlayout: modules\n")
	writeConfigurationFile(t, root, "core/configurations/local/"+baseFile, base)
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: core\n    path: ../core\n"+extension)
	writeConfigurationFile(t, root, "product/configurations/local/"+ownFile, own)
	ws, err := resources.LoadWorkspaceFromDir(context.Background(), filepath.Join(root, "product"))
	require.NoError(t, err)
	return ws
}

func TestWorkspaceExtensionPreservesImportedKeysAndAppendsRoutes(t *testing.T) {
	ws := extensionWorkspace(t, "configuration-extensions:\n  edge:\n    append: {MODULES: ','}\n", "edge.env", "MODULES=robin,tracing\nROBIN_REST=${endpoint:host/hop/http}\n", "edge.env", "MODULES=wiki\nWIKI_REST=${endpoint:wiki/backend/http}\n")
	provided, err := configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
	require.NoError(t, err)
	require.Len(t, provided.Infos, 1)
	values := map[string]string{}
	for _, v := range provided.Infos[0].ConfigurationValues {
		values[v.Key] = v.Value
	}
	require.Equal(t, map[string]string{"MODULES": "robin,tracing,wiki", "ROBIN_REST": "${endpoint:host/hop/http}", "WIKI_REST": "${endpoint:wiki/backend/http}"}, values)
	require.Len(t, provided.Origins, 4, "both contributors to MODULES must remain visible")
	require.Empty(t, provided.Decisions, "extension must not report whole-group replacement")
	loader, err := configurations.NewConfigurationLocalReader(t.Context(), ws)
	require.NoError(t, err)
	require.NoError(t, loader.Load(t.Context(), &resources.Environment{Name: "local"}))
	require.Equal(t, provided.Infos, loader.Configurations()[0].Infos, "run and inspection share the resolver")
}

func TestWorkspaceExtensionAssemblesFixtureGrantDocument(t *testing.T) {
	ws := extensionWorkspace(t, "configuration-extensions:\n  fixture-grants: {}\n", "fixture-grants.yaml", "roles:\n  - name: Module viewer\n    permissions: [documents.read]\nassignments:\n  modules: [viewer]\n", "fixture-grants.yaml", "roles:\n  - name: Wiki viewer\n    permissions: [wiki.read]\nassignments:\n  wiki: [viewer]\ninstallations: [wiki]\n")
	provided, err := configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(provided.Infos[0].Data.Content, &document))
	require.Len(t, document["roles"], 2)
	require.Equal(t, map[string]any{"modules": []any{"viewer"}, "wiki": []any{"viewer"}}, document["assignments"])
	require.Equal(t, []any{"wiki"}, document["installations"])
	require.Len(t, provided.Origins, 2)
	require.Equal(t, "../core/configurations/local/fixture-grants.yaml", provided.Origins[0].File)
}

func TestWorkspaceExtensionRefusesConflictsByName(t *testing.T) {
	for _, tc := range []struct{ name, rule, baseFile, base, ownFile, own, want string }{
		{"env collision", "{}", "edge.env", "UPSTREAM=base\n", "edge.env", "UPSTREAM=own\n", "key UPSTREAM"},
		{"equal collision", "{}", "edge.env", "UPSTREAM=same\n", "edge.env", "UPSTREAM=same\n", "key UPSTREAM"},
		{"normalized collision", "{}", "edge.env", "upstream-url=base\n", "edge.env", "UPSTREAM_URL=own\n", "key UPSTREAM_URL"},
		{"duplicate key", "{}", "edge.env", "A=x\nA=y\n", "edge.env", "B=z\n", "edge/A"},
		{"append duplicate", "{append: {MODULES: ','}}", "edge.env", "MODULES=wiki\n", "edge.env", "MODULES=wiki\n", "key MODULES"},
		{"append typo", "{append: {MODULS: ','}}", "edge.env", "MODULES=core\n", "edge.env", "MODULES=wiki\n", "key MODULS"},
		{"append secret", "{append: {MODULES: ','}}", "edge.secret.env", "MODULES=secret\n", "edge.env", "MODULES=wiki\n", "supplied plaintext"},
		{"document collision", "{}", "edge.yaml", "routes: {wiki: base}\n", "edge.yaml", "routes: {wiki: own}\n", "key $.routes.wiki"},
		{"document type", "{}", "edge.yaml", "roles: []\n", "edge.yaml", "roles: scalar\n", "key $.roles"},
		{"duplicate yaml", "{}", "edge.yaml", "roles: []\nroles: []\n", "edge.yaml", "other: []\n", "key $.roles"},
		{"yaml alias", "{}", "edge.yaml", "roles: &roles []\ncopy: *roles\n", "edge.yaml", "other: []\n", "YAML aliases"},
		{"yaml documents", "{}", "edge.yaml", "roles: []\n---\nother: []\n", "edge.yaml", "other: []\n", "one YAML document"},
		{"mixed", "{}", "edge.yaml", "roles: []\n", "edge.env", "A=b\n", "document and env keys"},
		{"missing import", "{}", "other.env", "A=b\n", "edge.env", "B=c\n", "both an own and an imported group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := extensionWorkspace(t, "configuration-extensions:\n  edge: "+tc.rule+"\n", tc.baseFile, tc.base, tc.ownFile, tc.own)
			_, err := configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
			require.ErrorContains(t, err, tc.want)
			require.ErrorContains(t, err, "edge")
		})
	}
}

func TestWorkspaceExtensionKeepsRequirementsAndComposesTransitively(t *testing.T) {
	ws := extensionWorkspace(t, "configuration-extensions:\n  edge: {}\n", "edge.env", "REQUIRED=${profile}\n", "edge.env", "WIKI=own\n")
	provided, err := configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
	require.NoError(t, err)
	require.Len(t, provided.Unsupplied, 1)
	require.Equal(t, "REQUIRED", provided.Unsupplied[0].Key)
	root := filepath.Dir(ws.Dir())
	writeConfigurationFile(t, root, "outer/workspace.codefly.yaml", "name: outer\nlayout: modules\nworkspaces:\n  - name: product\n    path: ../product\nconfiguration-extensions:\n  edge: {}\n")
	writeConfigurationFile(t, root, "outer/configurations/local/edge.env", "EXTRA=outer\n")
	outer, err := resources.LoadWorkspaceFromDir(t.Context(), filepath.Join(root, "outer"))
	require.NoError(t, err)
	provided, err = configurations.ReadWorkspaceConfigurations(t.Context(), outer, &resources.Environment{Name: "local"})
	require.NoError(t, err)
	require.Len(t, provided.Infos[0].ConfigurationValues, 3)
	require.Len(t, provided.Origins, 3)
	require.Len(t, provided.Unsupplied, 1)
}

func TestWorkspaceExtensionProfilesAndSiblingAmbiguity(t *testing.T) {
	ws := extensionWorkspace(t, "configuration-extensions:\n  edge: {append: {MODULES: ','}}\n", "edge.env", "MODULES=core\n", "edge.env", "MODULES=wiki\n")
	root := filepath.Dir(ws.Dir())
	writeConfigurationFile(t, root, "core/configurations/base/edge.env", "MODULES=old\n")
	writeConfigurationFile(t, root, "core/configurations/local/profile.codefly.yaml", "derives-from: base\n")
	provided, err := configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
	require.NoError(t, err)
	require.Len(t, provided.Decisions, 1)
	require.True(t, provided.Decisions[0].Final, "profile-selected value contributes to the appended key")
	writeConfigurationFile(t, root, "sibling/workspace.codefly.yaml", "name: sibling\nlayout: modules\n")
	writeConfigurationFile(t, root, "sibling/configurations/local/edge.env", "MODULES=different\n")
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: core\n    path: ../core\n  - name: sibling\n    path: ../sibling\nconfiguration-extensions:\n  edge: {append: {MODULES: ','}}\n")
	ws, err = resources.LoadWorkspaceFromDir(t.Context(), ws.Dir())
	require.NoError(t, err)
	_, err = configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
	require.ErrorContains(t, err, `workspace configuration "edge" differs between workspaces "core" and "sibling"`)
}

func TestWorkspaceExtensionSecretDocumentAndStrictDeclaration(t *testing.T) {
	ws := extensionWorkspace(t, "configuration-extensions:\n  grants: {}\n", "grants.secret.yaml", "module: hidden-value\n", "grants.yaml", "solution: own\n")
	provided, err := configurations.ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "local"})
	require.NoError(t, err)
	require.True(t, provided.Infos[0].Data.Secret)
	require.Len(t, provided.Origins, 2)
	var declaration resources.Workspace
	require.ErrorContains(t, yaml.Unmarshal([]byte("configuration-extensions:\n  edge: {apend: {MODULES: ','}}\n"), &declaration), "unknown field")
}
