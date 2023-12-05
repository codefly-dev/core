package configurations_test

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionproject "github.com/codefly-dev/core/actions/project"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestCreationWithDefaultPath(t *testing.T) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)
	configurations.SetCurrentWorkspace(w)

	action := actionproject.NewAddProjectAction(&v1actions.AddProject{
		Name: "test-project",
	})
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	p := shared.Must(actions.As[configurations.Project](out))
	assert.NoError(t, err)
	assert.Equal(t, "test-project", p.Name)
	assert.Equal(t, w.Name, p.Workspace)
	assert.Equal(t, 1, len(w.Projects))
	assert.Equal(t, "test-project", w.Projects[0].Name)
	assert.Equal(t, dir, w.Dir())
	assert.Equal(t, path.Join(dir, "test-project"), p.Dir())

	// Creating again should return an error
	action = actionproject.NewAddProjectAction(&v1actions.AddProject{
		Name: "test-project",
	})
	out, err = action.Run(ctx)
	assert.Error(t, err)
	assert.Equal(t, 1, len(w.Projects))

	// Save
	err = p.Save(ctx)
	assert.NoError(t, err)
	// Check that we have a README
	_, err = os.Stat(path.Join(p.Dir(), "README.md"))
	assert.NoError(t, err)

	// Load the workspace configuration
	ws, err := os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))
	assert.NoError(t, err)
	// We use default path
	assert.NotContains(t, string(ws), "path")

	content, err := os.ReadFile(path.Join(p.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	// We use default path
	assert.NotContains(t, string(content), "path")

	// Load -- reference
	ref := &configurations.ProjectReference{Name: "test-project"}
	back, err := w.LoadProject(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, p.Name, back.Name)
	assert.Equal(t, p.Workspace, back.Workspace)
}

func TestCreationWithAbsolutePath(t *testing.T) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)
	configurations.SetCurrentWorkspace(w)

	projectDir := t.TempDir()

	action := actionproject.NewAddProjectAction(&v1actions.AddProject{
		Name: "test-project",
		Path: projectDir,
	})
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	p := shared.Must(actions.As[configurations.Project](out))
	assert.NoError(t, err)
	assert.Equal(t, "test-project", p.Name)

	// Save
	err = p.Save(ctx)
	assert.NoError(t, err)

	// Load the workspace configuration
	ws, err := os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))
	assert.NoError(t, err)
	// We should find the path
	assert.Contains(t, string(ws), fmt.Sprintf("path: %s", projectDir))

	content, err := os.ReadFile(path.Join(p.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), fmt.Sprintf("path: %s", projectDir))

	// Load -- reference
	ref := &configurations.ProjectReference{Name: "test-project", PathOverride: &projectDir}
	back, err := w.LoadProject(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, p.Name, back.Name)
	assert.Equal(t, p.Workspace, back.Workspace)
}

func TestCreationWithRelativePath(t *testing.T) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)
	configurations.SetCurrentWorkspace(w)

	action := actionproject.NewAddProjectAction(&v1actions.AddProject{
		Name: "test-project",
		Path: "path-from-workspace",
	})
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	p := shared.Must(actions.As[configurations.Project](out))
	assert.NoError(t, err)
	assert.Equal(t, "test-project", p.Name)

	// Save
	err = p.Save(ctx)
	assert.NoError(t, err)

	// Load the workspace configuration
	ws, err := os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))
	assert.NoError(t, err)
	// We should find the path

	assert.Contains(t, string(ws), fmt.Sprintf("path: %s", "path-from-workspace"))

	content, err := os.ReadFile(path.Join(p.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), fmt.Sprintf("path: %s", "path-from-workspace"))

	// Load -- reference
	ref := &configurations.ProjectReference{Name: "test-project", PathOverride: configurations.Pointer("path-from-workspace")}
	back, err := w.LoadProject(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, p.Name, back.Name)
	assert.Equal(t, p.Workspace, back.Workspace)
}

func TestLoading(t *testing.T) {
	p, err := configurations.LoadProjectFromDir("testdata/project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", p.Name)
	assert.Equal(t, 2, len(p.Applications))
	assert.Equal(t, "web", p.Applications[0].Name)
	assert.Equal(t, "management", p.Applications[1].Name)
	assert.Equal(t, "web", p.Current())

	// Save and make sure we preserve the "current application" convention
	tmpDir := t.TempDir()
	err = p.SaveToDir(tmpDir)
	assert.NoError(t, err)
	content, err := os.ReadFile(path.Join(tmpDir, configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "web*")
	p, err = configurations.LoadProjectFromDir(tmpDir)
	assert.NoError(t, err)
	assert.Equal(t, "web", p.Current())
}
