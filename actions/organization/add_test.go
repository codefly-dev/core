package organization_test

import (
	"encoding/json"
	"github.com/codefly-dev/core/shared"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/organization"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"github.com/stretchr/testify/assert"
)

func TestOrganizationAddFromJson(t *testing.T) {
	content, err := os.ReadFile("testdata/add.json")
	assert.NoError(t, err)

	action, err := actions.CreateAction(content)
	assert.NoError(t, err)
	assert.IsType(t, &organization.AddOrganizationAction{}, action)
	create := action.(*organization.AddOrganizationAction)
	assert.Equal(t, "My Organization", create.Name)
	assert.Equal(t, "https://github.com/my-organization", create.Domain)

	back, err := json.Marshal(create)
	assert.NoError(t, err)
	assert.JSONEq(t, string(content), string(back))
}

func TestOrganizationAddFromCode(t *testing.T) {
	ctx := shared.NewContext()
	action, err := organization.NewActionAddOrganization(ctx, &v1actions.AddOrganization{
		Name:   "My Organization",
		Domain: "https://github.com/my-organization",
	})
	assert.NoError(t, err)
	content := `{"kind":"organization.add","name":"My Organization","domain": "https://github.com/my-organization"}`
	back, err := json.Marshal(action)
	assert.NoError(t, err)
	assert.JSONEq(t, content, string(back))
}
