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

// writeWorkspaceFixture copies a fixture workspace whose module directories
// exist and replaces its workspace.codefly.yaml with content.
func writeWorkspaceFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata/workspaces/interface-boundary-visibility")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte(content), 0o600))
	return dir
}

func readWorkspaceFile(t *testing.T, dir string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, resources.WorkspaceConfigurationName))
	require.NoError(t, err)
	return string(content)
}

// setAgentOverrides rebuilds the agent-overrides block from a plain map, the
// way a command that edits pins writes it: a fresh node with no comments.
func setAgentOverrides(t *testing.T, workspace *resources.Workspace, pins map[string]string) {
	t.Helper()
	var node yaml.Node
	require.NoError(t, node.Encode(pins))
	workspace.Extensions[resources.AgentOverridesKey] = resources.YAMLValue{Node: node}
}

const commentedWorkspace = `# Example Solution workspace.
# Owned by the platform team.

name: codefly-platform # the workspace name
layout: modules
# Dev pins, removed before release.
agent-overrides:
    # go-grpc carries the fix under test
    codefly.dev/go-grpc: 0.1.47 # dev pin
    # redis is pinned while a bug is open
    codefly.dev/redis: 0.0.89
    codefly.dev/postgres: 0.0.12
modules:
    # the product module
    - name: saas
    - name: platform # shared services
# end of workspace

# nothing below this line
`

func TestWorkspaceSaveKeepsComments(t *testing.T) {
	ctx := context.Background()
	dir := writeWorkspaceFixture(t, commentedWorkspace)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	// Change one pin, remove one, add one.
	setAgentOverrides(t, workspace, map[string]string{
		"codefly.dev/go-grpc":  "0.1.48",
		"codefly.dev/postgres": "0.0.12",
		"codefly.dev/nextjs":   "0.2.3",
	})
	require.NoError(t, workspace.Save(ctx))

	require.Equal(t, `# Example Solution workspace.
# Owned by the platform team.

name: codefly-platform # the workspace name
layout: modules
# Dev pins, removed before release.
agent-overrides:
    # go-grpc carries the fix under test
    codefly.dev/go-grpc: 0.1.48 # dev pin
    codefly.dev/nextjs: 0.2.3
    codefly.dev/postgres: 0.0.12
modules:
    # the product module
    - name: saas
    - name: platform # shared services
# end of workspace

# nothing below this line
`, readWorkspaceFile(t, dir))

	reloaded, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	overrides, err := reloaded.AgentOverrides()
	require.NoError(t, err)
	require.Equal(t, []resources.AgentOverride{
		{Publisher: "codefly.dev", Name: "go-grpc", Version: "0.1.48"},
		{Publisher: "codefly.dev", Name: "nextjs", Version: "0.2.3"},
		{Publisher: "codefly.dev", Name: "postgres", Version: "0.0.12"},
	}, overrides)
	require.Equal(t, workspace.Name, reloaded.Name)
	require.Equal(t, workspace.Layout, reloaded.Layout)
	require.Equal(t, []string{"saas", "platform"}, moduleReferenceNames(reloaded.Modules))

	// A second save of an unchanged workspace is a fixed point.
	before := readWorkspaceFile(t, dir)
	require.NoError(t, reloaded.Save(ctx))
	require.Equal(t, before, readWorkspaceFile(t, dir))
}

func TestWorkspaceSaveKeepsCommentsWhenAModuleIsAddedAndTheLastKeyRemoved(t *testing.T) {
	ctx := context.Background()
	dir := writeWorkspaceFixture(t, `# head
name: codefly-platform
layout: modules
modules:
    - name: saas # product
agent-overrides:
    codefly.dev/go-grpc: 0.1.47
# closing comment
`)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, workspace.AddModuleReference(&resources.ModuleReference{Name: "platform"}))
	delete(workspace.Extensions, resources.AgentOverridesKey)
	require.NoError(t, workspace.Save(ctx))

	require.Equal(t, `# head
name: codefly-platform
layout: modules
modules:
    - name: saas # product
    - name: platform
# closing comment
`, readWorkspaceFile(t, dir))
}

// commentFreeWorkspace is deliberately not in canonical form: keys out of
// order, two-space indentation, a quoted scalar. Without comments nothing is
// carried, so Save rewrites it exactly as it always has.
const commentFreeWorkspace = `layout: modules
name: "codefly-platform"
modules:
  - name: saas
  - name: platform
agent-overrides:
  codefly.dev/go-grpc: "0.1.47"
`

// canonicalCommentFreeWorkspace is what Save wrote for commentFreeWorkspace
// before comments were carried; pinned so the bytes cannot drift.
const canonicalCommentFreeWorkspace = `name: codefly-platform
layout: modules
modules:
    - name: saas
    - name: platform
agent-overrides:
    codefly.dev/go-grpc: "0.1.47"
`

func TestWorkspaceSaveWithoutCommentsIsByteIdentical(t *testing.T) {
	ctx := context.Background()
	dir := writeWorkspaceFixture(t, commentFreeWorkspace)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, workspace.Save(ctx))
	require.Equal(t, canonicalCommentFreeWorkspace, readWorkspaceFile(t, dir))

	// And it is exactly what the plain, comment-unaware saver writes.
	plain := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(plain, resources.WorkspaceConfigurationName), []byte(commentFreeWorkspace), 0o600))
	require.NoError(t, resources.SaveToDir(ctx, workspace, plain))
	require.Equal(t, readWorkspaceFile(t, plain), readWorkspaceFile(t, dir))
}

func TestWorkspaceSaveOfANewFileIsUnchanged(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.NewWorkspace(ctx, "codefly-platform", resources.LayoutKindModules)
	require.NoError(t, err)
	require.NoError(t, workspace.AddModuleReference(&resources.ModuleReference{Name: "saas"}))

	kept := t.TempDir()
	require.NoError(t, workspace.SaveToDirUnsafe(ctx, kept))
	require.Equal(t, `name: codefly-platform
layout: modules
modules:
    - name: saas
`, readWorkspaceFile(t, kept))
}

func moduleReferenceNames(refs []*resources.ModuleReference) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}
