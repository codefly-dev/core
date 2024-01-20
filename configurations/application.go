package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

const (
	ApplicationKind              = "application"
	ApplicationConfigurationName = "application.codefly.yaml"
)

// An Application is a collection of services that are deployed together.
type Application struct {
	Kind         string  `yaml:"kind"`
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`

	Project     string `yaml:"project"`
	Domain      string `yaml:"domain"`
	Description string `yaml:"description,omitempty"`

	Services []*ServiceReference `yaml:"services"`

	// internal
	dir string
}

func (app *Application) Unique() string {
	return app.Name
}

func (app *Application) Proto() *basev0.Application {
	return &basev0.Application{
		Name:        app.Name,
		Description: app.Description,
		Project:     app.Project,
	}
}

// Dir returns the directory of the application
func (app *Application) Dir() string {
	return app.dir
}

// An ApplicationReference
type ApplicationReference struct {
	Name          string              `yaml:"name"`
	PathOverride  *string             `yaml:"path,omitempty"`
	Services      []*ServiceReference `yaml:"services,omitempty"`
	ActiveService string              `yaml:"active-service,omitempty"`
}

func (ref *ApplicationReference) GetActive(ctx context.Context) (*ServiceReference, error) {
	w := wool.Get(ctx).In("configurations.ApplicationReference.GetActive")
	if len(ref.Services) == 0 {
		return nil, w.NewError("no services")
	}
	if len(ref.Services) == 1 {
		return ref.Services[0], nil
	}
	if ref.ActiveService == "" {
		return nil, w.NewError("no active service")
	}
	for _, r := range ref.Services {
		if ref.Name == ref.ActiveService {
			return r, nil
		}
	}
	return nil, w.NewError("cannot find active service")
}

// NewApplication creates an application in a project
func (project *Project) NewApplication(ctx context.Context, action *actionsv0.AddApplication) (*Application, error) {
	w := wool.Get(ctx).In("configurations.NewApplication", wool.NameField(action.Name))
	if project.ExistsApplication(action.Name) {
		return nil, w.NewError("project already exists")
	}

	app := &Application{
		Kind:    ApplicationKind,
		Name:    action.Name,
		Domain:  ExtendDomain(project.Organization.Domain, action.Name),
		Project: project.Name,
	}

	ref := &ApplicationReference{Name: action.Name, PathOverride: OverridePath(action.Name, action.Path)}
	dir := project.ApplicationPath(ctx, ref)

	app.dir = dir

	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create application directory")
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save application configuration")
	}
	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), shared.NewDir("templates/application"), shared.NewDir(dir), app)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}
	// Add application to project
	project.Applications = append(project.Applications, ref)
	err = project.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save project configuration")
	}
	return app, nil
}

func LoadApplicationFromDirUnsafe(ctx context.Context, dir string) (*Application, error) {
	w := wool.Get(ctx).In("configurations.LoadApplicationFromDirUnsafe", wool.DirField(dir))
	app, err := LoadFromDir[Application](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = app.postLoad(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot post load")
	}
	app.dir = dir
	if err != nil {
		return nil, err
	}
	return app, nil
}

// LoadApplicationFromPath loads an application from a path
func LoadApplicationFromPath(ctx context.Context) (*Application, error) {
	dir, err := FindUp[Application](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, nil
	}
	return LoadApplicationFromDirUnsafe(ctx, *dir)
}

func (app *Application) postLoad(_ context.Context) error {
	for _, ref := range app.Services {
		ref.Application = app.Name
	}
	return app.Validate()
}

func (app *Application) SaveToDir(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("configurations.SaveToDir", wool.DirField(dir))
	err := app.preSave(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot pre-save")
	}
	return SaveToDir(ctx, app, dir)
}

func (app *Application) Save(ctx context.Context) error {
	return app.SaveToDir(ctx, app.Dir())
}

// Pre-save deals with some optimization
func (app *Application) preSave(_ context.Context) error {
	for _, ref := range app.Services {
		// Don't write Application in yaml
		ref.Application = ""
	}
	return nil
}

func (app *Application) AddService(_ context.Context, service *Service) error {
	w := wool.Get(context.Background()).In("configurations.AddService", wool.NameField(service.Name))
	for _, s := range app.Services {
		if s.Name == service.Name {
			return nil
		}
	}
	reference, err := service.Reference()
	if err != nil {
		return w.Wrapf(err, "cannot get service reference")
	}
	app.Services = append(app.Services, reference)
	return nil
}

func (app *Application) ServiceDomain(name string) string {
	return path.Join(app.Domain, name)
}

func (app *Application) GetServiceReferences(name string) (*ServiceReference, error) {
	for _, ref := range app.Services {
		if ref.Name == name {
			return ref, nil
		}
	}
	return nil, nil
}

func (app *Application) Reference() *ApplicationReference {
	return &ApplicationReference{
		Name:         app.Name,
		PathOverride: app.PathOverride,
	}
}

// ExistsService returns true if the service exists in the application
func (app *Application) ExistsService(ctx context.Context, name string) bool {
	w := wool.Get(ctx).In("configurations.ExistsService", wool.NameField(name))
	for _, s := range app.Services {
		if s.Name == name {
			return true
		}
	}
	w.Debug("current services", wool.Field("services", app.Services))
	return false
}

// ServicePath returns the absolute path of an Service
// Cases for Reference.Dir
// nil: relative path to application with name
// rel: relative path
// /abs: absolute path
func (app *Application) ServicePath(_ context.Context, ref *ServiceReference) string {
	if ref.PathOverride == nil {
		return path.Join(app.Dir(), ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(app.Dir(), *ref.PathOverride)
}

func (app *Application) LoadServiceFromReference(ctx context.Context, ref *ServiceReference) (*Service, error) {
	dir := app.ServicePath(ctx, ref)
	return LoadServiceFromDirUnsafe(ctx, dir)
}

func (app *Application) LoadServiceFromName(ctx context.Context, name string) (*Service, error) {
	w := wool.Get(ctx).In("configurations.LoadServiceFromName", wool.NameField(name))
	for _, ref := range app.Services {
		if ReferenceMatch(ref.Name, name) {
			return app.LoadServiceFromReference(ctx, ref)
		}
	}
	return nil, w.NewError("cannot find service <%s> in <%s>", name, app.Name)
}

func (app *Application) LoadServices(ctx context.Context) ([]*Service, error) {
	var services []*Service
	for _, ref := range app.Services {
		service, err := app.LoadServiceFromReference(ctx, ref)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, nil
}

func ReloadApplication(ctx context.Context, app *Application) (*Application, error) {
	return LoadApplicationFromDirUnsafe(ctx, app.Dir())
}

// DeleteService deletes a service from an application
func (app *Application) DeleteService(ctx context.Context, name string) error {
	w := wool.Get(ctx).In("configurations.DeleteService", wool.NameField(name))
	var services []*ServiceReference
	for _, s := range app.Services {
		if s.Name != name {
			services = append(services, s)
		}
	}
	app.Services = services
	err := app.Save(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot save application")
	}
	err = os.RemoveAll(app.ServicePath(ctx, &ServiceReference{Name: name}))
	if err != nil {
		return w.Wrapf(err, "cannot remove service directory")
	}
	return nil
}

func (app *Application) PublicEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("Application::PublicEndpoints", wool.ThisField(app))
	var publicEndpoints []*basev0.Endpoint
	// InitAndWait services
	services, err := app.LoadServices(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load services")
	}
	for _, service := range services {
		// InitAndWait groups
		for _, endpoint := range service.Endpoints {
			if endpoint.Visibility != VisibilityPublic {
				continue
			}
			publicEndpoints = append(publicEndpoints, EndpointBaseProto(endpoint))
		}
	}
	return publicEndpoints, nil
}

func (app *Application) DeleteServiceDependencies(ctx context.Context, ref *ServiceReference) error {
	w := wool.Get(ctx).In("Application::DeleteServiceDependencies", wool.ThisField(app), wool.Field("service", ref))
	for _, serviceRef := range app.Services {
		service, err := app.LoadServiceFromReference(ctx, serviceRef)
		if err != nil {
			return w.Wrapf(err, "can't load service from ref")
		}
		err = service.DeleteServiceDependencies(ctx, ref)
		if err != nil {
			return w.Wrapf(err, "can't delete service dependencies")
		}
	}
	return nil
}

func (app *Application) Validate() error {
	proto := app.Proto()
	return Validate(proto)
}

type NoApplicationError struct {
	Project string
}

func (e NoApplicationError) Error() string {
	return fmt.Sprintf("no applications found in <%s>", e.Project)
}
