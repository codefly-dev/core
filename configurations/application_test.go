package configurations_test

import (
	"github.com/codefly-dev/core/actions/actions"
	actionapplication "github.com/codefly-dev/core/actions/application"
	actionproject "github.com/codefly-dev/core/actions/project"
	"github.com/codefly-dev/core/configurations"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"testing"
)

func TestCreationApplication(t *testing.T) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)
	defer os.RemoveAll(dir)

	var action actions.Action
	var err error
	action, err = actionproject.NewActionAddProject(ctx, &v1actions.AddProject{
		Name:        "test-project",
		InWorkspace: w.Name,
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	// Action needs a project
	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name: "test-application",
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.Error(t, err)

	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name:      "test-application",
		InProject: project.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	application, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application", application.Name)

	projectConfig := string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.Contains(t, projectConfig, "name: test-application")
	assert.NotContains(t, projectConfig, "name: test-application*")
	assert.NotContains(t, projectConfig, "path:") // use default path

	// ReloadProject
	project, err = w.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(project.Applications))

	// Check that we have the configuration
	back, err := project.LoadApplicationFromName(ctx, "test-application")
	assert.NoError(t, err)
	assert.Equal(t, application.Name, back.Name)

	// Check the active application
	back, err = project.LoadActiveApplication(ctx)
	assert.NoError(t, err)
	assert.Equal(t, application.Name, back.Name)

	// Adding the same application should return an error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	projectConfig = string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.Contains(t, projectConfig, "name: test-application")

	project, err = w.ReloadProject(ctx, project)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(project.Applications))

	// Add a second application
	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name:      "test-application-2",
		InProject: project.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	application, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", application.Name)

	projectConfig = string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.Contains(t, projectConfig, "name: test-application")
	assert.NotContains(t, projectConfig, "name: test-application*") // Active is most recent

	assert.Contains(t, projectConfig, "name: test-application-2*")
	// Paths by default
	assert.NotContains(t, projectConfig, "path:")

	// ReloadProject
	project, err = w.ReloadProject(ctx, project)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(project.Applications))

	// Check active is the second one
	projectConfig = string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	back, err = project.LoadActiveApplication(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", back.Name)

	// Make the first one active
	action, err = actionapplication.NewActionSetApplicationActive(ctx, &v1actions.SetApplicationActive{
		Name:      "test-application",
		InProject: project.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	application, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application", application.Name)

	projectConfig = string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.Contains(t, projectConfig, "name: test-application*")
	assert.NotContains(t, projectConfig, "name: test-application-2*")

	action, err = actionapplication.NewActionSetApplicationActive(ctx, &v1actions.SetApplicationActive{
		Name:      "test-application-2",
		InProject: project.Name,
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)

	application, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-application-2", application.Name)

	projectConfig = string(shared.Must(os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))))
	assert.NotContains(t, projectConfig, "name: test-application*")
	assert.Contains(t, projectConfig, "name: test-application-2*")

}
