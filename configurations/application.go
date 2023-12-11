package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
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
	dir           string
	activeService string
}

// Dir returns the directory of the application
func (app *Application) Dir() string {
	return app.dir
}

// An ApplicationReference
type ApplicationReference struct {
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`
}

// MarkAsActive marks a project as active
func (ref *ApplicationReference) MarkAsActive() {
	if !strings.HasSuffix(ref.Name, "*") {
		ref.Name = fmt.Sprintf("%s*", ref.Name)
	}
}

// IsActive returns true if the project is marked as active
func (ref *ApplicationReference) IsActive() (*ApplicationReference, bool) {
	if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
		return &ApplicationReference{Name: name, PathOverride: ref.PathOverride}, true
	}
	return ref, false
}

// NewApplication creates an application in a project
func (project *Project) NewApplication(ctx context.Context, action *v1actions.AddApplication) (*Application, error) {
	logger := shared.GetLogger(ctx).With("NewApplication<%s>", action.Name)
	if project.ExistsApplication(action.Name) {
		return nil, logger.Errorf("project already exists")
	}

	app := &Application{
		Kind:    ApplicationKind,
		Name:    action.Name,
		Domain:  ExtendDomain(project.Domain, action.Name),
		Project: project.Name,
	}

	ref := &ApplicationReference{Name: action.Name, PathOverride: OverridePath(action.Name, action.Path)}
	dir := project.ApplicationPath(ctx, ref)

	app.dir = dir

	err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot create application directory")
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save application configuration")
	}
	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), shared.NewDir("templates/application"), shared.NewDir(dir), app)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot copy and apply template")
	}
	// Add application to project
	project.Applications = append(project.Applications, ref)
	err = project.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project configuration")
	}
	return app, nil
}

func LoadApplicationFromDirUnsafe(ctx context.Context, dir string) (*Application, error) {
	logger := shared.GetLogger(ctx).With("LoadApplicationFromDirUnsafe")
	app, err := LoadFromDir[Application](ctx, dir)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	err = app.postLoad(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot post load")
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

func (app *Application) postLoad(ctx context.Context) error {
	for _, ref := range app.Services {
		ref.Application = app.Name
	}
	// Internally we keep track of active application differently
	if len(app.Services) == 1 {
		app.activeService = app.Services[0].Name
		return nil
	}
	for _, ref := range app.Services {
		if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
			ref.Name = name
			app.activeService = name
		}
	}
	return nil
}

func (app *Application) SaveToDir(ctx context.Context, dir string) error {
	logger := shared.GetLogger(ctx).With("SaveToDir<%s>", app.Name)
	err := app.preSave(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot pre-save")
	}
	return SaveToDir(ctx, app, dir)
}

func (app *Application) Save(ctx context.Context) error {
	return app.SaveToDir(ctx, app.Dir())
}

// Pre-save deals with the * style of active
func (app *Application) preSave(ctx context.Context) error {
	for _, ref := range app.Services {
		ref.Application = ""
	}
	if len(app.Services) == 1 {
		app.Services[0].Name = MakeInactive(app.Services[0].Name)
		return nil
	}
	for _, ref := range app.Services {
		if ref.Name == app.activeService {
			ref.Name = MakeActive(ref.Name)
		} else {
			ref.Name = MakeInactive(ref.Name)
		}
	}
	return nil
}

func (app *Application) AddService(ctx context.Context, service *Service) error {
	logger := shared.NewLogger().With("AddService")
	for _, s := range app.Services {
		if s.Name == service.Name {
			return nil
		}
	}
	reference, err := service.Reference()
	if err != nil {
		return logger.Wrapf(err, "cannot get service reference")
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

func (app *Application) LoadServiceFromName(ctx context.Context, name string) (*Service, error) {
	logger := shared.GetLogger(ctx).With("LoadServiceFromName<%s>", name)
	for _, ref := range app.Services {
		if ReferenceMatch(ref.Name, name) {
			return app.LoadServiceFromReference(ctx, ref)
		}
	}
	return nil, logger.Errorf("cannot find service <%s> in application <%s>", name, app.Name)
}

func (app *Application) Reference() *ApplicationReference {
	return &ApplicationReference{
		Name:         app.Name,
		PathOverride: app.PathOverride,
	}
}

// ExistsService returns true if the service exists in the application
func (app *Application) ExistsService(name string) bool {
	for _, s := range app.Services {
		if s.Name == name {
			return true
		}
	}
	return false
}

// ServicePath returns the absolute path of an Service
// Cases for Reference.Path
// nil: relative path to application with name
// rel: relative path
// /abs: absolute path
func (app *Application) ServicePath(ctx context.Context, ref *ServiceReference) string {
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

func (app *Application) Reload(ctx context.Context, app2 *Application) (*Application, error) {
	return LoadApplicationFromDirUnsafe(ctx, app.Dir())
}

func (app *Application) ActiveService(ctx context.Context) *string {
	if app.activeService == "" {
		return nil
	}
	return &app.activeService
}

func (app *Application) SetActiveService(ctx context.Context, name string) error {
	logger := shared.GetLogger(ctx).With("SetActiveService<%s>", name)
	logger.DebugMe("Services: %v", app.Services)
	if !app.ExistsService(name) {
		return logger.Errorf("service <%s> does not exist in application <%s>", name, app.Name)
	}
	app.activeService = name
	return nil
}

// DeleteService deletes a service from an application
func (app *Application) DeleteService(ctx context.Context, name string) error {
	logger := shared.GetLogger(ctx).With("DeleteService<%s>", name)
	logger.TODO("Need to delete service dependencies everywhere")
	var services []*ServiceReference
	for _, s := range app.Services {
		if s.Name != name {
			services = append(services, s)
		}
	}
	app.Services = services
	err := app.Save(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot save application")
	}
	err = os.RemoveAll(app.ServicePath(ctx, &ServiceReference{Name: name}))
	if err != nil {
		return logger.Wrapf(err, "cannot remove service directory")
	}
	return nil
}

func (app *Application) LoadActiveService(ctx context.Context) (*Service, error) {
	return app.LoadServiceFromName(ctx, app.activeService)
}

type NoApplicationError struct {
	Project string
}

func (e NoApplicationError) Error() string {
	return fmt.Sprintf("no applications found in <%s>", e.Project)
}
