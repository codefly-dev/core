package configurations_test

import (
	"github.com/codefly-dev/core/actions/actions"
	actionapplication "github.com/codefly-dev/core/actions/application"
	actionproject "github.com/codefly-dev/core/actions/project"
	actionservice "github.com/codefly-dev/core/actions/service"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	v1base "github.com/codefly-dev/core/proto/v1/go/base"
	"github.com/codefly-dev/core/shared"
	"os"
	"path"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestAddService(t *testing.T) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)
	configurations.SetActiveWorkspace(w)
	defer os.RemoveAll(dir)

	var action actions.Action
	action = actionproject.NewActionAddProject(&v1actions.AddProject{
		Name:      "test-project",
		Workspace: w.Name,
	})
	_, err := action.Run(ctx)
	assert.NoError(t, err)
	action = actionapplication.NewActionAddApplication(&v1actions.AddApplication{
		Name:    "test-app",
		Project: "test-project",
	})
	_, err = action.Run(ctx)
	assert.NoError(t, err)

	action = actionservice.NewActionAddService(&v1actions.AddService{
		Name:        "test-service",
		Application: "test-app",
		Project:     "test-project",
		Agent: &v1base.Agent{
			Kind: v1base.Agent_SERVICE,
		},
	})
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	service, err := actions.As[configurations.Service](out)
	assert.NoError(t, err)

	assert.Equal(t, "test-service", service.Name)
	assert.Equal(t, "test-app", service.Namespace)

}

type testSpec struct {
	TestField string `yaml:"test-field"`
}

func TestSpecSave(t *testing.T) {
	ctx := shared.NewContext()
	s := &configurations.Service{Name: "testName"}
	out, err := yaml.Marshal(s)
	assert.NoError(t, err)
	assert.Contains(t, string(out), "testName")

	err = s.UpdateSpecFromSettings(&testSpec{TestField: "testKind"})
	assert.NoError(t, err)
	assert.NotNilf(t, s.Spec, "UpdateSpecFromSettings failed")
	_, ok := s.Spec["test-field"]
	assert.True(t, ok)

	// save to file
	tmp := t.TempDir()
	err = s.SaveAtDir(tmp)
	assert.NoError(t, err)
	// make sure it looks good
	content, err := os.ReadFile(path.Join(tmp, configurations.ServiceConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "test-field")
	assert.Contains(t, string(content), "testKind")

	s, err = configurations.LoadFromDir[configurations.Service](ctx, tmp)
	assert.NoError(t, err)

	assert.NoError(t, err)
	var field testSpec
	err = s.LoadSettingsFromSpec(&field)
	assert.NoError(t, err)
	assert.Equal(t, "testKind", field.TestField)
}
