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

	ServiceReferences []*ServiceReference `yaml:"services"`

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
	w := wool.Get(ctx).In("configurations.ApplicationReference.GetActiveService")
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
		if r.Name == ref.ActiveService {
			w.Debug("found active service", wool.Field("service", r.Name))
			return r, nil
		}
	}
	return nil, w.NewError("cannot find active service")
}

func (ref *ApplicationReference) GetActiveService(ctx context.Context) (*ServiceReference, error) {
	w := wool.Get(ctx).In("configurations.ApplicationReference.GetActiveService")
	if len(ref.Services) == 0 {
		return nil, w.NewError("no services")
	}
	if len(ref.Services) == 1 {
		return ref.Services[0], nil
	}
	if ref.ActiveService == "" {
		return nil, w.NewError("no active service")
	}
	return ref.GetServiceFromName(ctx, ref.ActiveService)
}

func (ref *ApplicationReference) GetServiceFromName(ctx context.Context, serviceName string) (*ServiceReference, error) {
	w := wool.Get(ctx).In("configurations.ApplicationReference.GetActiveService")
	for _, r := range ref.Services {
		if r.Name == serviceName {
			return r, nil
		}
	}
	return nil, w.NewError("cannot find active service")
}

func (ref *ApplicationReference) AddService(_ context.Context, service *ServiceReference) error {
	for _, s := range ref.Services {
		if s.Name == service.Name {
			return nil
		}
	}
	ref.Services = append(ref.Services, service)
	return nil
}

type NewApplicationInput struct {
	Name string
}

// NewApplication creates an application in a project
func (project *Project) NewApplication(ctx context.Context, action *actionsv0.NewApplication) (*Application, error) {
	w := wool.Get(ctx).In("configurations.NewApplication", wool.NameField(action.Name))
	if project.ExistsApplication(action.Name) {
		return nil, w.NewError("project already exists")
	}

	app := &Application{
		Kind: ApplicationKind,
		Name: action.Name,
		//SourceVersionControl:  ExtendDomain(project.Organization.SourceVersionControl, action.Name),
		Project: project.Name,
	}
	dir := path.Join(project.Dir(), "applications", action.Name)

	app.dir = dir

	exists, err := shared.CheckDirectory(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if exists {
		return nil, w.NewError("directory already exists")
	}

	_, err = shared.CheckDirectoryOrCreate(ctx, dir)

	if err != nil {
		return nil, w.Wrapf(err, "cannot create application directory")
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save application configuration")
	}
	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), "templates/application", dir, app)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}

	err = project.AddApplicationReference(app.Reference())
	if err != nil {
		return nil, w.Wrapf(err, "cannot add application to project")
	}

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
	for _, ref := range app.ServiceReferences {
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
	for _, ref := range app.ServiceReferences {
		// Don't write Application in yaml
		ref.Application = ""
	}
	return nil
}

func (app *Application) AddServiceReference(_ context.Context, ref *ServiceReference) error {
	w := wool.Get(context.Background()).In("configurations.AddServiceReference", wool.NameField(ref.Name))
	w.Trace("adding service reference", wool.Field("service", ref))
	for _, s := range app.ServiceReferences {
		if s.Name == ref.Name {
			return nil
		}
	}
	app.ServiceReferences = append(app.ServiceReferences, ref)
	return nil
}

func (app *Application) ServiceDomain(name string) string {
	return path.Join(app.Domain, name)
}

func (app *Application) GetServiceReferences(name string) (*ServiceReference, error) {
	for _, ref := range app.ServiceReferences {
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
	for _, s := range app.ServiceReferences {
		if s.Name == name {
			return true
		}
	}
	w.Debug("current services", wool.Field("services", app.ServiceReferences))
	return false
}

// ServicePath returns the absolute path of an Service
// Cases for Reference.Dir
// nil: relative path to application with name
// rel: relative path
// /abs: absolute path
func (app *Application) ServicePath(_ context.Context, ref *ServiceReference) string {
	if ref.PathOverride == nil {
		return path.Join(app.Dir(), "services", ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(app.Dir(), "services", *ref.PathOverride)
}

func (app *Application) LoadServiceFromReference(ctx context.Context, ref *ServiceReference) (*Service, error) {
	dir := app.ServicePath(ctx, ref)
	service, err := LoadServiceFromDir(ctx, dir)
	if err != nil {
		return nil, wool.Get(ctx).In("configurations.LoadServiceFromReference", wool.DirField(dir)).Wrap(err)
	}
	service.Application = app.Name
	return service, nil
}

func (app *Application) LoadServiceFromName(ctx context.Context, name string) (*Service, error) {
	w := wool.Get(ctx).In("configurations.LoadServiceFromName", wool.NameField(name))
	for _, ref := range app.ServiceReferences {
		if ReferenceMatch(ref.Name, name) {
			return app.LoadServiceFromReference(ctx, ref)
		}
	}
	return nil, w.NewError("cannot find service <%s> in <%s>", name, app.Name)
}

func (app *Application) LoadServices(ctx context.Context) ([]*Service, error) {
	var services []*Service
	for _, ref := range app.ServiceReferences {
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
	for _, s := range app.ServiceReferences {
		if s.Name != name {
			services = append(services, s)
		}
	}
	app.ServiceReferences = services
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
		for _, endpoint := range service.Endpoints {
			if endpoint.Visibility != VisibilityPublic {
				continue
			}
			publicEndpoints = append(publicEndpoints, endpoint.Proto())
		}
	}
	return publicEndpoints, nil
}

func (app *Application) DeleteServiceDependencies(ctx context.Context, ref *ServiceReference) error {
	w := wool.Get(ctx).In("Application::DeleteServiceDependencies", wool.ThisField(app), wool.Field("service", ref))
	for _, serviceRef := range app.ServiceReferences {
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
