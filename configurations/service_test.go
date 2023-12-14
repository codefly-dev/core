package configurations_test

import (
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionapplication "github.com/codefly-dev/core/actions/application"
	actionproject "github.com/codefly-dev/core/actions/project"
	actionservice "github.com/codefly-dev/core/actions/service"
	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"
	v1base "github.com/codefly-dev/core/generated/v1/go/proto/base"
	"github.com/codefly-dev/core/shared"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

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

type Cleanup func()

func BaseSetup(t *testing.T) (BaseOutput, Cleanup) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)
	cleanup := func() {
		os.RemoveAll(dir)
	}

	var action actions.Action
	var err error
	action, err = actionproject.NewActionAddProject(ctx, &v1actions.AddProject{
		Name:      "test-project",
		Workspace: w.Name,
	})
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.NoError(t, err)
	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name:    "test-app-1",
		Project: "test-project",
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	appOne, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-app-1", appOne.Name)
	assert.Equal(t, 0, len(appOne.Services))

	input := &v1actions.AddService{
		Name:        "test-service-1",
		Application: "test-app-1",
		Project:     "test-project",
		Agent: &v1base.Agent{
			Kind: v1base.Agent_SERVICE,
		},
	}
	action, err = actionservice.NewActionAddService(ctx, input)
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	serviceOne, err := actions.As[configurations.Service](out)
	assert.NoError(t, err)

	assert.Equal(t, "test-service-1", serviceOne.Name)
	assert.Equal(t, "test-app-1", serviceOne.Application)
	assert.Equal(t, "test-app-1", serviceOne.Namespace)
	assert.Equal(t, "0.0.0", serviceOne.Version)

	// Check configurations
	serviceConfig := string(shared.Must(os.ReadFile(path.Join(serviceOne.Dir(), configurations.ServiceConfigurationName))))
	assert.Contains(t, serviceConfig, "name: test-service-1")
	assert.Contains(t, serviceConfig, "application: test-app-1")
	assert.NotContains(t, serviceConfig, "path:") // use default path

	appConfig := string(shared.Must(os.ReadFile(path.Join(appOne.Dir(), configurations.ApplicationConfigurationName))))
	assert.Contains(t, appConfig, "name: test-service-1")
	assert.NotContains(t, appConfig, "path:") // use default path

	// make sure it's saved
	s, err := configurations.LoadFromDir[configurations.Service](ctx, serviceOne.Dir())
	assert.NoError(t, err)
	assert.Equal(t, serviceOne.Name, s.Name)

	// re-load the appOne and check that this is the active serviceOne
	appOne, err = appOne.ReloadApplication(ctx, appOne)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-1", *appOne.ActiveService(ctx))

	// re-create gets an error
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Check configuration to see if nothing is gone
	appConfig = string(shared.Must(os.ReadFile(path.Join(appOne.Dir(), configurations.ApplicationConfigurationName))))
	assert.Contains(t, appConfig, "name: test-service-1")
	assert.NotContains(t, appConfig, "path:") // use default path

	// re-create with override doesn't trigger an error

	input.Override = true
	input.Namespace = "overwritten"
	action, err = actionservice.NewActionAddService(ctx, input)
	assert.NoError(t, err)

	out, err = action.Run(ctx)
	assert.NoError(t, err)

	serviceOne, err = actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-1", serviceOne.Name)
	assert.Equal(t, "overwritten", serviceOne.Namespace)

	// re-load
	serviceOne, err = configurations.ReloadService(ctx, serviceOne)
	assert.NoError(t, err)
	assert.Equal(t, "overwritten", serviceOne.Namespace)

	// create another service
	action, err = actionservice.NewActionAddService(ctx, &v1actions.AddService{
		Name:        "test-service-2",
		Application: "test-app-1",
		Project:     "test-project",
		Agent: &v1base.Agent{
			Kind: v1base.Agent_SERVICE,
		},
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	serviceTwo, err := actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-2", serviceTwo.Name)

	// re-load
	appOne, err = appOne.ReloadApplication(ctx, appOne)
	assert.NoError(t, err)

	assert.Equal(t, "test-service-2", *appOne.ActiveService(ctx))

	// new appOne and new serviceOne
	action, err = actionapplication.NewActionAddApplication(ctx, &v1actions.AddApplication{
		Name:    "test-app-2",
		Project: "test-project",
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	appTwo, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-app-2", appTwo.Name)

	action, err = actionservice.NewActionAddService(ctx, &v1actions.AddService{
		Name:        "test-service-3",
		Application: "test-app-2",
		Project:     "test-project",
		Agent: &v1base.Agent{
			Kind: v1base.Agent_SERVICE,
		},
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	serviceThree, err := actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-3", serviceThree.Name)
	assert.Equal(t, "test-app-2", serviceThree.Application)
	return BaseOutput{
		serviceOne:   serviceOne,
		serviceTwo:   serviceTwo,
		serviceThree: serviceThree,
		appOne:       appOne,
		appTwo:       appTwo,
	}, cleanup
}

type BaseOutput struct {
	serviceOne   *configurations.Service
	serviceTwo   *configurations.Service
	serviceThree *configurations.Service
	appOne       *configurations.Application
	appTwo       *configurations.Application
}

func TestAddService(t *testing.T) {
	_, cleanup := BaseSetup(t)
	defer cleanup()
}

func TestAddDependencyService(t *testing.T) {
	setup, cleanup := BaseSetup(t)
	defer cleanup()

	ctx := shared.NewContext()
	var action actions.Action
	var err error
	// No endpoint yet
	input := &v1actions.AddServiceDependency{
		Name:                  "test-service-1",
		Application:           "test-app-1",
		Project:               "test-project",
		DependencyName:        "test-service-2",
		DependencyApplication: "test-app-1",
	}
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.NoError(t, err)

	// Same action with endpoint that doesn't exist yet
	input.Endpoints = []string{"not-existing"}
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Add two endpoints to service-2, one private, one application level
	service := setup.serviceTwo
	service.Endpoints = []*configurations.Endpoint{
		{
			Name: "test-endpoint-private",
		},
		{
			Name:       "test-endpoint-application",
			Visibility: "application",
		},
	}
	err = service.Save(ctx)
	assert.NoError(t, err)

	service, err = configurations.ReloadService(ctx, service)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(service.Endpoints))

	shared.SetLogLevel(shared.Debug)

	// Both endpoints will work because we are inside the same application
	input.Endpoints = []string{"test-endpoint-private", "test-endpoint-application"}
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	service, err = actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(service.Dependencies))
	assert.Equal(t, 2, len(service.Dependencies[0].Endpoints))

	// Adding them again should return an error
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.Error(t, err)
}
