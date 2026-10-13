package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// A solution group organises the composing product's effective solutions and
// nothing else: a module of a composed workspace is not groupable, a solution
// is in at most one group, and the group is judged after composition, against
// the composed set. The lookup is by the name the composition carries the
// solution under, which is what a renderer projects into its presence.
func TestSolutionGroupsOrganiseEffectiveSolutionsOnly(t *testing.T) {
	root := t.TempDir()
	compositionFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: accounts\n    source: team/accounts\n    version: 1.2.3\n")
	product := `name: platform-obin
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
  - name: proposals
    source: team/solutions
    module: solutions/proposals
    version: 2.0.0
`
	knowledge := "  - id: knowledge\n    title: Knowledge\n    order: 2\n    solutions: [wiki, proposals]\n"
	identity := "  - id: identity\n    title: Identity\n    order: 1\n    solutions: [lastlogin-go]\n"
	ctx := context.Background()
	for _, scenario := range []struct{ name, groups, refusal string }{
		{"valid", "solution-groups:\n" + knowledge + identity, ""},
		{"none", "", ""},
		{"id-twice", "solution-groups:\n" + knowledge + "  - id: knowledge\n    title: Again\n    order: 3\n    solutions: [lastlogin-go]\n", `solution group "knowledge" is declared twice`},
		{"module-member", "solution-groups:\n  - id: platform\n    title: Platform\n    order: 1\n    solutions: [accounts]\n", `names "accounts", which is not a solution of this composition`},
		{"unknown-member", "solution-groups:\n  - id: platform\n    title: Platform\n    order: 1\n    solutions: [billing]\n", `names "billing", which is not a solution of this composition`},
		{"solution-twice", "solution-groups:\n" + knowledge + "  - id: identity\n    title: Identity\n    order: 1\n    solutions: [wiki]\n", `solution "wiki" is in groups "knowledge" and "identity"`},
		{"id-invalid", "solution-groups:\n  - id: Knowledge\n    title: Knowledge\n    order: 2\n    solutions: [wiki]\n", `solution group id "Knowledge" must be lowercase letters and digits separated by dashes`},
		{"title-blank", "solution-groups:\n  - id: knowledge\n    title: \"  \"\n    order: 2\n    solutions: [wiki]\n", `solution group "knowledge" requires a one-line title`},
		{"no-members", "solution-groups:\n  - id: knowledge\n    title: Knowledge\n    order: 2\n    solutions: []\n", `solution group "knowledge" organises no solution`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := filepath.Join(root, "product-"+scenario.name)
			compositionFile(t, root, filepath.Join("product-"+scenario.name, "workspace.codefly.yaml"), product+scenario.groups)
			workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
			if scenario.refusal != "" {
				require.ErrorContains(t, err, scenario.refusal)
				return
			}
			require.NoError(t, err)
			group, grouped := workspace.SolutionGroupOf("wiki")
			if scenario.name == "none" {
				require.False(t, grouped)
				require.Nil(t, group)
				return
			}
			require.True(t, grouped)
			require.Equal(t, &resources.SolutionGroup{ID: "knowledge", Title: "Knowledge", Order: 2, Solutions: []string{"wiki", "proposals"}}, group)
			group, grouped = workspace.SolutionGroupOf("lastlogin-go")
			require.True(t, grouped)
			require.Equal(t, "identity", group.ID)
			for _, ungrouped := range []string{"accounts", "billing", ""} {
				_, grouped := workspace.SolutionGroupOf(ungrouped)
				require.False(t, grouped, ungrouped)
			}
			// The declaration survives a save and a reload, as the product wrote it.
			require.NoError(t, workspace.Save(ctx))
			data, err := os.ReadFile(filepath.Join(dir, "workspace.codefly.yaml"))
			require.NoError(t, err)
			require.Contains(t, string(data), "solution-groups:")
			reloaded, err := resources.LoadWorkspaceFromDir(ctx, dir)
			require.NoError(t, err)
			require.Equal(t, workspace.SolutionGroups, reloaded.SolutionGroups)
		})
	}
}
