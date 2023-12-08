package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/codefly-dev/core/templates"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	"github.com/codefly-dev/core/shared"
)

const ProjectConfigurationName = "project.codefly.yaml"

type Project struct {
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`

	Domain       string `yaml:"domain"`
	Organization string `yaml:"organization"`
	Description  string `yaml:"description,omitempty"`

	// Applications in the project
	Applications []*ApplicationReference `yaml:"applications"`

	// Partials are a convenient way to run several applications
	Partials []*Partial `yaml:"partials"`

	// Providers in the project
	Providers []*ProviderReference `yaml:"providers"`

	// Environments in the project
	Environments []*EnvironmentReference `yaml:"environments"`

	// internal
	dir               string // actual dir
	activeApplication string // internal use
}

func (project *Project) Unique() string {
	return project.Name
}

// Workspace references Projects

// ProjectReference is a reference to a project used by Workspace configuration
type ProjectReference struct {
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`
}

func (ref *ProjectReference) String() string {
	return ref.Name
}

// MarkAsActive marks a project as active
func (ref *ProjectReference) MarkAsActive() {
	if !strings.HasSuffix(ref.Name, "*") {
		ref.Name = fmt.Sprintf("%s*", ref.Name)
	}
}

func (ref *ProjectReference) MarkAsInactive() {
	if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
		ref.Name = name
	}
}

// IsActive returns true if the project is marked as active
func (ref *ProjectReference) IsActive() (*ProjectReference, bool) {
	if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
		return &ProjectReference{Name: name, PathOverride: ref.PathOverride}, true
	}
	return ref, false
}

func (workspace *Workspace) NewProject(ctx context.Context, action *v1actions.AddProject) (*Project, error) {
	logger := shared.GetBaseLogger(ctx).With("NewProject<%s>", action.Name)
	if slices.Contains(workspace.ProjectNames(), action.Name) {
		return nil, logger.Errorf("project already exists in workspace: %s at %s", workspace.Name, workspace.Dir())
	}
	if err := ValidateProjectName(action.Name); err != nil {
		return nil, logger.Wrapf(err, "invalid project name")
	}

	ref := &ProjectReference{Name: action.Name, PathOverride: OverridePath(action.Name, action.Path)}
	dir := workspace.ProjectPath(ctx, ref)

	err := shared.CreateDirIf(dir)
	shared.UnexpectedExitOnError(err, "cannot create default project directory")

	p := &Project{
		Name:         action.Name,
		Organization: workspace.Organization,
		Domain:       ExtendDomain(workspace.Domain, action.Name),
		PathOverride: ref.PathOverride,

		dir: dir,
	}
	workspace.Projects = append(workspace.Projects, ref)

	err = p.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project")
	}

	err = workspace.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save workspace")
	}
	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), shared.NewDir("templates/project"), shared.NewDir(p.dir), p)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot copy and apply template")
	}
	return p, nil
}

func (project *Project) Save(ctx context.Context) error {
	return project.SaveToDirUnsafe(ctx, project.Dir())
}

func (project *Project) SaveToDirUnsafe(ctx context.Context, dir string) error {
	logger := shared.GetBaseLogger(ctx).With("SaveProject<%s>", project.Name)
	logger.Debugf("saving with #application <%d>", len(project.Applications))
	err := project.preSave(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot pre-save project")
	}
	err = SaveToDir[Project](ctx, project, dir)
	if err != nil {
		return logger.Wrapf(err, "cannot save project")
	}
	return nil
}

/*
Applications are parts of the project
*/

// LoadApplication loads the application based on the following rules:
// - if in a application directory, load it
// - otherwise, use the active application
// and returns the project and a boolean flag to indicate if it comes from path
func (project *Project) LoadApplication(ctx context.Context) (*Application, bool, error) {
	up, err := FindUp[Application](ctx)
	if err == nil {
		return up, true, nil
	}
	app, err := project.LoadActiveApplication(ctx)
	return app, false, err
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

// ApplicationsNames returns the names of the applications in the project
func (project *Project) ApplicationsNames() []string {
	var names []string
	for _, app := range project.Applications {
		names = append(names, app.Name)
	}
	return names
}

// ApplicationPath returns the absolute path of an application
// Cases for Reference.Path
// nil: relative path to project with name
// rel: relative path
// /abs: absolute path
func (project *Project) ApplicationPath(ctx context.Context, ref *ApplicationReference) string {
	if ref.PathOverride == nil {
		return path.Join(project.Dir(), ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(project.Dir(), *ref.PathOverride)
}

// LoadApplications returns the applications in the project
func (project *Project) LoadApplications(ctx context.Context) ([]*Application, error) {
	logger := shared.GetBaseLogger(ctx).With("Project.ListApplications")
	var applications []*Application
	for _, ref := range project.Applications {
		app, err := project.LoadApplicationFromReference(ctx, ref)
		if err != nil {
			return nil, logger.Wrapf(err, "cannot load application <%s>", ref.Name)
		}
		applications = append(applications, app)
	}
	return applications, nil
}

/*

CLEAN

*/

func (project *Project) Active() string {
	return project.activeApplication
}

func ProjectMatch(entry string, name string) bool {
	return entry == name || entry == fmt.Sprintf("%s*", name)
}

func (project *Project) postLoad() error {
	// Internally we keep track of active application differently
	for _, app := range project.Applications {
		if name, ok := strings.CutSuffix(app.Name, "*"); ok {
			app.Name = name
			project.activeApplication = name
		}
	}
	return nil
}

// Pre-save deals with the * style of active
func (project *Project) preSave(ctx context.Context) error {
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
func (project *Project) SetActiveApplication(ctx context.Context, name string) error {
	project.activeApplication = name
	return nil
}

// ActiveApplication returns the active application
func (project *Project) ActiveApplication() string {
	return project.activeApplication
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
func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid project name")
	}
	return nil
}

func (project *Project) Dir() string {
	return project.dir
}

func (project *Project) ListServices() ([]*ServiceReference, error) {
	logger := shared.NewLogger().With("Project.ListServices")
	logger.Debugf("Listing services in <%s>", project.Dir())
	var references []*ServiceReference
	err := filepath.Walk(project.Dir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return logger.Errorf("error during walking root <%s>: %v", project.Dir(), err)
		}

		if info.IsDir() {
			return nil // Skip directories but proceed to explore its contents
		}

		matched, err := filepath.Match(ServiceConfigurationName, filepath.Base(path))
		if err != nil {
			return logger.Errorf("error during matching <%s> with <%s>: %v", path, ApplicationConfigurationName, err)
		}

		if matched {
			//config, err := LoadServiceFromDir(filepath.Dir(path))
			//if err != nil {
			//	return fmt.Errorf("cannot load service configuration for <%s>: %v", path, err)
			//}
			//app, err := FindApplicationUp(path)
			//if err != nil {
			//	return fmt.Errorf("cannot find applications for service <%s>: %v", path, err)
			//}
			//ref := &ServiceReference{
			//	Name:                 config.Name,
			//	RelativePathOverride: config.RelativePathOverride,
			//	Application:          app.Name,
			//}
			//references = append(references, ref)

		}
		return nil
	})
	if err != nil {
		return nil, logger.Errorf("error during walking root <%s>: %v", project.Dir(), err)
	}
	return references, nil
}

func (project *Project) GetService(name string) (*Service, error) {
	logger := shared.NewLogger().With("Project.GetService")
	// Unique can be scoped to applications or not
	entries, err := project.ListServices()
	if err != nil {
		return nil, logger.Errorf("cannot list services for project <%s>: %v", project.Name, err)
	}
	for _, entry := range entries {
		if entry.Name == name {
			return LoadServiceFromReference(entry)
		}
	}
	return nil, logger.Errorf("cannot find service <%s> in project <%s>", name, project.Name)
}

func (project *Project) Relative(absolute string) string {
	s, err := filepath.Rel(project.Dir(), absolute)
	shared.ExitOnError(err, "cannot compute relative path from project")
	return s
}

func (project *Project) AddApplication(app *ApplicationReference) error {
	for _, a := range project.Applications {
		if a.Name == app.Name {
			return nil
		}
	}
	project.Applications = append(project.Applications, app)
	return nil
}

func (project *Project) GetPartial(name string) (*Partial, error) {
	for _, partial := range project.Partials {
		if partial.Name == name {
			return partial, nil
		}
	}
	return nil, fmt.Errorf("cannot find partial <%s> in project <%s>", name, project.Name)
}

func (project *Project) AddPartial(partial Partial) error {
	ctx := shared.NewContext()
	for _, p := range project.Partials {
		if p.Name == partial.Name {
			return nil
		}
	}
	project.Partials = append(project.Partials, &partial)
	return project.Save(ctx)
}

func (project *Project) FindEnvironment(environment string) (*Environment, error) {
	logger := shared.NewLogger().With("Project.FindEnvironment")
	if environment == "" {
		return nil, logger.Errorf("environment cannot be empty")
	}
	for _, ref := range project.Environments {
		if ref.Name == environment {
			return LoadEnvironmentFromReference(ref)
		}
	}
	return nil, logger.Errorf("unknown environment %s", environment)

}

func LoadEnvironmentFromReference(ref *EnvironmentReference) (*Environment, error) {
	return &Environment{Name: ref.Name}, nil
}
