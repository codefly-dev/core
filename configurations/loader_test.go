package configurations_test

import (
	"testing"
)

func TestConfigurations(t *testing.T) {
	//// The project is the basis
	//tmpDir := t.TempDir()
	//
	//project, err := configurations.NewProjectOld(org, "project", tmpDir)
	//assert.NoError(t, err, "failed to create project")
	//err = project.Save()
	//assert.NoError(t, err, "failed to save project")
	//
	//// TODO: This will be dealt with a global configuration ~/.codefly
	//configurations.SetCurrentProject(project)
	//
	//// current project
	//current := configurations.MustCurrentProject()
	//assert.NotNil(t, current, "current project is nil")
	//assert.Equal(t, project.ProjectName, current.ProjectName, "current project is not equal to the original")
	//
	//// Runtime the project
	//{
	//	p, err := configurations.LoadProjectFromDir(tmpDir)
	//	assert.NoError(t, err, "failed to load project")
	//	assert.Equal(t, project.ProjectName, p.ProjectName, "loaded project is not equal to the original")
	//}
	//
	//// Create an applications
	//app, err := configurations.NewApplication("applications")
	//assert.NoError(t, err, "failed to create applications")
	//assert.Equal(t, "applications", app.ProjectName, "applications Name is not equal to the original")
	//
	//// Runtime the applications
	//{
	//	a, err := configurations.LoadApplicationConfigurationFromName("applications")
	//	assert.NoError(t, err, "failed to load applications")
	//	assert.Equal(t, app.ProjectName, a.ProjectName, "loaded applications is not equal to the original")
	//
	//	apps, err := configurations.ListApplications(configurations.MustCurrentProject())
	//	assert.NoError(t, err, "failed to list applications")
	//	assert.Equal(t, 1, len(apps), "list of applications is not equal to 1")
	//}
	//
	//assert.NotNil(t, configurations.MustCurrentApplication(), "current applications is nil")
	//assert.Equal(t, app.ProjectName, configurations.MustCurrentApplication().ProjectName, "current applications is not equal to the original")
	//// No applications yet
	//assert.Equal(t, 0, len(configurations.MustCurrentApplication().Services), "current applications has services")
	//
	//// Create a service
	//service, err := configurations.NewService("service", "default", &configurations.Plugin{Identifier: "codefly-io/codefly-service-base", Version: "0.0.1"})
	//assert.NoError(t, err, "failed to create service")
	//assert.Equal(t, "service", service.ProjectName, "service Name is not equal to the original")
	//
	//err = service.Save()
	//assert.NoError(t, err, "failed to save service")
	//
	//// Still no services
	//assert.Equal(t, 0, len(configurations.MustCurrentApplication().Services), "current applications has services")
	//
	//// Map the service to the applications
	//err = configurations.MustCurrentApplication().AddService(service)
	//assert.NoError(t, err, "failed to add service to applications")
	//err = configurations.MustCurrentApplication().Save()
	//assert.NoError(t, err, "failed to save applications")
	//
	//entry := service.Reference()
	//
	//// Runtime the service
	//{
	//	s, err := configurations.LoadServiceFromReference(entry)
	//	assert.NoError(t, err, "failed to load service")
	//	assert.Equal(t, service.ProjectName, s.ProjectName, "loaded service is not equal to the original")
	//}
	//// Make sure it is added to the applications
	//{
	//	app, err := configurations.LoadApplicationConfigurationFromName("applications")
	//	assert.NoError(t, err, "failed to load applications")
	//	assert.Equal(t, 1, len(app.Services), "applications does not have the service")
	//}
	//
	//// For example, we can share some caching between applications
	//// Probably bad practice but allow it
	//anotherApp, err := configurations.NewApplication("another-applications")
	//assert.NoError(t, err, "failed to create another applications")
	//
	//cache, err := configurations.NewService("cache", "default", &configurations.Plugin{Identifier: "codefly-io/cache", Version: "0.0.1"})
	//assert.NoError(t, err, "failed to create cache service")
	//err = cache.Save()
	//assert.NoError(t, err, "failed to save service")
	//
	//err = anotherApp.AddService(cache)
	//assert.NoError(t, err, "failed to add cache service to another applications")
	//
	//err = service.AddDependencyReference(cache)
	//assert.NoError(t, err, "failed to add dependency to service")
	//err = service.Save()
	//assert.NoError(t, err, "failed to save service")
	//
	//{
	//	// Runtime service
	//	s, err := configurations.LoadServiceFromReference(entry)
	//	assert.NoError(t, err, "failed to load service")
	//	assert.Equal(t, service.ProjectName, s.ProjectName, "loaded service is not equal to the original")
	//	assert.Equal(t, 1, len(s.Dependencies), "service does not have the dependency")
	//	assert.Equal(t, cache.ProjectName, s.Dependencies[0].ProjectName, "service dependency is not equal to the original")
	//
	//	// Make sure we can load the cache configuration from the dependency
	//	dep := s.Dependencies[0]
	//	cacheBack, err := configurations.LoadServiceFromReference(&dep)
	//	assert.NoError(t, err, "failed to load cache service")
	//	assert.Equal(t, cache.ProjectName, cacheBack.ProjectName, "loaded cache service is not equal to the original")
	//
	//}
}
