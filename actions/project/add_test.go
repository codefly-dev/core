package project_test

import (
	"encoding/json"
	"github.com/codefly-dev/core/shared"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/project"
	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"
	"github.com/stretchr/testify/assert"
)

func TestProjectAddFromJson(t *testing.T) {
	content, err := os.ReadFile("testdata/add.json")
	assert.NoError(t, err)

	action, err := actions.CreateAction(content)
	assert.NoError(t, err)
	assert.IsType(t, &project.AddProjectAction{}, action)
	create := action.(*project.AddProjectAction)
	assert.Equal(t, "My Project", create.Name)
	assert.Equal(t, "My Project Description", create.Description)

	back, err := json.Marshal(create)
	assert.NoError(t, err)
	assert.JSONEq(t, string(content), string(back))
}

func TestProjectAddFromCode(t *testing.T) {
	ctx := shared.NewContext()
	action, err := project.NewActionAddProject(ctx, &v1actions.AddProject{
		Name:        "My Project",
		Description: "My Project Description",
	})
	assert.NoError(t, err)
	content := `{"kind":"project.add","name":"My Project","description":"My Project Description"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))

}
