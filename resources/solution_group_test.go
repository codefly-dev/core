package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	compositionFile(t, root, "core/workspace.codefly.yaml", "name: platform-base\nlayout: modules\nmodules:\n  - name: accounts\n    source: team/accounts\n    version: 1.2.3\n")
	product := `name: platform-acme
layout: modules
workspaces:
  - name: platform-base
    path: ../core
solutions:
  - name: notes
    source: team/solutions
    module: solutions/notes
    version: 2.0.0
  - name: greeter-go
    source: team/solutions
    module: solutions/greeter-go
    version: 2.0.0
  - name: proposals
    source: team/solutions
    module: solutions/proposals
    version: 2.0.0
`
	knowledge := "  - id: knowledge\n    title: Knowledge\n    order: 2\n    solutions: [notes, proposals]\n"
	identity := "  - id: identity\n    title: Identity\n    order: 1\n    solutions: [greeter-go]\n"
	ctx := context.Background()
	for _, scenario := range []struct{ name, groups, refusal string }{
		{"valid", "solution-groups:\n" + knowledge + identity, ""},
		{"none", "", ""},
		{"id-twice", "solution-groups:\n" + knowledge + "  - id: knowledge\n    title: Again\n    order: 3\n    solutions: [greeter-go]\n", `solution group "knowledge" is declared twice`},
		{"module-member", "solution-groups:\n  - id: platform\n    title: Platform\n    order: 1\n    solutions: [accounts]\n", `names "accounts", which is not a solution of this composition`},
		{"unknown-member", "solution-groups:\n  - id: platform\n    title: Platform\n    order: 1\n    solutions: [billing]\n", `names "billing", which is not a solution of this composition`},
		{"solution-twice", "solution-groups:\n" + knowledge + "  - id: identity\n    title: Identity\n    order: 1\n    solutions: [notes]\n", `solution "notes" is in groups "knowledge" and "identity"`},
		{"id-invalid", "solution-groups:\n  - id: Knowledge\n    title: Knowledge\n    order: 2\n    solutions: [notes]\n", `solution group id "Knowledge" must be lowercase letters and digits separated by dashes`},
		{"title-blank", "solution-groups:\n  - id: knowledge\n    title: \"  \"\n    order: 2\n    solutions: [notes]\n", `solution group "knowledge": solution group title "  " must be one non-blank line`},
		{"title-control", "solution-groups:\n  - id: knowledge\n    title: \"Know\\u0085ledge\"\n    order: 2\n    solutions: [notes]\n", `must be one non-blank line without control characters`},
		{"title-too-large", "solution-groups:\n  - id: knowledge\n    title: " + strings.Repeat("x", resources.SolutionGroupTextMaxBytes+1) + "\n    order: 2\n    solutions: [notes]\n", `id and title are each at most 1024 bytes`},
		{"order-too-large", "solution-groups:\n  - id: knowledge\n    title: Knowledge\n    order: 2147483648\n    solutions: [notes]\n", `solution group order 2147483648 is outside -2147483648..2147483647`},
		{"order-too-small", "solution-groups:\n  - id: knowledge\n    title: Knowledge\n    order: -2147483649\n    solutions: [notes]\n", `solution group order -2147483649 is outside -2147483648..2147483647`},
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
			group, grouped := workspace.SolutionGroupOf("notes")
			if scenario.name == "none" {
				require.False(t, grouped)
				require.Nil(t, group)
				return
			}
			require.True(t, grouped)
			require.Equal(t, &resources.SolutionGroup{ID: "knowledge", Title: "Knowledge", Order: 2, Solutions: []string{"notes", "proposals"}}, group)
			group, grouped = workspace.SolutionGroupOf("greeter-go")
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
