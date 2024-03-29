package configurations_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionapplication "github.com/codefly-dev/core/actions/application"
	actionproject "github.com/codefly-dev/core/actions/project"
	actionservice "github.com/codefly-dev/core/actions/service"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	v0base "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/shared"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestServiceUnique(t *testing.T) {
	svc := configurations.Service{Name: "svc", Application: "app"}
	unique := svc.Unique()
	info, err := configurations.ParseServiceUnique(unique)
	assert.NoError(t, err)
	assert.Equal(t, "svc", info.Name)
	assert.Equal(t, "app", info.Application)
}

type testSpec struct {
	TestField string `yaml:"test-field"`
}

func TestSpecSave(t *testing.T) {
	ctx := context.Background()
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
	ctx := context.Background()

	projectDir := t.TempDir()

	cleanup := func() {
		os.RemoveAll(projectDir)
	}
	var action actions.Action
	var err error
	action, err = actionproject.NewActionNewProject(ctx, &actionsv0.NewProject{
		Name: "test-project",
		Path: projectDir,
	})
	assert.NoError(t, err)
	out, err := action.Run(ctx)
	assert.NoError(t, err)
	project := shared.Must(actions.As[configurations.Project](out))

	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.NewApplication{
		Name:        "test-app-1",
		ProjectPath: project.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	appOne, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-app-1", appOne.Name)
	assert.Equal(t, 0, len(appOne.ServiceReferences))

	input := &actionsv0.AddService{
		Name:            "test-service-1",
		ApplicationPath: appOne.Dir(),
		Agent: &v0base.Agent{
			Kind:      v0base.Agent_SERVICE,
			Name:      "awesome-agent",
			Publisher: "codefly.test",
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
	assert.Equal(t, "test-project", serviceOne.Project)
	assert.Equal(t, "0.0.0", serviceOne.Version)
	assert.Equal(t, path.Join(appOne.Dir(), "services", "test-service-1"), serviceOne.Dir())

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
	appOne, err = configurations.ReloadApplication(ctx, appOne)
	assert.NoError(t, err)

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

	// re-load
	serviceOne, err = configurations.ReloadService(ctx, serviceOne)
	assert.NoError(t, err)

	// create another service
	action, err = actionservice.NewActionAddService(ctx, &actionsv0.AddService{
		Name:            "test-service-2",
		ApplicationPath: appOne.Dir(),
		Agent: &v0base.Agent{
			Kind:      v0base.Agent_SERVICE,
			Name:      "awesome-agent",
			Publisher: "codefly.test",
		},
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	serviceTwo, err := actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-2", serviceTwo.Name)

	// re-load
	appOne, err = configurations.ReloadApplication(ctx, appOne)
	assert.NoError(t, err)

	// new appOne and new serviceOne
	action, err = actionapplication.NewActionAddApplication(ctx, &actionsv0.NewApplication{
		Name:        "test-app-2",
		ProjectPath: project.Dir(),
	})
	assert.NoError(t, err)
	out, err = action.Run(ctx)
	assert.NoError(t, err)
	appTwo, err := actions.As[configurations.Application](out)
	assert.NoError(t, err)
	assert.Equal(t, "test-app-2", appTwo.Name)

	action, err = actionservice.NewActionAddService(ctx, &actionsv0.AddService{
		Name:            "test-service-3",
		ApplicationPath: appTwo.Dir(),
		Agent: &v0base.Agent{
			Kind:      v0base.Agent_SERVICE,
			Name:      "awesome-agent",
			Publisher: "codefly.test",
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
		project:      project,
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
	project      *configurations.Project
}

func TestAddService(t *testing.T) {
	_, cleanup := BaseSetup(t)
	defer cleanup()
}

func TestAddDependencyService(t *testing.T) {
	setup, cleanup := BaseSetup(t)
	defer cleanup()

	ctx := context.Background()
	var action actions.Action
	var err error
	// No info yet
	input := &actionsv0.AddServiceDependency{
		Name:                  setup.serviceOne.Name,
		Application:           setup.appOne.Name,
		ProjectPath:           setup.project.Dir(),
		DependencyName:        setup.serviceTwo.Name,
		DependencyApplication: setup.appOne.Name,
	}
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.NoError(t, err)

	// Same action with info that doesn't exist yet
	input.Endpoints = []string{"not-existing"}
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.Error(t, err)

	// Add two endpoints to service-2, one private, one application level
	service := setup.serviceTwo
	service.Endpoints = []*configurations.Endpoint{
		{
			Name: "test-info-private",
		},
		{
			Name:       "test-info-application",
			Visibility: "application",
		},
	}
	err = service.Save(ctx)
	assert.NoError(t, err)

	service, err = configurations.ReloadService(ctx, service)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(service.Endpoints))

	// Both endpoints will work because we are inside the same application
	input.Endpoints = []string{"test-info-private", "test-info-application"}
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	service, err = actions.As[configurations.Service](out)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(service.ServiceDependencies))
	assert.Equal(t, 2, len(service.ServiceDependencies[0].Endpoints))

	// Adding them again should return an error
	action, err = actionservice.NewActionAddServiceDependency(ctx, input)
	assert.NoError(t, err)
	_, err = action.Run(ctx)
	assert.Error(t, err)
}
