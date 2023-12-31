package application_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/application"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/stretchr/testify/assert"
)

func TestApplicationAddFromJson(t *testing.T) {
	content, err := os.ReadFile("testdata/add.json")
	assert.NoError(t, err)

	action, err := actions.CreateAction(content)
	assert.NoError(t, err)
	assert.IsType(t, &application.AddApplicationAction{}, action)
	create := action.(*application.AddApplicationAction)
	assert.Equal(t, "My Application", create.Name)
	assert.Equal(t, "My Application Description", create.Description)

	back, err := json.Marshal(create)
	assert.NoError(t, err)
	assert.JSONEq(t, string(content), string(back))
}

func TestApplicationAddFromCode(t *testing.T) {
	ctx := context.Background()
	action, err := application.NewActionAddApplication(ctx, &actionsv0.AddApplication{
		Name:        "My Application",
		Description: "My Application Description",
	})
	assert.NoError(t, err)
	content := `{"kind":"application.add","name":"My Application","description":"My Application Description"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))

}
