package configurations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// composedGroupOverriddenBy writes the two-workspace fixture every test in this
// file reads: a composed host module shipping one group — a default, and two
// values it declares each profile must supply — and a consuming solution whose
// own file for that group holds whatever the test is about. It returns the
// solution directory.
//
// The module declares the group, so the module is where the group's keys and its
// per-profile requirements are declared; the solution's file is an override of
// that declaration, never a replacement of it.
func composedGroupOverriddenBy(t *testing.T, override string) string {
	t.Helper()
	return composedGroupDeclaredAsOverriddenBy(t,
		"CONFIG_DIR=/etc/app\nCONFIG_FILE=${profile}\nCONFIG_MODE=${profile}\n", override, "")
}

// composedGroupDeclaredAsOverriddenBy is composedGroupOverriddenBy with the
// module's own declaration of the group under the test's control, and with a
// second file of the solution's contributing to the same group name — both of
// which a test about one key declared twice needs, since a duplicate arrives
// either from a repeated line or from two files consolidated into one group.
func composedGroupDeclaredAsOverriddenBy(t *testing.T, declaration, override, overrideSecret string) string {
	t.Helper()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host
    path: ../host
`)
	if override != "" {
		writeConfigurationFile(t, root, "solution/configurations/local/app-config.env", override)
	}
	if overrideSecret != "" {
		writeConfigurationFile(t, root, "solution/configurations/local/app-config.secret.env", overrideSecret)
	}

	writeConfigurationFile(t, root, "host/module.codefly.yaml", `kind: module
name: host
services:
  - name: telemetry
`)
	writeConfigurationFile(t, root, "host/configurations/local/app-config.env", declaration)
	writeConfigurationFile(t, root, "host/services/telemetry/service.codefly.yaml", `kind: service
name: telemetry
version: 0.0.0
agent:
  kind: runtime::service
  name: go-grpc
  version: 0.0.1
  publisher: codefly.ai
`)
	return filepath.Join(root, "solution")
}

func loadWorkspace(t *testing.T, ctx context.Context, dir string) *resources.Workspace {
	t.Helper()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	return workspace
}

func groupValues(t *testing.T, infos []*basev0.ConfigurationInformation, group string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, info := range infos {
		if info.GetName() != group {
			continue
		}
		for _, value := range info.GetConfigurationValues() {
			values[value.GetKey()] = value.GetValue()
		}
	}
	return values
}

// A solution that overrides one key of a composed module's group overrides THAT
// KEY. The module keeps supplying the keys the solution never mentioned — both
// its default and, decisively, the declaration that a value is supplied per
// profile, which is reported as owed and fails the load. Replacing the group
// whole discarded the default, discarded the requirement with it, and so
// returned a clean load for a composition that is missing a value no profile
// ever supplies.
func TestAWorkspaceOverrideOfAComposedGroupOverlaysPerKey(t *testing.T) {
	ctx := context.Background()
	solution := composedGroupOverriddenBy(t, "CONFIG_FILE=/etc/app/solution.yaml\n")
	workspace := loadWorkspace(t, ctx, solution)

	provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"CONFIG_DIR":  "/etc/app",
		"CONFIG_FILE": "/etc/app/solution.yaml",
		"CONFIG_MODE": configurations.ProfileValueMarker,
	}, groupValues(t, provided.Infos, "app-config"),
		"the overridden key is the solution's, every other key is still the module's")

	owed := configurations.StillUnsupplied(provided.Unsupplied, func(string) []*basev0.ConfigurationInformation {
		return provided.Infos
	})
	require.Len(t, owed, 1, "the override discharged one of the module's two requirements, and only one")
	assert.Equal(t, "app-config", owed[0].Group)
	assert.Equal(t, "CONFIG_MODE", owed[0].Key)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	err = loader.Load(ctx, resources.LocalEnvironment())
	require.Error(t, err, "a value no profile supplies fails the load, override or no override")
	assert.ErrorContains(t, err, "app-config/CONFIG_MODE")
}

// The group is the module's, and overriding keys of it does not make it the
// solution's. It stays composed, so it reaches the services that declared it as
// a dependency rather than every service of the composition.
//
// This is the half of the defect that widened delivery while the other half
// narrowed contents. It matters more now than it did before the overlay: a
// run-wide injection of an overridden group would inject the MODULE's keys —
// CONFIG_DIR here, which the solution never wrote — into every service of the
// composition. A solution that wants a group of its own in every service
// declares one under its own name, where no module's keys ride along; an
// operator who wants one value everywhere uses --set, which is attributed to the
// run and stays composition-root.
func TestAnOverriddenComposedGroupStaysComposedAndKeepsItsDeliveryScoped(t *testing.T) {
	ctx := context.Background()
	solution := composedGroupOverriddenBy(t,
		"CONFIG_FILE=/etc/app/solution.yaml\nCONFIG_MODE=solution\n")
	workspace := loadWorkspace(t, ctx, solution)

	provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"app-config": "host"}, provided.ComposedBy,
		"the module that declared the group is still the group's provider")
	assert.Empty(t, provided.Ambiguous)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	require.NoError(t, loader.Load(ctx, resources.LocalEnvironment()),
		"both per-profile values are supplied now, one by the module's own file and one by the override")
	assert.Empty(t, loader.CompositionRootWorkspaceConfigurationNames(),
		"an overridden composed group is not the composition root's own")

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	rootConfs, err := manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	assert.Empty(t, rootConfs, "nothing is injected run-wide")

	confs, err := manager.GetWorkspaceDependenciesConfigurations(ctx, "app-config")
	require.NoError(t, err)
	require.Len(t, confs, 1, "and a consumer that declares the group still receives it")
	for key, expected := range map[string]string{
		"CONFIG_DIR":  "/etc/app",
		"CONFIG_FILE": "/etc/app/solution.yaml",
		"CONFIG_MODE": "solution",
	} {
		value, err := resources.GetConfigurationValue(ctx, confs[0], "app-config", key)
		require.NoError(t, err)
		assert.Equal(t, expected, value, key)
	}
}

// An empty value is not a value. A solution that overrides a key the module
// declared as supplied per profile with nothing at all is refused NAMING the
// key: accepted, it would discharge the requirement and deliver the group with
// the key reading as the empty string, which is the until-runtime failure the
// declaration exists to refuse — and it would do it with the declaration still
// on the page.
func TestAWorkspaceOverrideCannotEmptyAValueTheComposedModuleDeclaresPerProfile(t *testing.T) {
	ctx := context.Background()
	solution := composedGroupOverriddenBy(t, "CONFIG_FILE=\n")
	workspace := loadWorkspace(t, ctx, solution)

	_, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.Error(t, err)
	require.ErrorIs(t, err, configurations.ErrEmptyProfileValue)
	assert.ErrorContains(t, err, "app-config/CONFIG_FILE")
	assert.ErrorContains(t, err, `"host"`, "the diagnostic names the module whose declaration is being emptied")

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	require.ErrorIs(t, loader.Load(ctx, resources.LocalEnvironment()), configurations.ErrEmptyProfileValue,
		"the load fails too, so no render can reach a workload from this composition")
}

// One key declared twice has no single value, and at this boundary it is refused
// rather than resolved by position. It is how the empty-value refusal above was
// bypassed: an override reading
//
//	CONFIG_FILE=/etc/app/solution.yaml
//	CONFIG_FILE=
//
// discharged the marker with the first entry, and the second then found no
// marker left to protect — because requiredness was read from the group being
// built instead of from the module's declaration — so the group was delivered
// with the key reading as the empty string, the exact failure the marker
// refuses, from a file that names the key twice on the page.
//
// Both halves of that are closed here: a duplicate is refused before any rule
// reads a value, and every rule reads the module's declaration as it stands
// before the overlay. A duplicate reaches this function from a repeated line or
// from two of the solution's files consolidated into one group — the parsers
// append every declaration they read — and neither spelling of the key has to
// match the other's, since one key is one key however it is written.
func TestAWorkspaceOverrideDeclaringOneKeyTwiceIsRefusedRatherThanResolvedByOrder(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name     string
		override string
		secret   string
	}{
		{
			name:     "the same spelling twice, the second emptying the first",
			override: "CONFIG_FILE=/etc/app/solution.yaml\nCONFIG_FILE=\nCONFIG_MODE=solution\n",
		},
		{
			name:     "two spellings of one key, matched the way every lookup matches",
			override: "CONFIG_FILE=/etc/app/solution.yaml\nconfig-file=\nCONFIG_MODE=solution\n",
		},
		{
			name:     "two spellings carrying two real values, with nothing to choose between them",
			override: "CONFIG_FILE=/etc/app/solution.yaml\nconfig_file=/etc/app/other.yaml\nCONFIG_MODE=solution\n",
		},
		{
			name:     "two of the solution's files consolidated into one group",
			override: "CONFIG_FILE=/etc/app/solution.yaml\nCONFIG_MODE=solution\n",
			secret:   "CONFIG_FILE=\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			solution := composedGroupDeclaredAsOverriddenBy(t,
				"CONFIG_DIR=/etc/app\nCONFIG_FILE=${profile}\nCONFIG_MODE=${profile}\n", test.override, test.secret)
			workspace := loadWorkspace(t, ctx, solution)

			_, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
			require.Error(t, err, "a key declared twice is never resolved by which line came last")
			require.ErrorIs(t, err, configurations.ErrConfigurationConflict)
			assert.ErrorContains(t, err, "app-config/CONFIG_FILE", "the diagnostic names the key, under the spelling the file used")

			loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
			require.NoError(t, err)
			require.Error(t, loader.Load(ctx, resources.LocalEnvironment()),
				"the load fails rather than discharging the module's requirement with an empty value")
		})
	}
}

// The same rule on the module's side of the boundary, where the duplicate is not
// the solution's to fix. Replacing the group whole made it invisible: the
// module's group was discarded, duplicate and all. Overlaying onto it has to
// pick a declaration to overlay — and a solution that supplies the key would
// otherwise be told the key is still owed, because the second declaration still
// carries the marker. Naming the module's duplicate says what is actually
// wrong, to the only author who can fix it.
func TestAComposedModuleDeclaringOneKeyTwiceIsRefusedWhereItIsOverridden(t *testing.T) {
	ctx := context.Background()
	solution := composedGroupDeclaredAsOverriddenBy(t,
		"CONFIG_DIR=/etc/app\nCONFIG_FILE=${profile}\nconfig-file=${profile}\n",
		"CONFIG_FILE=/etc/app/solution.yaml\n", "")
	workspace := loadWorkspace(t, ctx, solution)

	_, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.Error(t, err)
	require.ErrorIs(t, err, configurations.ErrConfigurationConflict)
	assert.ErrorContains(t, err, "app-config/CONFIG_FILE")
	assert.ErrorContains(t, err, "app-config/config-file", "the diagnostic names both declarations")
	assert.ErrorContains(t, err, `"host"`, "and the module that carries them")
}

// The module's group is the declared SET of its keys, as a base profile is for
// the profiles derived from it. A key only the solution carries is refused where
// it is written: the group is delivered to the module's services, which read the
// keys the module declared, so a key the declaration never mentions is a value
// nothing reads — and the remedy is named, since a solution that wants a key of
// its own has a group of its own to put it in.
func TestAWorkspaceOverrideCannotIntroduceAKeyTheComposedGroupDoesNotDeclare(t *testing.T) {
	ctx := context.Background()
	solution := composedGroupOverriddenBy(t, "CONFIG_FILE=/etc/app/solution.yaml\nCONFIG_EXTRA=solution\n")
	workspace := loadWorkspace(t, ctx, solution)

	_, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.Error(t, err)
	require.ErrorIs(t, err, configurations.ErrUndeclaredProfileKey)
	assert.ErrorContains(t, err, "app-config/CONFIG_EXTRA")
	assert.ErrorContains(t, err, configurations.ProfileValueMarker, "the diagnostic names the remedy")
}

// Keys are matched the way every other configuration lookup matches them — case
// insensitively, with "-" and "_" equivalent — so an override is an override of
// the module's key however the solution's file spells it, rather than a second
// key beside it that the undeclared-key rule then refuses. The declaration
// replaces the whole value, its spelling included, exactly as a derived
// profile's does; nothing downstream reads the difference, since every lookup
// matches either spelling and the delivered environment variable is normalized
// (resources.NameToKey).
func TestAWorkspaceOverrideMatchesTheComposedGroupsKeysTheWayEveryLookupDoes(t *testing.T) {
	ctx := context.Background()
	solution := composedGroupOverriddenBy(t, "config-file=/etc/app/solution.yaml\nconfig-mode=solution\n")
	workspace := loadWorkspace(t, ctx, solution)

	provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"CONFIG_DIR":  "/etc/app",
		"config-file": "/etc/app/solution.yaml",
		"config-mode": "solution",
	}, groupValues(t, provided.Infos, "app-config"),
		"one key per declaration of the module's group, carrying the solution's value")
	assert.Empty(t, configurations.StillUnsupplied(provided.Unsupplied, func(string) []*basev0.ConfigurationInformation {
		return provided.Infos
	}), "the marker is discharged through the match, not left standing beside a second key")

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	require.NoError(t, loader.Load(ctx, resources.LocalEnvironment()))
	conf, err := resources.FindWorkspaceConfiguration(ctx, loader.Configurations(), "app-config")
	require.NoError(t, err)
	value, err := resources.GetConfigurationValue(ctx, conf, "app-config", "CONFIG_FILE")
	require.NoError(t, err)
	assert.Equal(t, "/etc/app/solution.yaml", value, "a reader spelling the key the module's way still finds it")
	variables, err := resources.ConfigurationAsEnvironmentVariables(conf, "local", false)
	require.NoError(t, err)
	var delivered []string
	for _, variable := range variables {
		delivered = append(delivered, variable.Key)
	}
	assert.ElementsMatch(t,
		[]string{"CODEFLY__WORKSPACE_CONFIGURATION__APP_CONFIG__CONFIG_DIR",
			"CODEFLY__WORKSPACE_CONFIGURATION__APP_CONFIG__CONFIG_FILE",
			"CODEFLY__WORKSPACE_CONFIGURATION__APP_CONFIG__CONFIG_MODE"},
		delivered, "and the workload receives the keys under one normalized name either way")
}

// Two composed modules that disagree on a group are ambiguous, and the
// diagnostic's own remedy is a declaration in the consuming workspace. So a
// workspace that has one resolves the ambiguity rather than being reported it:
// there is no single group to overlay onto — that is precisely what the two
// modules disagree about — and the workspace's declaration stands whole, as the
// composition root's own, reaching every service of the composition.
func TestAWorkspaceDeclarationResolvesAnAmbiguityBetweenComposedModules(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host-a
    path: ../host-a
  - name: host-b
    path: ../host-b
`)
	writeConfigurationFile(t, root, "solution/configurations/local/legal.env", "LEGAL_URL=solution-legal\n")
	for _, host := range []string{"host-a", "host-b"} {
		writeConfigurationFile(t, root, host+"/module.codefly.yaml", "kind: module\nname: "+host+"\nservices: []\n")
		writeConfigurationFile(t, root, host+"/configurations/local/legal.env", "LEGAL_URL="+host+"-legal\n")
	}

	workspace := loadWorkspace(t, ctx, filepath.Join(root, "solution"))
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.NoError(t, err)
	assert.Empty(t, provided.Ambiguous, "the workspace's declaration is the remedy the diagnostic names")
	assert.Empty(t, provided.ComposedBy, "with no single module definition to overlay, the declaration stands whole")
	assert.Equal(t, map[string]string{"LEGAL_URL": "solution-legal"}, groupValues(t, provided.Infos, "legal"))

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	require.NoError(t, loader.Load(ctx, resources.LocalEnvironment()))
	assert.Equal(t, []string{"legal"}, loader.CompositionRootWorkspaceConfigurationNames(),
		"it is the composition root's own group, injected run-wide")
}

// A structured document has no keys to overlay, so a solution's document
// replaces the module's whole — as a derived profile's does. The declaration
// that each profile supplies the document is still a declaration: an empty
// document supplies nothing and is refused naming the group, and a boundary that
// turns a document into key/value pairs is a conflict rather than a silent
// choice between the two shapes.
func TestAWorkspaceOverrideOfAComposedDocumentReplacesItWhole(t *testing.T) {
	ctx := context.Background()

	fixture := func(t *testing.T, override string, overrideName string) *resources.Workspace {
		t.Helper()
		root := t.TempDir()
		writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: host
    path: ../host
`)
		writeConfigurationFile(t, root, "solution/configurations/local/"+overrideName, override)
		writeConfigurationFile(t, root, "host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
		writeConfigurationFile(t, root, "host/configurations/local/policy.yaml", "mode: ${profile}\n")
		return loadWorkspace(t, ctx, filepath.Join(root, "solution"))
	}

	t.Run("supplied", func(t *testing.T) {
		provided, err := configurations.ReadWorkspaceConfigurations(ctx, fixture(t, "mode: solution\n", "policy.yaml"), resources.LocalEnvironment())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"policy": "host"}, provided.ComposedBy)
		var content string
		for _, info := range provided.Infos {
			if info.GetName() == "policy" {
				content = string(info.GetData().GetContent())
			}
		}
		assert.Equal(t, "mode: solution\n", content)
	})

	t.Run("emptied", func(t *testing.T) {
		_, err := configurations.ReadWorkspaceConfigurations(ctx, fixture(t, "", "policy.yaml"), resources.LocalEnvironment())
		require.ErrorIs(t, err, configurations.ErrEmptyProfileValue)
		assert.ErrorContains(t, err, "policy")
	})

	t.Run("shape conflict", func(t *testing.T) {
		_, err := configurations.ReadWorkspaceConfigurations(ctx, fixture(t, "MODE=solution\n", "policy.env"), resources.LocalEnvironment())
		require.ErrorIs(t, err, configurations.ErrConfigurationConflict)
		assert.ErrorContains(t, err, "policy")
	})
}

// The overlay is the consuming workspace's own declaration over a composed
// module's group. A workspace-level declaration a COMPOSED WORKSPACE also
// carries — the product model — still replaces a module's group of that name
// whole, deliberately: an inherited declaration is not the product's to amend,
// and overlaying the product's own onto the module's group where a composed
// workspace declares the name too would rank a module default above the
// inherited declaration it is supposed to sit below. Deciding the product
// model's precedence per key is a separate change; this test is here so the
// boundary of this one is visible rather than discovered.
func TestAnInheritedGroupStillReplacesAComposedModuleGroupWhole(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeConfigurationFile(t, root, "product/workspace.codefly.yaml", `name: product
layout: modules
workspaces:
  - name: platform-core
    path: ../core
modules:
  - name: host
    path: ../host
`)
	writeConfigurationFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\n")
	writeConfigurationFile(t, root, "core/configurations/local/app-config.env", "CONFIG_DIR=/platform\n")
	writeConfigurationFile(t, root, "host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
	writeConfigurationFile(t, root, "host/configurations/local/app-config.env",
		"CONFIG_DIR=/etc/app\nCONFIG_FILE=/etc/app/host.yaml\n")

	workspace := loadWorkspace(t, ctx, filepath.Join(root, "product"))
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"CONFIG_DIR": "/platform"},
		groupValues(t, provided.Infos, "app-config"),
		"the inherited declaration stands whole; the module's CONFIG_FILE is not overlaid under it")
	assert.Empty(t, provided.ComposedBy, "and the group is the workspace level's, not the module's")
}
