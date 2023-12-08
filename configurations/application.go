package configurations

import (
	"context"
	"fmt"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"path"
	"slices"
	"strings"

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
	dir string
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

func (project *Project) NewApplication(ctx context.Context, action *v1actions.AddApplication) (*Application, error) {
	logger := shared.GetBaseLogger(ctx).With("NewApplication<%s>", action.Name)
	if slices.Contains(project.ApplicationsNames(), action.Name) {
		return nil, logger.Errorf("project already exists")
	}

	if err := ValidateProjectName(action.Name); err != nil {
		return nil, logger.Wrapf(err, "invalid project name")
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

func (app *Application) Dir() string {
	return app.dir
}

// LoadApplicationFromReference loads an application from a reference
func (project *Project) LoadApplicationFromReference(ctx context.Context, ref *ApplicationReference) (*Application, error) {
	logger := shared.GetBaseLogger(ctx).With("LoadApplicationFromReference<%s>", ref.Name)
	dir := project.ApplicationPath(ctx, ref)
	app, err := LoadFromDir[Application](ctx, dir)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	app.dir = dir
	if err != nil {
		return nil, err
	}
	return app, nil
}

// LoadApplicationFromName loads an application from a name
func (project *Project) LoadApplicationFromName(ctx context.Context, name string) (*Application, error) {
	logger := shared.GetBaseLogger(ctx).With("LoadApplicationFromName<%s>", name)
	logger.Debugf("#applications %d", len(project.Applications))
	for _, ref := range project.Applications {
		if ReferenceMatch(ref.Name, name) {
			return project.LoadApplicationFromReference(ctx, ref)
		}
	}
	return nil, fmt.Errorf("cannot find application <%s> in project <%s>", name, project.Name)
}

// LoadApplicationFromDir is the loader from filesystem
func LoadApplicationFromDir(ctx context.Context, dir string) (*Application, error) {
	logger := shared.GetBaseLogger(ctx).With("LoadApplicationFromDir<%s>", dir)
	app, err := LoadFromDir[Application](ctx, dir)
	logger.Tracef("loaded applications configuration")
	if err != nil {
		return nil, err
	}
	app.dir = dir
	return app, err
}

func (app *Application) AddService(service *Service) error {
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

func (app *Application) Save(ctx context.Context) error {
	return SaveToDir(ctx, app, app.Dir())
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
	return LoadServiceFromDir(ctx, path.Join(app.Dir(), name))
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

type NoApplicationError struct {
	Project string
}

func (e NoApplicationError) Error() string {
	return fmt.Sprintf("no applications found in <%s>", e.Project)
}
