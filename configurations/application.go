package configurations

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/golor"
	"os"
	"path"
	"path/filepath"
)

/*
This is a configuration wrapper to be able to read and write configuration of the applications.
*/

var currentApplication *Application

const ApplicationConfigurationName = "application.codefly.yaml"
const ApplicationKind = "application"

/*
Convention: relative to path from project
*/

type Application struct {
	Kind         string `yaml:"kind"`
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path"`
	Project      string `yaml:"project"`
	Domain       string `yaml:"domain"`

	Services []*ServiceReference `yaml:"services"`
}

func NewApplication(name string) (*Application, error) {
	logger := shared.NewLogger("configurations.NewApplication")
	app := Application{
		Kind:    ApplicationKind,
		Name:    name,
		Domain:  ExtendDomain(MustCurrentProject().Domain, name),
		Project: MustCurrentProject().Name,

		RelativePath: name,
	}
	dir := path.Join(MustCurrentProject().Dir(), app.RelativePath)
	SolveDirOrCreate(dir)

	// Templatize as usual
	err := templates.CopyAndApply(logger, templates.NewEmbeddedFileSystem(fs), shared.NewDir("templates/application"), shared.NewDir(dir), app)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot copy and apply template")
	}

	err = SaveToDir[Application](&app, dir)
	if err != nil {
		return nil, logger.Errorf("cannot save applications configuration <%s>: %v", app.Name, err)
	}
	SetCurrentApplication(&app)

	err = MustCurrentProject().AddApplication(&ApplicationReference{
		Name:         name,
		RelativePath: name,
	})
	if err != nil {
		return nil, logger.Wrapf(err, "cannot add applications to project configuration")
	}
	err = MustCurrentProject().Save()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project configuration")
	}
	return &app, nil
}

func (app *Application) Dir(opts ...Option) string {
	scope := WithScope(opts...).WithApplication(app)
	return path.Join(scope.Project.Dir(), scope.Application.RelativePath)
}

func LoadApplicationFromDir(dir string) (*Application, error) {
	logger := shared.NewLogger("configurations.LoadApplicationFromDir<%s>", dir)
	config, err := LoadFromDir[Application](dir)
	if err != nil {
		return nil, err
	}
	config.RelativePath = MustCurrentProject().Relative(dir)
	logger.Debugf("loaded applications configuration with %d services", len(config.Services))
	return config, err
}

type Scope struct {
	Project     *Project
	Application *Application
}

func (s *Scope) WithApplication(app *Application) *Scope {
	s.Application = app
	return s
}

func WithScope(opts ...Option) *Scope {
	scope := &Scope{
		Project: MustCurrentProject(),
	}
	for _, opt := range opts {
		opt(scope)
	}
	return scope
}

type Option func(scope *Scope)

func WithProject(project *Project) Option {
	return func(scope *Scope) {
		scope.Project = project
	}
}

func LoadApplicationFromName(name string, opts ...Option) (*Application, error) {
	logger := shared.NewLogger("configurations.LoadApplicationFromName<%s>", name)
	//scope := WithScope(opts...)
	apps, err := ListApplications(opts...)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot list applications")
	}
	for _, a := range apps {
		if a.Name == name {
			return a, nil
		}
	}
	return nil, logger.Errorf("cannot find application <%s>", name)
}

func (app *Application) Relative(absolute string, opts ...Option) string {
	s, err := filepath.Rel(app.Dir(opts...), absolute)
	shared.ExitOnError(err, "cannot compute relative path from applications")
	return s
}

func (app *Application) AddService(service *Service) error {
	logger := shared.NewLogger("configurations.AddService")
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

func (app *Application) Save() error {
	return SaveToDir(app, app.Dir())
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

func (app *Application) Unique() string {
	return app.Name
}

func CurrentApplication(opts ...Option) (*Application, error) {
	logger := shared.NewLogger("configurations.CurrentApplication")
	scope := WithScope(opts...)
	logger.TODO("This is more complicated: should be take the current as by configuration or by path?")
	if currentApplication == nil {
		// Look for the current applications in the project
		current := scope.Project.CurrentApplication
		if current == "" {
			logger.Debugf("no current applications for <%v>", scope.Project.Name)
			return nil, nil //NoApplicationError{Project: scope.Project.Name}
		}

		all, err := ListApplications(opts...)
		if err != nil {
			return nil, logger.Wrapf(err, "Listing applications in project <%s>", MustCurrentProject().Name)
		}
		if len(all) == 0 {
			return nil, NoApplicationError{Project: MustCurrentProject().Name}
		}
		for _, app := range all {
			if app.Name == current {
				currentApplication = app
				return app, nil
			}
		}
		return nil, logger.Errorf("cannot find current applications <%s> in project <%s>", current, MustCurrentProject().Name)
	}
	return currentApplication, nil
}

func MustCurrentApplication() *Application {
	app, err := CurrentApplication()
	shared.ExitOnError(err, "cannot get current application")
	return app
}

type NoApplicationError struct {
	Project string
}

func (e NoApplicationError) Error() string {
	return fmt.Sprintf("no applications found in <%s>", e.Project)
}

func SetCurrentApplication(app *Application) {
	if app == nil {
		golor.Println(`#(bold,white)[No application selected: you are running outside the application folder or forgot to use --current]`)
		os.Exit(0)
	}
	currentApplication = app
	MustCurrentProject().CurrentApplication = app.Name
	err := MustCurrentProject().Save()
	shared.ExitOnError(err, "cannot save current project")
}

func ListApplications(opts ...Option) ([]*Application, error) {
	logger := shared.NewLogger("applications.List<%s>", MustCurrentProject().Dir())
	scope := WithScope()
	for _, opt := range opts {
		opt(scope)
	}
	var apps []*Application
	for _, app := range scope.Project.Applications {
		a, err := LoadApplicationFromDir(path.Join(scope.Project.Dir(), app.RelativePath))
		if err != nil {
			return nil, logger.Errorf("cannot load applications configuration <%s>: %v", app.Name, err)
		}
		apps = append(apps, a)
	}
	return apps, nil
}

func FindApplicationUp(p string) (*Application, error) {
	logger := shared.NewLogger("configurations.FindApplicationUp")
	// Look at current directory
	cur := filepath.Dir(p)
	for {
		// Look for a service configuration
		p := path.Join(cur, ApplicationConfigurationName)
		if _, err := os.Stat(p); err == nil {
			return LoadApplicationFromDir(cur)
		}
		// Move up one directory
		cur = filepath.Dir(cur)

		// Stop if we reach the root directory
		if cur == "/" || cur == "." {
			return nil, logger.Errorf("cannot find service configuration: reached root directory")
		}
	}
}
