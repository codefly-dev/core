package configurations_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionenviroment "github.com/codefly-dev/core/actions/environment"
	actionproject "github.com/codefly-dev/core/actions/project"
	"github.com/codefly-dev/core/configurations"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestEnvironment(t *testing.T) {
	ctx := context.Background()
	projectDir := t.TempDir()

	defer func() {
		os.RemoveAll(projectDir)
	}()

	var action actions.Action
	var err error

	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: "test-project",
		Path: projectDir,
	})
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	action, err = actionenviroment.NewActionAddEnvironment(ctx, &actionsv0.AddEnvironment{
		Name:        "test-environment",
		ProjectPath: project.Dir(),
	})
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.NoError(t, err)

	// Make sure the environment is created
	content, err := os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "name: test-environment")
}
