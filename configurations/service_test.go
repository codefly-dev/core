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
	defer os.RemoveAll(dir)

	var action actions.Action
	var err error
	action, err = actionproject.NewActionAddProject(ctx, &v1actions.AddProject{
		Name:        "test-project",
		InWorkspace: w.Name,
	})
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.NoError(t, err)
	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name:      "test-app",
		InProject: "test-project",
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	app, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-app", app.Name)
	assert.Equal(t, 0, len(app.Services))

	input := &v1actions.AddService{
		Name:          "test-service",
		InApplication: "test-app",
		InProject:     "test-project",
		Agent: &v1base.Agent{
			Kind: v1base.Agent_SERVICE,
		},
	}
	action, err = actionservice.NewActionAddService(ctx, input)
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	service, err := actions.As[configurations.Service](out)
	assert.NoError(t, err)

	assert.Equal(t, "test-service", service.Name)
	assert.Equal(t, "test-app", service.Application)
	assert.Equal(t, "test-app", service.Namespace)
	assert.Equal(t, "0.0.0", service.Version)

	// Check configurations
	serviceConfig := string(shared.Must(os.ReadFile(path.Join(service.Dir(), configurations.ServiceConfigurationName))))
	assert.Contains(t, serviceConfig, "name: test-service")
	assert.Contains(t, serviceConfig, "application: test-app")
	assert.NotContains(t, serviceConfig, "path:") // use default path

	appConfig := string(shared.Must(os.ReadFile(path.Join(app.Dir(), configurations.ApplicationConfigurationName))))
	assert.Contains(t, appConfig, "name: test-service")
	assert.NotContains(t, appConfig, "path:") // use default path

	// make sure it's saved
	s, err := configurations.LoadFromDir[configurations.Service](ctx, service.Dir())
	assert.NoError(t, err)
	assert.Equal(t, service.Name, s.Name)

	// re-load the app and check that this is the active service
	app, err = app.Reload(ctx, app)
	assert.NoError(t, err)
	assert.Equal(t, "test-service", *app.ActiveService(ctx))

	// re-create gets an error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Check configuration to see if nothing is gone
	appConfig = string(shared.Must(os.ReadFile(path.Join(app.Dir(), configurations.ApplicationConfigurationName))))
	assert.Contains(t, appConfig, "name: test-service")
	assert.NotContains(t, appConfig, "path:") // use default path

	// re-create with override doesn't trigger an error

	input.Override = true
	input.Namespace = "overwritten"
	action, err = actionservice.NewActionAddService(ctx, input)
	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)

	service, err = actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-service", service.Name)
	assert.Equal(t, "overwritten", service.Namespace)

	// re-load
	service, err = service.Reload(ctx, service)
	assert.NoError(t, err)
	assert.Equal(t, "overwritten", service.Namespace)

	// create another service
	action, err = actionservice.NewActionAddService(ctx, &v1actions.AddService{
		Name:          "test-service2",
		InApplication: "test-app",
		InProject:     "test-project",
		Agent: &v1base.Agent{
			Kind: v1base.Agent_SERVICE,
		},
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	service, err = actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-service2", service.Name)

	// re-load
	app, err = app.Reload(ctx, app)
	assert.NoError(t, err)

	assert.Equal(t, "test-service2", *app.ActiveService(ctx))

	// new app and new service
	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name:      "test-app2",
		InProject: "test-project",
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	app, err = actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-app2", app.Name)

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
	err = s.SaveAtDir(ctx, tmp)
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
