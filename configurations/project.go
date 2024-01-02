package configurations

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"

	"github.com/codefly-dev/core/templates"

	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

const ProjectConfigurationName = "project.codefly.yaml"

type Project struct {
	Name string `yaml:"name"`

	Organization Organization `yaml:"organization"`
	Domain       string       `yaml:"domain,omitempty"`
	Description  string       `yaml:"description,omitempty"`

	// Applications in the project
	Applications []*ApplicationReference `yaml:"applications"`

	// Providers in the project
	Providers []*ProviderReference `yaml:"providers"`

	// Environments in the project
	Environments []*EnvironmentReference `yaml:"environments"`

	// internal
	dir               string
	activeApplication string // internal use
}

func (project *Project) Proto() *basev1.Project {
	return &basev1.Project{
		Name:         project.Name,
		Organization: project.Organization.Proto(),
		Description:  project.Description,
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
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

func (ref *ProjectReference) String() string {
	return ref.Name
}

// MarkAsActive marks a project as active using the * convention
func (ref *ProjectReference) MarkAsActive() {
	if !strings.HasSuffix(ref.Name, "*") {
		ref.Name = fmt.Sprintf("%s*", ref.Name)
	}
}

// MarkAsInactive marks a project as inactive using the * convention
func (ref *ProjectReference) MarkAsInactive() {
	if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
		ref.Name = name
	}
}

// IsActive returns true if the project is marked as active
// and return the clean reference for Loading
func (ref *ProjectReference) IsActive() (*ProjectReference, bool) {
	if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
		return &ProjectReference{Name: name, Path: ref.Path}, true
	}
	return ref, false
}

// NewProject creates a new project in a workspace
func (workspace *Workspace) NewProject(ctx context.Context, action *actionsv1.AddProject) (*Project, error) {
	w := wool.Get(ctx).In("NewProject", wool.NameField(action.Name), wool.DirField(workspace.Dir()))
	if slices.Contains(workspace.ProjectNames(), action.Name) {
		return nil, w.NewError("project already exists")
	}

	w.Trace("action", wool.PathField(action.Path))
	dir := path.Join(action.Path, action.Name)

	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create project directory")
	}

	project := &Project{
		Name:         action.Name,
		Organization: workspace.Organization,
		Domain:       ExtendDomain(workspace.Domain, action.Name),
		dir:          dir,
	}

	err = workspace.AddProject(ctx, project)
	if err != nil {
		return nil, w.Wrapf(err, "cannot add project to workspace")
	}

	err = project.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save project")
	}

	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), shared.NewDir("templates/project"), shared.NewDir(project.dir), project)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}
	return project, nil
}

func (project *Project) Save(ctx context.Context) error {
	return project.SaveToDirUnsafe(ctx, project.Dir())
}

func (project *Project) SaveToDirUnsafe(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("SaveProject<%s>", wool.NameField(project.Name))
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
	dir, err = SolveDir(dir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := LoadFromDir[Project](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	project.dir = dir
	err = project.postLoad(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return project, nil
}

func LoadProjectFromPath(ctx context.Context) (*Project, error) {
	dir, err := FindUp[Project](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, nil
	}
	return LoadProjectFromDirUnsafe(ctx, *dir)
}

// LoadActiveApplication decides which application is active:
// - only 1: it is active
// - more than 1: use the activeApplication internal field
func (project *Project) LoadActiveApplication(ctx context.Context) (*Application, error) {
	if len(project.Applications) == 0 {
		return nil, fmt.Errorf("no application in project")
	}
	if len(project.Applications) == 1 {
		return project.LoadApplicationFromReference(ctx, project.Applications[0])
	}
	if project.activeApplication == "" {
		return nil, fmt.Errorf("active application not defined")
	}
	return project.LoadApplicationFromName(ctx, project.activeApplication)
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
		return path.Join(project.Dir(), ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(project.Dir(), *ref.PathOverride)
}

// Internally we keep track of active application differently
func (project *Project) postLoad(_ context.Context) error {
	if len(project.Applications) == 1 {
		project.activeApplication = project.Applications[0].Name
		return nil
	}
	for _, ref := range project.Applications {
		if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
			ref.Name = name
			project.activeApplication = name
		}
	}
	return nil
}

// Internally we keep track of active application differently
func (project *Project) preSave(_ context.Context) error {
	if len(project.Applications) == 1 {
		project.Applications[0].Name = MakeInactive(project.Applications[0].Name)
		return nil
	}
	for _, ref := range project.Applications {
		if ref.Name == project.activeApplication {
			ref.Name = MakeActive(ref.Name)
		} else {
			ref.Name = MakeInactive(ref.Name)
		}
	}
	return nil
}

// SetActiveApplication sets the active application
func (project *Project) SetActiveApplication(_ context.Context, name string) error {
	project.activeApplication = name
	return nil
}

// ActiveApplication returns the active application
func (project *Project) ActiveApplication(_ context.Context) *string {
	if project.activeApplication == "" {
		return nil
	}
	return &project.activeApplication
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

// AddApplication adds an application to the project
func (project *Project) AddApplication(app *ApplicationReference) error {
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
	if active := project.ActiveApplication(ctx); shared.PointerEqual(active, name) {
		project.activeApplication = ""
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
