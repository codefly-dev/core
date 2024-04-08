package configurations_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionproject "github.com/codefly-dev/core/actions/project"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestProjectValidation(t *testing.T) {
	ctx := context.Background()

	tcs := []struct {
		name    string
		project string
	}{
		{"normal", "project"},
		{"with -", "my-project"},
		{"ending in 0", "my-project0"},
		{"ending in -0", "my-project-0"},
		{"start with 0", "0-project"},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := configurations.NewProject(ctx, tc.project)
			assert.NoError(t, err)
		})
	}

	tcs = []struct {
		name    string
		project string
	}{
		{"too short", "pr"},
		{"with _", "my_project"},
		{"with .", "my.project"},
		{"with spaces", "my project"},
		{"with two --", "my--project"},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := configurations.NewProject(ctx, tc.project)
			assert.Error(t, err)
		})
	}
}

func TestCreationProject(t *testing.T) {
	ctx := context.Background()

	projectPath := t.TempDir()
	defer func() {
		os.RemoveAll(projectPath)
	}()

	var action actions.Action
	var err error

	projectName := "test-project"
	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: projectName,
		Path: projectPath,
	})
	assert.NoError(t, err)

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	project, err := actions.As[configurations.Project](out)
	assert.NoError(t, err)
	assert.Equal(t, projectName, project.Name)
	assert.Equal(t, path.Join(projectPath, projectName), project.Dir())

	// Creating again should return an error
	out, err = action.Run(ctx)
	assert.Error(t, err)

	// Check that we have a configuration
	_, err = os.Stat(path.Join(project.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)

	// Check that we have the applications and providers folders
	_, err = os.Stat(path.Join(project.Dir(), "applications"))
	assert.NoError(t, err)
	_, err = os.Stat(path.Join(project.Dir(), "configurations"))
	assert.NoError(t, err)

	// Check that we have a README
	_, err = os.Stat(path.Join(project.Dir(), "README.md"))
	assert.NoError(t, err)

	projectConfig := string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.NoError(t, err)
	// We use default path
	assert.NotContains(t, projectConfig, "path")

}

func TestCreationProjectWithWorkspace(t *testing.T) {
	ctx := context.Background()

	workspace, dir := createTestWorkspace(t, ctx)

	cur, err := os.Getwd()
	assert.NoError(t, err)
	err = os.Chdir(dir)
	assert.NoError(t, err)

	defer func() {
		os.RemoveAll(dir)
		os.Chdir(cur)
	}()

	var action actions.Action

	projectDir := t.TempDir()

	assert.Equal(t, dir, workspace.Dir())

	projectName := "test-project"
	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: projectName,
		Path: projectDir,
	})
	assert.NoError(t, err)

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	project, err := actions.As[configurations.Project](out)
	assert.NoError(t, err)

	// Creating again should return an error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Add to the workspace
	action, err = actionproject.NewActionAddProjectToWorkspace(ctx, &actionsv0.AddProjectToWorkspace{
		Name:      projectName,
		Workspace: workspace.Name,
		Path:      project.Dir(),
	})

	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)

	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)

	assert.True(t, workspace.HasProject(projectName))

	action, err = actionproject.NewActionSetProjectActive(ctx, &actionproject.SetProjectActive{
		Name:      projectName,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)
	assert.Equal(t, projectName, workspace.ActiveProject)

	// Load project
	project, err = workspace.LoadProjectFromName(ctx, projectName)
	assert.NoError(t, err)
	assert.Equal(t, projectName, project.Name)
	assert.Equal(t, path.Join(projectDir, projectName), project.Dir())

	project, err = workspace.LoadProjectFromDir(ctx, project.Dir())
	assert.NoError(t, err)
	assert.Equal(t, projectName, project.Name)

	// Check that we have a configuration
	_, err = os.Stat(path.Join(project.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)

	// Check that we have a README
	_, err = os.Stat(path.Join(project.Dir(), "README.md"))
	assert.NoError(t, err)

	projectConfig := string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.NoError(t, err)
	// We use default path
	assert.NotContains(t, projectConfig, "path")

	ref := &configurations.ProjectReference{Name: projectName, Path: project.Dir()}
	back, err := workspace.LoadProjectFromReference(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, project.Name, back.Name)

	// Check that the active project is the one we created
	active, err := workspace.LoadActiveProject(ctx)
	assert.NoError(t, err)
	assert.Equal(t, project.Name, active.Name)

	all, err := workspace.LoadProjects(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(all))

	// Add another project
	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: "test-project-2",
		Path: projectDir,
	})
	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)
	recent, err := actions.As[configurations.Project](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-project-2", recent.Name)

	// Add to the workspace
	action, err = actionproject.NewActionAddProjectToWorkspace(ctx, &actionsv0.AddProjectToWorkspace{
		Name:      recent.Name,
		Workspace: workspace.Name,
		Path:      recent.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)
	assert.True(t, workspace.HasProject(recent.Name))
	assert.Equal(t, 2, len(workspace.Projects))

	// Make active
	action, err = actionproject.NewActionSetProjectActive(ctx, &actionproject.SetProjectActive{
		Name:      recent.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)

	// Check that the active project is the latest one
	active, err = workspace.LoadActiveProject(ctx)
	assert.NoError(t, err)
	assert.Equal(t, recent.Name, active.Name)

	all, err = workspace.LoadProjects(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(all))

	// Change the active project
	action, err = actionproject.NewActionSetProjectActive(ctx, &actionproject.SetProjectActive{
		Name:      projectName,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)

	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)
	assert.Equal(t, projectName, workspace.ActiveProject)
}

func TestAddingExistingProjectAbsolutePath(t *testing.T) {
	ctx := context.Background()
	workspace, dir := createTestWorkspace(t, ctx)
	defer os.RemoveAll(dir)

	project, err := configurations.LoadProjectFromDir(ctx, "testdata/project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", project.Name)
	cur, _ := os.Getwd()
	assert.Equal(t, path.Join(cur, "testdata/project"), project.Dir())

	// Adding this project to the workspace should result in absolute path
	err = workspace.AddProjectReference(ctx, project.Reference())
	assert.NoError(t, err)

	wsConfig := string(shared.Must(os.ReadFile(path.Join(dir, configurations.WorkspaceConfigurationName))))

	assert.NotContains(t, wsConfig, "path: testdata/project")
	assert.Contains(t, wsConfig, fmt.Sprintf("path: %s", project.Dir()))
}

func TestProjectLoading(t *testing.T) {
	ctx := context.Background()
	ws := &configurations.Workspace{}

	p, err := ws.LoadProjectFromDir(ctx, "testdata/project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", p.Name)
	assert.Equal(t, 2, len(p.Applications))
	assert.Equal(t, "web", p.Applications[0].Name)
	assert.Equal(t, "management", p.Applications[1].Name)

	// Save and make sure we preserve the "active application" convention
	tmpDir := t.TempDir()

	err = p.SaveToDirUnsafe(ctx, tmpDir)
	assert.NoError(t, err)

	p, err = ws.LoadProjectFromDir(ctx, tmpDir)
	assert.NoError(t, err)
}

func TestProjectLoadingFromPath(t *testing.T) {
	ctx := context.Background()
	ws := &configurations.Workspace{}

	cur, err := os.Getwd()
	assert.NoError(t, err)

	err = os.Chdir(path.Join(cur, "testdata/project"))
	assert.NoError(t, err)
	defer os.Chdir(cur)

	p, err := configurations.LoadProjectFromPath(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", p.Name)
	assert.Equal(t, 2, len(p.Applications))
	assert.Equal(t, "web", p.Applications[0].Name)
	assert.Equal(t, "management", p.Applications[1].Name)

	// Save and make sure we preserve the "active application" convention
	tmpDir := t.TempDir()

	err = p.SaveToDirUnsafe(ctx, tmpDir)
	assert.NoError(t, err)

	_, err = os.ReadFile(path.Join(tmpDir, configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	p, err = ws.LoadProjectFromDir(ctx, tmpDir)
	assert.NoError(t, err)
}
