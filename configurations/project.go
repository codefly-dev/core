package configurations

import (
	"context"
	"path"
	"path/filepath"

	"github.com/google/uuid"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/codefly-dev/core/templates"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

const ProjectConfigurationName = "project.codefly.yaml"

type Project struct {
	Name string `yaml:"name"`
	// ID must be globally unique
	ID string `yaml:"id,omitempty"`

	Domain      string `yaml:"domain,omitempty"`
	Description string `yaml:"description,omitempty"`

	// Applications in the project
	Applications []*ApplicationReference `yaml:"applications"`

	// Environments in the project
	Environments []*EnvironmentReference `yaml:"environments"`

	// internal
	dir                  string
	applicationsRelative string
}

func (project *Project) Proto() *basev0.Project {
	return &basev0.Project{
		Name:        project.Name,
		Description: project.Description,
	}
}

// Dir is the directory of the project
func (project *Project) Dir() string {
	return project.dir
}

// Unique returns the unique name of the project
// Currently, we don't insure uniqueness across workspaces
func (project *Project) Unique() string {
	return project.Name
}

// ProjectReference is a reference to a project used by Workspace configuration
type ProjectReference struct {
	Name              string                  `yaml:"name"`
	Path              string                  `yaml:"path"`
	Applications      []*ApplicationReference `yaml:"applications"`
	ActiveApplication string                  `yaml:"active-application"`
}

func (ref *ProjectReference) String() string {
	return ref.Name
}

// GetActiveApplication returns the active application
// returns nil if no active application
func (ref *ProjectReference) GetActiveApplication(ctx context.Context) (*ApplicationReference, error) {
	if ref.ActiveApplication == "" {
		return nil, nil
	}
	return ref.GetApplicationFromName(ctx, ref.ActiveApplication)
}

func (ref *ProjectReference) GetApplicationFromName(ctx context.Context, applicationName string) (*ApplicationReference, error) {
	w := wool.Get(ctx).In("ProjectReference.GetActiveApplication", wool.NameField(ref.Name))
	for _, app := range ref.Applications {
		if app.Name == applicationName {
			return app, nil
		}
	}
	return nil, w.NewError("cannot find active application")
}

func (ref *ProjectReference) AddApplication(ctx context.Context, application *ApplicationReference) error {
	w := wool.Get(ctx).In("ProjectReference.AddApplicationReference", wool.NameField(ref.Name))
	for _, app := range ref.Applications {
		if app.Name == application.Name {
			return w.NewError("application already exists")
		}
	}
	ref.Applications = append(ref.Applications, application)
	return nil
}

// NewProject creates a new project
func NewProject(ctx context.Context, action *actionsv0.NewProject) (*Project, error) {
	w := wool.Get(ctx).In("NewProject", wool.NameField(action.Name))

	w.Trace("action", wool.PathField(action.Path))
	dir := path.Join(action.Path, action.Name)

	exists, err := shared.CheckDirectory(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check project directory")
	}
	if exists {
		return nil, w.NewError("project directory already exists")
	}

	_, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create project directory")
	}

	//// Generate UUID
	//id, err := uuid.NewUUID()
	//if err != nil {
	//	return nil, w.Wrapf(err, "cannot generate UUID")
	//}
	project := &Project{
		Name:                 action.Name,
		dir:                  dir,
		applicationsRelative: "applications",
	}
	err = project.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save project")
	}

	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), "templates/project", project.dir, project)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}

	return project, nil
}

func (project *Project) Save(ctx context.Context) error {
	return project.SaveToDirUnsafe(ctx, project.Dir())
}

func (project *Project) SaveToDirUnsafe(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("SaveProject", wool.NameField(project.Name))
	w.Debug("applications", wool.SliceCountField(project.Applications))
	err := project.preSave(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot pre-save project")
	}
	err = SaveToDir[Project](ctx, project, dir)
	if err != nil {
		return w.Wrapf(err, "cannot save project")
	}
	return nil
}

/*
Loaders
*/

// LoadProjectFromDirUnsafe loads a Project configuration from a directory
func LoadProjectFromDirUnsafe(ctx context.Context, dir string) (*Project, error) {
	w := wool.Get(ctx).In("LoadProjectFromDirUnsafe")
	var err error
	dir, err = shared.SolvePath(dir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := LoadFromDir[Project](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	project.dir = dir
	// For safety, generate UUID if not present
	if project.ID == "" {
		id, err := uuid.NewUUID()
		if err != nil {
			return nil, w.Wrapf(err, "cannot generate UUID")
		}
		project.ID = id.String()
	}
	err = project.postLoad(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return project, nil
}

func LoadProjectFromPath(ctx context.Context) (*Project, error) {
	w := wool.Get(ctx).In("LoadProjectFromPath")
	dir, err := FindUp[Project](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		w.Debug("no project found from path")
		return nil, nil
	}

	return LoadProjectFromDirUnsafe(ctx, *dir)
}

// LoadApplicationFromReference loads an application from a reference
func (project *Project) LoadApplicationFromReference(ctx context.Context, ref *ApplicationReference) (*Application, error) {
	w := wool.Get(ctx).In("Project.LoadApplicationFromReference", wool.NameField(ref.Name))
	dir := project.ApplicationPath(ctx, ref)
	app, err := LoadApplicationFromDirUnsafe(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load application")
	}
	return app, nil
}

// LoadApplicationFromName loads an application from a name
func (project *Project) LoadApplicationFromName(ctx context.Context, name string) (*Application, error) {
	w := wool.Get(ctx).In("LoadApplicationFromName", wool.NameField(name))
	for _, ref := range project.Applications {
		if ReferenceMatch(ref.Name, name) {
			return project.LoadApplicationFromReference(ctx, ref)
		}
	}
	return nil, w.NewError("cannot find application")
}

// LoadApplications returns the applications in the project
func (project *Project) LoadApplications(ctx context.Context) ([]*Application, error) {
	w := wool.Get(ctx).In("Project.ListApplications", wool.NameField(project.Name))
	var applications []*Application
	for _, ref := range project.Applications {
		app, err := project.LoadApplicationFromReference(ctx, ref)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load application: <%s>", ref.Name)
		}
		applications = append(applications, app)
	}
	return applications, nil
}

// ApplicationsNames returns the names of the applications in the project
func (project *Project) ApplicationsNames() []string {
	var names []string
	for _, app := range project.Applications {
		names = append(names, app.Name)
	}
	return names
}

// ApplicationPath returns the absolute path of an application
// Cases for Reference.Dir
// nil: relative path to project with name
// rel: relative path
// /abs: absolute path
func (project *Project) ApplicationPath(_ context.Context, ref *ApplicationReference) string {
	if ref.PathOverride == nil {
		return path.Join(project.Dir(), "applications", ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(project.Dir(), *ref.PathOverride)
}

// Internally we keep track of active application differently
func (project *Project) postLoad(_ context.Context) error {
	proto := project.Proto()
	return Validate(proto)
}

func (project *Project) preSave(_ context.Context) error {
	proto := project.Proto()
	return Validate(proto)
}

// ExistsApplication returns true if the application exists in the project
func (project *Project) ExistsApplication(name string) bool {
	for _, app := range project.Applications {
		if app.Name == name {
			return true
		}
	}
	return false
}

// AddApplicationReference adds an application to the project
func (project *Project) AddApplicationReference(app *ApplicationReference) error {
	for _, a := range project.Applications {
		if a.Name == app.Name {
			return nil
		}
	}
	project.Applications = append(project.Applications, app)
	return nil
}

// DeleteApplication deletes an application from the project
func (project *Project) DeleteApplication(ctx context.Context, name string) error {
	w := wool.Get(ctx).In("Project.DeleteApplication")
	if !project.ExistsApplication(name) {
		return w.NewError("application <%s> does not exist in project <%s>", name, project.Name)
	}
	var apps []*ApplicationReference
	for _, app := range project.Applications {
		if app.Name != name {
			apps = append(apps, app)
		}
	}
	project.Applications = apps
	return project.Save(ctx)
}

func (project *Project) FindEnvironment(environment string) (*Environment, error) {
	w := wool.Get(context.Background()).In("Project.FindEnvironment", wool.NameField(environment))
	if environment == "" {
		return nil, w.NewError("environment cannot be empty")
	}
	for _, ref := range project.Environments {
		if ref.Name == environment {
			return LoadEnvironmentFromReference(ref)
		}
	}
	return nil, w.NewError("unknown environment %s", environment)

}

func LoadEnvironmentFromReference(ref *EnvironmentReference) (*Environment, error) {
	return &Environment{Name: ref.Name}, nil
}

// DeleteServiceDependencies deletes all service dependencies from a project
func (project *Project) DeleteServiceDependencies(ctx context.Context, ref *ServiceReference) error {
	w := wool.Get(ctx).In("configurations.DeleteService", wool.NameField(ref.String()))
	for _, appRef := range project.Applications {
		app, err := project.LoadApplicationFromReference(ctx, appRef)
		if err != nil {
			return w.Wrapf(err, "cannot load application")
		}
		err = app.DeleteServiceDependencies(ctx, ref)
		if err != nil {
			return w.Wrapf(err, "cannot delete service dependencies")
		}
	}

	return nil
}

func (project *Project) Reference() *ProjectReference {
	return &ProjectReference{
		Name:         project.Name,
		Path:         project.Dir(),
		Applications: project.Applications,
	}
}

func (project *Project) LoadService(ctx context.Context, input *ServiceWithApplication) (*Service, error) {
	w := wool.Get(ctx).In("Project.LoadService", wool.NameField(input.Name))
	app, err := project.LoadApplicationFromName(ctx, input.Application)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load application")
	}
	return app.LoadServiceFromName(ctx, input.Name)
}

func ReloadProject(ctx context.Context, project *Project) (*Project, error) {
	return LoadProjectFromDirUnsafe(ctx, project.Dir())
}
