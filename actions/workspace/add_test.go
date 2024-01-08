package workspace_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/workspace"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
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
	ctx := context.Background()
	action, err := workspace.NewActionAddWorkspace(ctx, &actionsv0.AddWorkspace{
		Name:        "My Workspace",
		Description: "My Workspace Description",
	})
	assert.NoError(t, err)
	content := `{"kind":"workspace.add","name":"My Workspace","description":"My Workspace Description"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))

}
