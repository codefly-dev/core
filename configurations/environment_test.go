package configurations_test

import (
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionenviroment "github.com/codefly-dev/core/actions/environment"
	actionproject "github.com/codefly-dev/core/actions/project"
	"github.com/codefly-dev/core/configurations"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestEnvironment(t *testing.T) {
	ctx := shared.NewContext()
	createTestWorkspace(t, ctx)

	var action actions.Action
	var err error

	action, err = actionproject.NewActionAddProject(ctx, &v1actions.AddProject{
		Name: "test-project",
	})
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	action, err = actionenviroment.NewActionAddEnvironment(ctx, &v1actions.AddEnvironment{
		Name:    "test-environment",
		Project: "test-project",
	})
	_, err = action.Run(ctx)
	assert.NoError(t, err)

	// Make sure the environment is created
	content, err := os.ReadFile(path.Join(project.Dir(), configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "name: test-environment")
}
