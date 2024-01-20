package configurations_test

import (
	"context"
	"fmt"
	"os"
	"path"
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
	action, err = actionproject.NewActionAddProject(ctx, &actionsv0.AddProject{
		Name:      "test-project",
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	// Action needs a workspace input
	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.AddApplication{
		Name: "test-application",
	})
	assert.Error(t, err)

	// Action needs a project input
	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.AddApplication{
		Name:      "test-application",
		Workspace: workspace.Name,
	})
	assert.Error(t, err)

	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.AddApplication{
		Name:      "test-application",
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	application, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application", application.Name)

	// Re-load: actions are out-of-memory operations
	workspace, err = configurations.ReloadWorkspace(ctx, workspace)
	assert.NoError(t, err)
	project, err = workspace.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(project.Applications))

	// Check that we have the configuration
	back, err := project.LoadApplicationFromName(ctx, "test-application")
	assert.NoError(t, err)
	assert.Equal(t, application.Name, back.Name)

	// Check the active application
	file := path.Join(workspace.Dir(), configurations.WorkspaceConfigurationName)
	content, err := os.ReadFile(file)
	assert.NoError(t, err)
	fmt.Println(string(content))

	back, err = workspace.LoadActiveApplication(ctx, project.Name)
	assert.NoError(t, err)
	assert.Equal(t, application.Name, back.Name)

	// Adding the same application should return an error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Re-load: actions are out-of-memory operations
	workspace, err = configurations.ReloadWorkspace(ctx, workspace)
	assert.NoError(t, err)
	project, err = workspace.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(project.Applications))

	// Add a second application
	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.AddApplication{
		Name:      "test-application-2",
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	application, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", application.Name)

	// Re-load: actions are out-of-memory operations
	workspace, err = configurations.ReloadWorkspace(ctx, workspace)
	assert.NoError(t, err)
	project, err = workspace.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(project.Applications))

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

	application, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application", application.Name)

	action, err = actionapplication.NewActionSetApplicationActive(ctx, &actionsv0.SetApplicationActive{
		Name:      "test-application-2",
		Project:   project.Name,
		Workspace: workspace.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	application, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", application.Name)

}
