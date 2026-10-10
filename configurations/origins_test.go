package configurations_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestConfigurationEvidenceDoesNotDependOnValuesReferencesOrContent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata/origins")))
	ctx := context.Background()
	workspace := loadOriginWorkspace(t, ctx, dir)
	env := &resources.Environment{Name: "staging", ConfigurationProfile: "staging"}
	read := func() *configurations.WorkspaceConfigurations {
		t.Helper()
		provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, env)
		require.NoError(t, err)
		require.Len(t, provided.Origins, 6)
		require.Len(t, provided.Decisions, 6)
		require.Len(t, provided.ProfileSelections, 1)
		return provided
	}
	// Exercise both protobuf transports and Go JSON. The evidence contract is
	// separate from Infos, which intentionally still holds the supplied values.
	encode := func(provided *configurations.WorkspaceConfigurations) []string {
		t.Helper()
		var messages []proto.Message
		for _, origin := range provided.Origins {
			messages = append(messages, origin)
		}
		for _, decision := range provided.Decisions {
			messages = append(messages, decision)
		}
		for _, selection := range provided.ProfileSelections {
			messages = append(messages, selection)
		}
		var encoded []string
		for _, message := range messages {
			wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
			require.NoError(t, err)
			jsonWire, err := protojson.Marshal(message)
			require.NoError(t, err)
			goJSON, err := json.Marshal(message)
			require.NoError(t, err)
			for _, payload := range [][]byte{wire, jsonWire, goJSON} {
				require.NotContains(t, string(payload), "private-canary")
				require.NotContains(t, string(payload), "op://")
				encoded = append(encoded, string(payload))
			}
		}
		return encoded
	}
	before := read()
	want := encode(before)
	// Change every selected AND shadowed value, including reference-only and
	// inline-secret documents. Any value-derived fingerprint would change too.
	require.NoError(t, filepath.WalkDir(dir, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		return os.WriteFile(file, []byte(strings.ReplaceAll(string(content), "origin-private-canary", "rotated-private-canary")), 0600)
	}))
	after := read()
	for i := range before.Infos {
		require.False(t, proto.Equal(before.Infos[i], after.Infos[i]), "fixture must actually change each supplied group")
	}
	require.Equal(t, want, encode(after), "origins, decisions and profile selections must not expose values or content hashes")

	relocated := t.TempDir()
	require.NoError(t, os.CopyFS(relocated, os.DirFS(dir)))
	workspace = loadOriginWorkspace(t, ctx, relocated)
	require.Equal(t, want, encode(read()), "exported evidence must not expose the absolute host path")
}

func TestConfigurationOriginsFollowEqualValuedOverridesAndDefaults(t *testing.T) {
	ctx := context.Background()
	dir := originFixture(t, "KEEP=private-default\nOVERRIDE=same-value\nTOKEN=default\n", "OVERRIDE=same-value\n", "TOKEN=private-secret\n")
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, dir), resources.LocalEnvironment())
	require.NoError(t, err)
	byKey := map[string]string{}
	for _, origin := range provided.Origins {
		byKey[origin.Key] = origin.File
	}
	require.Equal(t, "../host/configurations/local/app-config.env", byKey["KEEP"])
	require.Equal(t, "configurations/local/app-config.env", byKey["OVERRIDE"], "equal values must not erase override identity")
	require.Equal(t, "configurations/local/app-config.secret.env", byKey["TOKEN"])
	encoded, err := json.Marshal(provided.Origins)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-default")
	require.NotContains(t, string(encoded), "same-value")
	require.NotContains(t, string(encoded), "private-secret")
	require.Len(t, provided.Decisions, 2)
	d := provided.Decisions[0]
	require.Equal(t, basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_KEY_REPLACES_MODULE_DEFAULT, d.Rule)
	require.Equal(t, "host", d.Module)
	require.Len(t, d.Selected, 1)
	require.Len(t, d.Shadowed, 1)
	require.Equal(t, "OVERRIDE", d.Shadowed[0].Key)
	require.True(t, d.Final)
	require.Equal(t, "../host/configurations/local/app-config.env", d.Shadowed[0].File)
	decisions, err := json.Marshal(provided.Decisions)
	require.NoError(t, err)
	require.NotContains(t, string(decisions), "private-default")
	require.NotContains(t, string(decisions), "same-value")
	require.NotContains(t, string(decisions), "private-secret")
}

func TestConfigurationOriginsFollowProfileDerivation(t *testing.T) {
	ctx := context.Background()
	dir := originFixture(t, "KEEP=base\nOVERRIDE=base\n", "OVERRIDE=local\n", "")
	writeConfigurationFile(t, dir, "configurations/staging/profile.codefly.yaml", "derives-from: local\n")
	writeConfigurationFile(t, dir, "configurations/staging/app-config.env", "OVERRIDE=staging\n")
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, dir), &resources.Environment{Name: "staging", ConfigurationProfile: "staging"})
	require.NoError(t, err)
	for _, origin := range provided.Origins {
		if origin.Key == "OVERRIDE" {
			require.Equal(t, "configurations/staging/app-config.env", origin.File)
			return
		}
	}
	t.Fatal("missing overridden origin")
}

func TestConfigurationOriginsFollowWholeDocumentReplacement(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "workspace.codefly.yaml", "name: documents\nlayout: modules\n")
	writeConfigurationFile(t, dir, "configurations/local/policy.yaml", "mode: private-base\n")
	writeConfigurationFile(t, dir, "configurations/staging/profile.codefly.yaml", "derives-from: local\n")
	writeConfigurationFile(t, dir, "configurations/staging/policy.yaml", "mode: private-override\n")
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, dir), &resources.Environment{Name: "staging", ConfigurationProfile: "staging"})
	require.NoError(t, err)
	require.Equal(t, []*configurations.ConfigurationOrigin{{Group: "policy", File: "configurations/staging/policy.yaml", Document: true}}, provided.Origins)
	require.Len(t, provided.Decisions, 1)
	require.Equal(t, basev0.ConfigurationRule_CONFIGURATION_RULE_PROFILE_DOCUMENT_REPLACES_BASE_DOCUMENT, provided.Decisions[0].Rule)
	require.True(t, provided.Decisions[0].Final)
	require.Equal(t, "configurations/local/policy.yaml", provided.Decisions[0].Shadowed[0].File)
}

func TestConfigurationProfileDecisionsFollowIndividualValueIdentity(t *testing.T) {
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "workspace.codefly.yaml", "name: product\nlayout: modules\n")
	writeConfigurationFile(t, dir, "configurations/base/app.env", "KEEP=private-base\nURL=same-private-value\n")
	writeConfigurationFile(t, dir, "configurations/middle/profile.codefly.yaml", "derives-from: base\n")
	writeConfigurationFile(t, dir, "configurations/middle/app.env", "URL=same-private-value\n")
	writeConfigurationFile(t, dir, "configurations/staging/profile.codefly.yaml", "derives-from: middle\n")
	writeConfigurationFile(t, dir, "configurations/staging/app.env", "URL=same-private-value\n")
	ctx := context.Background()
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, dir), &resources.Environment{Name: "staging", ConfigurationProfile: "staging"})
	require.NoError(t, err)
	require.Len(t, provided.Decisions, 2)
	require.Equal(t, basev0.ConfigurationRule_CONFIGURATION_RULE_PROFILE_KEY_REPLACES_BASE_KEY, provided.Decisions[0].Rule)
	require.False(t, provided.Decisions[0].Final)
	require.True(t, provided.Decisions[1].Final)
	require.Equal(t, "staging", provided.Decisions[1].Profile)
	require.Len(t, provided.ProfileSelections, 1)
	profile := provided.ProfileSelections[0]
	require.True(t, profile.Found)
	require.Equal(t, []string{"staging"}, profile.Candidates)
	require.Equal(t, []string{"configurations/base", "configurations/middle", "configurations/staging"}, profile.Layers)
	require.Equal(t, provided.Decisions[0].Selected, provided.Decisions[1].Shadowed)
	require.Equal(t, "KEEP", provided.Origins[0].Key)
	require.Equal(t, "configurations/base/app.env", provided.Origins[0].File)
	encoded, err := json.Marshal(provided.Decisions)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
}

func loadOriginWorkspace(t *testing.T, ctx context.Context, dir string) *resources.Workspace {
	t.Helper()
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	return ws
}

func TestConfigurationDecisionsRetainNestedImportReplacement(t *testing.T) {
	root := t.TempDir()
	writeConfigurationFile(t, root, "base/workspace.codefly.yaml", "name: base\nlayout: modules\n")
	writeConfigurationFile(t, root, "middle/workspace.codefly.yaml", "name: middle\nlayout: modules\nworkspaces:\n  - name: base\n    path: ../base\n")
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: middle\n    path: ../middle\n")
	for _, name := range []string{"base", "middle", "product"} {
		writeConfigurationFile(t, root, name+"/configurations/local/wiring.env", "URL=same-private-value\n")
	}
	ctx := context.Background()
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, filepath.Join(root, "product")), resources.LocalEnvironment())
	require.NoError(t, err)
	require.Len(t, provided.Decisions, 2)
	middle, product := provided.Decisions[0], provided.Decisions[1]
	require.Equal(t, basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_GROUP_REPLACES_IMPORTED_GROUP, middle.Rule)
	require.Equal(t, "base", middle.ImportedWorkspace)
	require.Equal(t, "middle", middle.Workspace)
	require.False(t, middle.Final, "intermediate winner must not be advertised as final")
	require.Equal(t, "middle", product.ImportedWorkspace)
	require.Equal(t, "product", product.Workspace)
	require.True(t, product.Final)
	require.Equal(t, product.Shadowed, middle.Selected)
	require.Equal(t, provided.Origins, product.Selected)
	encoded, err := json.Marshal(provided.Decisions)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "same-private-value")
}
func originFixture(t *testing.T, base, ordinary, secret string) string {
	t.Helper()
	root := t.TempDir()
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nmodules:\n  - name: host\n    path: ../host\n")
	writeConfigurationFile(t, root, "host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
	writeConfigurationFile(t, root, "host/configurations/local/app-config.env", base)
	writeConfigurationFile(t, root, "product/configurations/local/app-config.env", ordinary)
	if secret != "" {
		writeConfigurationFile(t, root, "product/configurations/local/app-config.secret.env", secret)
	}
	return filepath.Join(root, "product")
}

func TestConfigurationProfileSelectionRecordsDirectoryFallbackAndAbsence(t *testing.T) {
	for _, found := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "local-fallback"}[found], func(t *testing.T) {
			dir := t.TempDir()
			writeConfigurationFile(t, dir, "workspace.codefly.yaml", "name: product\nlayout: modules\n")
			if found {
				writeConfigurationFile(t, dir, "configurations/local/app.env", "URL=private\n")
			}
			ctx := context.Background()
			provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, dir), &resources.Environment{Name: "staging", ConfigurationProfiles: []string{"staging", "local"}})
			require.NoError(t, err)
			require.Len(t, provided.ProfileSelections, 1)
			profile := provided.ProfileSelections[0]
			require.Equal(t, found, profile.Found)
			require.Equal(t, "configurations", profile.Location)
			require.Equal(t, []string{"staging", "local"}, profile.Candidates)
			if found {
				require.Equal(t, []string{"configurations/local"}, profile.Layers)
			} else {
				require.Empty(t, profile.Layers)
			}
		})
	}
}

func TestConfigurationOriginsRetainClonedModuleProfileDecisions(t *testing.T) {
	ctx := context.Background()
	dir := originFixture(t, "KEEP=base\nOVERRIDE=base\n", "OVERRIDE=product\n", "")
	host := filepath.Join(filepath.Dir(dir), "host")
	writeConfigurationFile(t, host, "configurations/staging/profile.codefly.yaml", "derives-from: local\n")
	writeConfigurationFile(t, host, "configurations/staging/app-config.env", "KEEP=derived\nOVERRIDE=derived\n")
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, dir), &resources.Environment{Name: "staging", ConfigurationProfiles: []string{"staging", "local"}})
	require.NoError(t, err)
	require.Len(t, provided.Decisions, 3)
	require.True(t, provided.Decisions[0].Final, "retained cloned KEEP preserves its profile decision")
	require.False(t, provided.Decisions[1].Final, "workspace overrides the profile's OVERRIDE")
	require.True(t, provided.Decisions[2].Final)
	require.Equal(t, "../host/configurations/staging/app-config.env", provided.Origins[0].File)
}

func TestConfigurationOriginsWorkspaceDocumentOverride(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nmodules:\n  - name: host\n    path: ../host\n")
	writeConfigurationFile(t, root, "host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
	writeConfigurationFile(t, root, "host/configurations/local/policy.yaml", "mode: private-default\n")
	writeConfigurationFile(t, root, "product/configurations/local/policy.yaml", "mode: private-override\n")
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, filepath.Join(root, "product")), resources.LocalEnvironment())
	require.NoError(t, err)
	require.Len(t, provided.Decisions, 1)
	require.Equal(t, basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_DOCUMENT_REPLACES_MODULE_DEFAULT, provided.Decisions[0].Rule)
	require.True(t, provided.Decisions[0].Final)
	require.Equal(t, provided.Origins, provided.Decisions[0].Selected)
	encoded, err := json.Marshal(provided.Decisions)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
}

func TestConfigurationDecisionsCombineModuleAndRepositoryOffers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: imported\n    path: ../imported\nmodules:\n  - name: host\n    path: ../repository/modules/host\n")
	writeConfigurationFile(t, root, "imported/workspace.codefly.yaml", "name: imported\nlayout: modules\n")
	writeConfigurationFile(t, root, "imported/configurations/local/app.env", "URL=imported\n")
	writeConfigurationFile(t, root, "repository/workspace.codefly.yaml", "name: repository\nlayout: modules\n")
	writeConfigurationFile(t, root, "repository/configurations/local/app.env", "URL=repository-default\n")
	writeConfigurationFile(t, root, "repository/modules/host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
	writeConfigurationFile(t, root, "repository/modules/host/configurations/local/app.env", "URL=module-default\n")
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, filepath.Join(root, "product")), resources.LocalEnvironment())
	require.NoError(t, err)
	require.Len(t, provided.Decisions, 1, "one replacement per module/group, even when it offers from two locations")
	decision := provided.Decisions[0]
	require.Equal(t, basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_GROUP_REPLACES_MODULE_DEFAULT, decision.Rule)
	require.Equal(t, "host", decision.Module)
	require.True(t, decision.Final)
	require.Equal(t, provided.Origins, decision.Selected)
	require.Equal(t, []*configurations.ConfigurationOrigin{
		{Group: "app", Key: "URL", File: "../repository/configurations/local/app.env"},
		{Group: "app", Key: "URL", File: "../repository/modules/host/configurations/local/app.env"},
	}, decision.Shadowed, "both replaced source locations remain visible")
}

func TestConfigurationOriginsNameFirstEqualSiblingImport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: first\n    path: ../first\n  - name: second\n    path: ../second\n")
	for _, name := range []string{"first", "second"} {
		writeConfigurationFile(t, root, name+"/workspace.codefly.yaml", "name: "+name+"\nlayout: modules\n")
		writeConfigurationFile(t, root, name+"/configurations/local/app.env", "URL=equal-private-value\n")
	}
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, loadOriginWorkspace(t, ctx, filepath.Join(root, "product")), resources.LocalEnvironment())
	require.NoError(t, err)
	require.Equal(t, []*configurations.ConfigurationOrigin{{Group: "app", Key: "URL", File: "../first/configurations/local/app.env"}}, provided.Origins)
	require.Empty(t, provided.Decisions, "equal sibling imports are deduplicated by the resolver, not a replacement rule")
}
