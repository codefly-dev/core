package workspace_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/workspace"
	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
	"github.com/stretchr/testify/assert"
)

func TestWorkspaceAddFromJson(t *testing.T) {
	content, err := os.ReadFile("testdata/add.json")
	assert.NoError(t, err)

	action, err := actions.CreateAction(content)
	assert.NoError(t, err)
	assert.IsType(t, &workspace.AddWorkspaceAction{}, action)
	create := action.(*workspace.AddWorkspaceAction)
	assert.Equal(t, "My Workspace", create.Name)
	assert.Equal(t, "My Workspace Description", create.Description)

	back, err := json.Marshal(create)
	assert.NoError(t, err)
	assert.JSONEq(t, string(content), string(back))
}

func TestWorkspaceAddFromCode(t *testing.T) {
	ctx := wool.NewContext()
	action, err := workspace.NewActionAddWorkspace(ctx, &actionsv1.AddWorkspace{
		Name:        "My Workspace",
		Description: "My Workspace Description",
	})
	assert.NoError(t, err)
	content := `{"kind":"workspace.add","name":"My Workspace","description":"My Workspace Description"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))

}
