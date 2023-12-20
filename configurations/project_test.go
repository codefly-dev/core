package configurations_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionproject "github.com/codefly-dev/core/actions/project"
	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestBadInputs(t *testing.T) {
	ctx := context.Background()
	tcs := []struct {
		name    string
		project string
	}{
		{"too short", "pr"},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := actionproject.NewActionAddProject(ctx, &actionsv1.AddProject{
				Name: tc.project,
			})
			assert.Error(t, err)
		})
	}
}

func TestAddingExistingProjectAbsolutePath(t *testing.T) {
	ctx := context.Background()
	workspace, dir := createTestWorkspace(t, ctx)
	defer os.RemoveAll(dir)

	project, err := configurations.LoadProjectFromDirUnsafe(ctx, "testdata/project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", project.Name)
	cur, _ := os.Getwd()
	assert.Equal(t, path.Join(cur, "testdata/project"), project.Dir())

	// Adding this project to the workspace should result in absolute path
	err = workspace.AddProject(ctx, project)
	assert.NoError(t, err)

	wsConfig := string(shared.Must(os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))))

	assert.NotContains(t, wsConfig, "path: testdata/project")
	assert.Contains(t, wsConfig, fmt.Sprintf("path: %s", project.Dir()))
}

func TestAddingExistingProjectDefaultRelativePath(t *testing.T) {
	ctx := context.Background()
	// Projects root is the workspace root
	workspace, err := configurations.LoadWorkspaceFromDirUnsafe(ctx, "testdata/workspace")
	workspace.ProjectsRoot = workspace.Dir()

	assert.NoError(t, err)
	project, err := configurations.LoadProjectFromDirUnsafe(ctx, "testdata/workspace/project")
	assert.Equal(t, "project", project.Name)
	assert.NoError(t, err)
	err = workspace.AddProjectReference(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(workspace.Projects))
	ref := workspace.Projects[0]
	assert.Nil(t, ref.PathOverride)
}

func TestAddingExistingProjectNonDefaultRelativePath(t *testing.T) {
	ctx := context.Background()
	// Projects root is the workspace root
	workspace, err := configurations.LoadWorkspaceFromDirUnsafe(ctx, "testdata/workspace")
	assert.NoError(t, err)
	workspace.ProjectsRoot = workspace.Dir()
	project, err := configurations.LoadProjectFromDirUnsafe(ctx, "testdata/workspace/other_project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", project.Name)
	err = workspace.AddProjectReference(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(workspace.Projects))
	ref := workspace.Projects[0]
	assert.Equal(t, "other_project", *ref.PathOverride)
}

func TestLoading(t *testing.T) {
	ctx := context.Background()
	ws := &configurations.Workspace{}

	p, err := ws.LoadProjectFromDir(ctx, "testdata/project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", p.Name)
	assert.Equal(t, 2, len(p.Applications))
	assert.Equal(t, "web", p.Applications[0].Name)
	assert.Equal(t, "management", p.Applications[1].Name)
	assert.Equal(t, "web", *p.ActiveApplication())

	// Save and make sure we preserve the "active application" convention
	tmpDir := t.TempDir()

	err = p.SaveToDirUnsafe(ctx, tmpDir)
	assert.NoError(t, err)

	content, err := os.ReadFile(path.Join(tmpDir, configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "web*")
	p, err = ws.LoadProjectFromDir(ctx, tmpDir)
	assert.NoError(t, err)
	assert.Equal(t, "web", *p.ActiveApplication())
}

func TestCreationWithDefaultPath(t *testing.T) {
	ctx := context.Background()
	w, dir := createTestWorkspace(t, ctx)
	defer os.RemoveAll(dir)

	var action actions.Action
	var err error

	assert.Equal(t, dir, w.Dir())

	projectName := "test-project"
	action, err = actionproject.NewActionAddProject(ctx, &actionsv1.AddProject{
		Name:      projectName,
		Workspace: w.Name,
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)

	w, err = configurations.ReloadWorkspace(ctx, w)
	assert.NoError(t, err)

	project, err := actions.As[configurations.Project](out)
	assert.NoError(t, err)
	assert.Equal(t, projectName, project.Name)
	assert.Equal(t, 1, len(w.Projects))
	assert.Equal(t, projectName, w.Projects[0].Name)
	assert.Equal(t, path.Join(w.Dir(), projectName), project.Dir())

	// Creating again should return an error
	action, err = actionproject.NewActionAddProject(ctx, &actionsv1.AddProject{
		Name: projectName,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.Error(t, err)

	assert.Equal(t, 1, len(w.Projects))

	// Load Back, different ways
	project, err = w.LoadProjectFromName(ctx, projectName)
	assert.NoError(t, err)
	assert.Equal(t, projectName, project.Name)

	project, err = w.LoadProjectFromDir(ctx, path.Join(w.Dir(), projectName))
	assert.NoError(t, err)
	assert.Equal(t, projectName, project.Name)

	// Check that we have a configuration
	_, err = os.Stat(path.Join(project.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)

	// Check that we have a README
	_, err = os.Stat(path.Join(project.Dir(), "README.md"))
	assert.NoError(t, err)

	// Load the workspace configuration
	wsConfig := string(shared.Must(os.ReadFile(path.Join(w.Dir(), configurations.WorkspaceConfigurationName))))
	assert.NoError(t, err)
	// We use default path
	assert.NotContains(t, wsConfig, "path")
	assert.Contains(t, wsConfig, "name: test-project")
	assert.NotContains(t, wsConfig, "name: test-project*")

	projectConfig := string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.NoError(t, err)
	// We use default path
	assert.NotContains(t, projectConfig, "path")

	// Load -- reference
	ref := &configurations.ProjectReference{Name: projectName}
	back, err := w.LoadProjectFromReference(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, project.Name, back.Name)

	// Check that the active project is the one we created
	active, err := w.LoadActiveProject(ctx)
	assert.NoError(t, err)
	assert.Equal(t, project.Name, active.Name)

	all, err := w.LoadProjects(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(all))

	// Add another project
	action, err = actionproject.NewActionAddProject(ctx, &actionsv1.AddProject{
		Name: "test-project-2",
	})
	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)
	recent, err := actions.As[configurations.Project](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-project-2", recent.Name)

	w, err = configurations.ReloadWorkspace(ctx, w)
	assert.NoError(t, err)

	//Maintain no * in memory
	for _, ref := range w.Projects {
		assert.NotContains(t, ref.Name, "*")
	}

	assert.Equal(t, 2, len(w.Projects))

	wsConfig = string(shared.Must(os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))))
	assert.Contains(t, wsConfig, "name: test-project-2*")
	assert.NotContains(t, wsConfig, "name: test-project*")

	// Check that the active project is the latest one
	active, err = w.LoadActiveProject(ctx)
	assert.NoError(t, err)
	assert.Equal(t, recent.Name, active.Name)

	all, err = w.LoadProjects(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(all))

	// Change the active project
	action, err = actionproject.NewActionSetProjectActive(ctx, &actionproject.SetProjectActive{
		Name:      projectName,
		Workspace: w.Name,
	})
	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)

	active, err = actions.As[configurations.Project](out)
	assert.NoError(t, err)
	assert.Equal(t, projectName, active.Name)

}

func TestCreationWithAbsolutePath(t *testing.T) {
	ctx := context.Background()
	w, dir := createTestWorkspace(t, ctx)

	projectDir := t.TempDir()

	action, err := actionproject.NewActionAddProject(ctx, &actionsv1.AddProject{
		Name: "test-project",
		Path: projectDir,
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	p := shared.Must(actions.As[configurations.Project](out))
	assert.NoError(t, err)
	assert.Equal(t, "test-project", p.Name)

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
	back, err := w.LoadProjectFromReference(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, p.Name, back.Name)
}

func TestCreationWithRelativePath(t *testing.T) {
	ctx := context.Background()
	workspace, dir := createTestWorkspace(t, ctx)

	action, err := actionproject.NewActionAddProject(ctx, &actionsv1.AddProject{
		Name: "test-project",
		Path: "path-from-workspace",
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	p := shared.Must(actions.As[configurations.Project](out))
	assert.NoError(t, err)
	assert.Equal(t, "test-project", p.Name)

	// Load the workspace configuration
	ws, err := os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))
	assert.NoError(t, err)
	// We should find the path

	assert.Contains(t, string(ws), fmt.Sprintf("path: %s", "path-from-workspace"))

	content, err := os.ReadFile(path.Join(p.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), fmt.Sprintf("path: %s", "path-from-workspace"))

	// Load -- reference
	ref := &configurations.ProjectReference{Name: "test-project", PathOverride: shared.Pointer("path-from-workspace")}
	back, err := workspace.LoadProjectFromReference(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, p.Name, back.Name)
}
