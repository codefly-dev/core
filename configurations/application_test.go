package configurations_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionapplication "github.com/codefly-dev/core/actions/application"
	actionproject "github.com/codefly-dev/core/actions/project"
	"github.com/codefly-dev/core/configurations"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestCreationApplication(t *testing.T) {
	tmpDir := t.TempDir()

	defer func() {
		os.RemoveAll(tmpDir)
	}()
	ctx := context.Background()

	var action actions.Action
	var err error

	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: "test-project",
		Path: tmpDir,
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.NewApplication{
		Name:        "test-application",
		ProjectPath: project.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	app, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application", app.Name)

	// Check that there is a configuration file
	_, err = os.Stat(filepath.Join(project.Dir(), "applications/test-application", configurations.ApplicationConfigurationName))

	// Run again should produce error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Re-load the project
	project, err = configurations.ReloadProject(ctx, project)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(project.Applications))

	// Check that we have the configuration
	back, err := project.LoadApplicationFromName(ctx, "test-application")
	assert.NoError(t, err)
	assert.Equal(t, app.Name, back.Name)

	// Add a second application
	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.NewApplication{
		Name:        "test-application-2",
		ProjectPath: project.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	app, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", app.Name)

	project, err = configurations.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(project.Applications))
}

func TestCreationApplicationWithWorkspace(t *testing.T) {
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
	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: "test-project",
		Path: workspace.Dir(),
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	// Add to workspace
	action, err = actionproject.NewActionAddProjectToWorkspace(ctx, &actionsv0.AddProjectToWorkspace{
		Name:      project.Name,
		Workspace: workspace.Name,
		Path:      project.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	workspace = shared.Must(actions.As[configurations.Workspace](out))

	assert.Equal(t, "test-project", workspace.ActiveProject)

	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.NewApplication{
		Name:        "test-application",
		ProjectPath: project.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	app := shared.Must(actions.As[configurations.Application](out))
	assert.Equal(t, "test-application", app.Name)
	assert.Equal(t, project.Name, app.Project)
	assert.Equal(t, filepath.Join(project.Dir(), "applications/test-application"), app.Dir())

	// Running again should produce an error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Add app to workspace
	action, err = actionapplication.NewActionAddApplicationToWorkspace(ctx, &actionsv0.AddApplicationToWorkspace{
		Name:      app.Name,
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	workspace = shared.Must(actions.As[configurations.Workspace](out))

	project, err = configurations.ReloadProject(ctx, project)
	assert.NoError(t, err)

	// Check that we have the app
	back, err := project.LoadApplicationFromName(ctx, "test-application")
	assert.NoError(t, err)
	assert.Equal(t, app.Name, back.Name)

	// One app should be active

	// Check the active application
	back, err = workspace.LoadActiveApplication(ctx, project.Name)
	assert.NoError(t, err)
	assert.Equal(t, app.Name, back.Name)

	// Add a second application
	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.NewApplication{
		Name:        "test-application-2",
		ProjectPath: project.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	app, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", app.Name)

	project, err = configurations.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(project.Applications))

	// Add workspace
	action, err = actionapplication.NewActionAddApplicationToWorkspace(ctx, &actionsv0.AddApplicationToWorkspace{
		Name:      app.Name,
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	workspace = shared.Must(actions.As[configurations.Workspace](out))

	// Set active
	action, err = actionapplication.NewActionSetApplicationActive(ctx, &actionsv0.SetApplicationActive{
		Name:      app.Name,
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	workspace = shared.Must(actions.As[configurations.Workspace](out))

	// Check active is the second one
	back, err = workspace.LoadActiveApplication(ctx, project.Name)

	// Make the first one active
	action, err = actionapplication.NewActionSetApplicationActive(ctx, &actionsv0.SetApplicationActive{
		Name:      "test-application",
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)
	active, err := workspace.LoadActiveApplication(ctx, project.Name)
	assert.Equal(t, "test-application", active.Name)

	action, err = actionapplication.NewActionSetApplicationActive(ctx, &actionsv0.SetApplicationActive{
		Name:      "test-application-2",
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	workspace, err = actions.As[configurations.Workspace](out)
	assert.NoError(t, err)

	active, err = workspace.LoadActiveApplication(ctx, project.Name)
	assert.Equal(t, "test-application-2", active.Name)
}
