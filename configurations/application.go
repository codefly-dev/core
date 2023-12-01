package configurations

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/golor"
)

/*
This is a configuration wrapper to be able to read and write configuration of the applications.
*/

var currentApplication *Application

const (
	ApplicationConfigurationName = "application.codefly.yaml"
	ApplicationKind              = "application"
)

/*
Convention: relative to path from project
*/

type Application struct {
	Kind                 string  `yaml:"kind"`
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path"`
	Project              string  `yaml:"project"`
	Domain               string  `yaml:"domain"`

	Services []*ServiceReference `yaml:"services"`
}

func ApplicationConfiguration(current bool) (*Application, error) {
	logger := shared.NewLogger("build.ApplicationCmd")
	var config *Application
	if !current {
		cur, err := os.Getwd()
		if err != nil {
			return nil, logger.Wrapf(err, "cannot get current directory")
		}
		config, err = FindUp[Application](cur)
		if err != nil {
			if strings.Contains(err.Error(), "reached root directory") {
				cur, err := CurrentApplication()
				if err != nil {
					return nil, logger.Wrapf(err, "cannot load current appplication")
				}
				// logger.WarnUnique(shared.NewUserWarning("You are running in a directory that is not part of a project. Using current application from context: <%s>.", cur.Name))
				return cur, nil
			}
			return nil, err
		}
	} else {
		return CurrentApplication()
	}
	return config, nil
}

func NewApplication(name string) (*Application, error) {
	logger := shared.NewLogger("NewApplication")
	app := Application{
		Kind:    ApplicationKind,
		Name:    name,
		Domain:  ExtendDomain(MustCurrentProject().Domain, name),
		Project: MustCurrentProject().Name,
	}
	dir := path.Join(MustCurrentProject().Dir(), app.RelativePath())
	SolveDirOrCreate(dir)

	// Templatize as usual
	err := templates.CopyAndApply(logger, shared.Embed(fs), shared.NewDir("templates/application"), shared.NewDir(dir), app)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot copy and apply template")
	}

	err = SaveToDir[Application](&app, dir)
	if err != nil {
		return nil, logger.Errorf("cannot save applications configuration <%s>: %v", app.Name, err)
	}
	SetCurrentApplication(&app)

	err = MustCurrentProject().AddApplication(&ApplicationReference{
		Name: name,
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
	scope := WithScopeProjectOnly(opts...)
	return path.Join(scope.Project.Dir(), app.RelativePath())
}

func LoadApplicationFromDir(dir string) (*Application, error) {
	logger := shared.NewLogger("LoadApplicationFromDir<%s>", dir)
	config, err := LoadFromDir[Application](dir)
	if err != nil {
		return nil, err
	}
	config.RelativePathOverride = RelativePath(config.Name, MustCurrentProject().Relative(dir))
	for _, service := range config.Services {
		service.Application = config.Name
	}
	logger.Tracef("loaded applications configuration with %d services", len(config.Services))
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

func (s *Scope) WithProject(project *Project) *Scope {
	s.Project = project
	return s
}

func WithScope(opts ...Option) *Scope {
	// If we don't have a project Option, add the current project
	scope := &Scope{}
	for _, opt := range opts {
		opt(scope)
	}
	if scope.Project == nil {
		project, err := CurrentProject()
		shared.ExitOnError(err, "cannot get current project")
		scope.Project = project
	}

	if scope.Application == nil {
		app, err := CurrentApplication()
		shared.ExitOnError(err, "cannot get current application")
		scope.Application = app
	}
	return scope
}

func WithScopeProjectOnly(opts ...Option) *Scope {
	scope := &Scope{}
	for _, opt := range opts {
		opt(scope)
	}
	if scope.Project == nil {
		scope.Project = MustCurrentProject()
	}
	return scope
}

type Option func(scope *Scope)

func WithProject(project *Project) Option {
	return func(scope *Scope) {
		scope.Project = project
	}
}

func WithApplication(app *Application) Option {
	return func(scope *Scope) {
		scope.Application = app
	}
}

func LoadApplicationFromReference(ref *ApplicationReference, opts ...Option) (*Application, error) {
	logger := shared.NewLogger("LoadApplicationFromReference<%s>", ref.Name)
	apps, err := ListApplications(opts...)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot list applications")
	}
	for _, a := range apps {
		if a.Name == ref.Name {
			return a, nil
		}
	}
	return nil, logger.Errorf("cannot find application <%v>", ref)
}

func LoadApplicationFromName(name string, opts ...Option) (*Application, error) {
	logger := shared.NewLogger("LoadApplicationFromName<%s>", name)
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
	logger := shared.NewLogger("AddService")
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

func (app *Application) LoadServiceFromName(name string) (*Service, error) {
	return LoadServiceFromDir(path.Join(app.Dir(), name))
}

func (app *Application) Reference() *ApplicationReference {
	return &ApplicationReference{
		Name:                 app.Name,
		RelativePathOverride: app.RelativePathOverride,
	}
}

func (app *Application) RelativePath() string {
	if app.RelativePathOverride != nil {
		return *app.RelativePathOverride
	}
	return app.Name
}

func (app *Application) LoadServiceFromReference(ref *ServiceReference, opts ...Option) (*Service, error) {
	dir := path.Join(app.Dir(opts...), ref.RelativePath())
	return LoadServiceFromDir(dir)
}

func CurrentApplication(opts ...Option) (*Application, error) {
	logger := shared.NewLogger("CurrentApplication")
	if currentApplication != nil {
		return currentApplication, nil
	}
	scope := WithScopeProjectOnly(opts...)
	current := scope.Project.Current()

	all, err := ListApplications(opts...)
	if err != nil {
		return nil, logger.Wrapf(err, "Listing applications in project <%s>", MustCurrentProject().Name)
	}
	if len(all) == 0 {
		return nil, NoApplicationError{Project: MustCurrentProject().Name}
	}
	if len(all) == 1 {
		return all[0], nil
	}
	for _, app := range all {
		if app.Name == current {
			currentApplication = app
			return app, nil
		}
	}
	return nil, logger.Errorf("cannot find current application <%s> in project <%s>", current, MustCurrentProject().Name)
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
	err := MustCurrentProject().SetCurrentApplication(app.Name)
	shared.ExitOnError(err, "cannot save current project")
}

func AddApplication(app *Application) error {
	for _, a := range MustCurrentProject().Applications {
		if a.Name == app.Name {
			return nil
		}
	}
	MustCurrentProject().Applications = append(MustCurrentProject().Applications, app.Reference())
	return nil
}

func ListApplications(opts ...Option) ([]*Application, error) {
	logger := shared.NewLogger("applications.List<%s>", MustCurrentProject().Dir())
	scope := WithScopeProjectOnly(opts...)
	var apps []*Application
	for _, app := range scope.Project.Applications {

		a, err := LoadApplicationFromDir(path.Join(scope.Project.Dir(), app.RelativePath()))
		if err != nil {
			return nil, logger.Errorf("cannot load applications configuration <%s>: %v", app.Name, err)
		}
		apps = append(apps, a)
	}
	return apps, nil
}

func FindApplicationUp(p string) (*Application, error) {
	logger := shared.NewLogger("FindApplicationUp")
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
