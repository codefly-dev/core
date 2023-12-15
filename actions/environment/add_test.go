package environment_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/environment"
	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"
	"github.com/stretchr/testify/assert"
)

func TestEnvironmentAddFromJson(t *testing.T) {
	content, err := os.ReadFile("testdata/add.json")
	assert.NoError(t, err)

	action, err := actions.CreateAction(content)
	assert.NoError(t, err)
	assert.IsType(t, &environment.AddEnvironmentAction{}, action)
	create := action.(*environment.AddEnvironmentAction)
	assert.Equal(t, "My Environment", create.Name)
	assert.Equal(t, "My Environment Description", create.Description)

	back, err := json.Marshal(create)
	assert.NoError(t, err)
	assert.JSONEq(t, string(content), string(back))
}

func TestEnvironmentAddFromCode(t *testing.T) {
	ctx := shared.NewContext()
	action, err := environment.NewActionAddEnvironment(ctx, &v1actions.AddEnvironment{
		Name:        "My Environment",
		Description: "My Environment Description",
	})
	assert.NoError(t, err)
	content := `{"kind":"environment.add","name":"My Environment","description":"My Environment Description"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))

}
