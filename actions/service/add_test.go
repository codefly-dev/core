package service_test

import (
	"encoding/json"
	"github.com/codefly-dev/core/shared"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/service"
	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"
	"github.com/stretchr/testify/assert"
)

func TestServiceAddFromJson(t *testing.T) {
	content, err := os.ReadFile("testdata/add.json")
	assert.NoError(t, err)

	action, err := actions.CreateAction(content)
	assert.NoError(t, err)
	assert.IsType(t, &service.AddServiceAction{}, action)
	create := action.(*service.AddServiceAction)
	assert.Equal(t, "My Service", create.Name)
	assert.Equal(t, "My Service Description", create.Description)

	back, err := json.Marshal(create)
	assert.NoError(t, err)
	assert.JSONEq(t, string(content), string(back))
}

func TestServiceAddFromCode(t *testing.T) {
	ctx := shared.NewContext()
	action, err := service.NewActionAddService(ctx, &v1actions.AddService{
		Name:        "My Service",
		Description: "My Service Description",
	})
	assert.NoError(t, err)
	content := `{"kind":"service.add","name":"My Service","description":"My Service Description"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))

}