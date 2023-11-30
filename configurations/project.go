package configurations

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

var currentProject *Project

const ProjectConfigurationName = "project.codefly.yaml"

type Project struct {
	Name         string       `yaml:"name"`
	Style        ProjectStyle `yaml:"style"`
	Domain       string       `yaml:"domain"`
	Organization string       `yaml:"organization"`
	RelativePath string       `yaml:"relative-path,omitempty"`

	// Applications in the project
	Applications       []*ApplicationReference `yaml:"applications"`
	currentApplication string                  `yaml:"current-application,omitempty"`

	// Partials are convenient way to run several applications
	Partials []Partial `yaml:"partials"`

	// Providers in the project
	Providers []ProviderReference `yaml:"providers"`

	// Environments in the project
	Environments []EnvironmentReference `yaml:"environments"`
}

func (project *Project) Current() string {
	return project.currentApplication
}

func ProjectMatch(entry string, name string) bool {
	return entry == name || entry == fmt.Sprintf("%s*", name)
}

func MakeCurrent(entry string) string {
	if strings.HasSuffix(entry, "*") {
		return entry
	}
	return fmt.Sprintf("%s*", entry)
}

func (project *Project) Process() error {
	// Internally we keep track of current application differently
	for _, app := range project.Applications {
		if strings.HasSuffix(app.Name, "*") {
			app.Name = strings.TrimSuffix(app.Name, "*")
			project.currentApplication = app.Name
		}
	}
	return nil
}

func (project *Project) SetCurrent(name string) {
	for _, app := range project.Applications {
		if app.Name == name {
			project.currentApplication = name
			app.Name = MakeCurrent(name)
			return
		}
	}
}

func (project *Project) PreSave() error {
	project.SetCurrent(project.currentApplication)
	return nil
}

func ProjectConfiguration(current bool) (*Project, error) {
	logger := shared.NewLogger("build.ProjectCmd")
	var config *Project
	if !current {
		cur, err := os.Getwd()
		if err != nil {
			return nil, logger.Wrapf(err, "cannot get current directory")
		}
		config, err = FindUp[Project](cur)
		if err != nil {
			if strings.Contains(err.Error(), "reached root directory") {
				cur, err := CurrentProject()
				if err != nil {
					return nil, logger.Wrapf(err, "cannot load current project")
				}
				// logger.WarnUnique(shared.NewUserWarning("You are running in a directory that is not part of a project. Using current project from context: <%s>.", cur.Name))
				return cur, nil
			}
			return nil, err
		}
	} else {
		return CurrentProject()
	}
	return config, nil
}

type ProjectStyle string

const (
	ProjectStyleUnknown   ProjectStyle = "unknown"
	ProjectStyleMonorepo  ProjectStyle = "monorepo"
	ProjectStyleMultirepo ProjectStyle = "multirepo"
)

func NewStyle(style string) ProjectStyle {
	switch style {
	case string(ProjectStyleMonorepo):
		return ProjectStyleMonorepo
	case string(ProjectStyleMultirepo):
		return ProjectStyleMultirepo
	default:
		return ProjectStyleUnknown
	}
}

type ProjectBuilder interface {
	ProjectName() string
	RelativePath() string
	Fetch() error
	Style() ProjectStyle
}

type ProjectInput struct {
	Name string
}

func (p *ProjectInput) Style() ProjectStyle {
	return ProjectStyleMonorepo
}

func (p *ProjectInput) RelativePath() string {
	return p.Name
}

func (p *ProjectInput) ProjectName() string {
	return p.Name
}

func (p *ProjectInput) Fetch() error {
	return nil
}

func (p *ProjectInput) ProjectDir() string {
	return path.Join(GlobalProjectRoot(), p.Name)
}

func NewProject(name string) (*Project, error) {
	logger := shared.NewLogger("NewProject<%s>", name)
	if slices.Contains(KnownProjects(), name) {
		return LoadProjectFromName(name)
	}
	if err := ValidateProjectName(name); err != nil {
		return nil, logger.Wrapf(err, "invalid project name")
	}
	ref := &ProjectReference{Name: name}
	ref.WithRelativePath(name)
	dir := path.Join(GlobalProjectRoot(), ref.RelativePath())
	err := shared.CreateDirIf(dir)
	shared.UnexpectedExitOnError(err, "cannot create default project directory")

	p := &Project{
		Name:         name,
		Organization: MustCurrent().Organization,
		Domain:       ExtendDomain(MustCurrent().Domain, name),
		RelativePath: ref.RelativePath(),
	}
	logger.TODO("Depending on style we want to do git init, etc...")
	logger.Debugf("to %s", dir)
	err = SaveToDir[Project](p, dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project")
	}

	// Templatize as usual
	err = templates.CopyAndApply(logger, shared.Embed(fs), shared.NewDir("templates/project"), shared.NewDir(dir), p)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot copy and apply template")
	}

	// And set as current
	MustCurrent().CurrentProject = name
	MustCurrent().Projects = append(MustCurrent().Projects, ref)
	SaveCurrent()
	return p, nil
}

func (project *Project) Unique() string {
	return project.Name
}

func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid project name")
	}
	return nil
}

func (project *Project) Dir() string {
	return path.Join(GlobalProjectRoot(), project.RelativePath)
}

func ProjectPath(relativePath string) string {
	return path.Join(GlobalProjectRoot(), relativePath)
}

func RelativeProjectPath(p string) string {
	rel, err := filepath.Rel(GlobalProjectRoot(), p)
	shared.UnexpectedExitOnError(err, "cannot compute relative path")
	return rel
}

func LoadProjectFromDir(dir string) (*Project, error) {
	logger := shared.NewLogger("LoadProjectFromDir<%s>", dir)
	logger.Tracef("loading project from <%s>", dir)
	project, err := LoadFromDir[Project](dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project configuration")
	}
	err = project.Process()
	if err != nil {
		return nil, err
	}

	return project, nil
}

func LoadProjectFromName(name string) (*Project, error) {
	logger := shared.NewLogger("LoadProjectFromName<%s>", name)
	reference, err := FindProjectReference(name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot find project reference")
	}
	return LoadProjectFromDir(ProjectPath(reference.RelativePath()))
}

func (project *Project) Save() error {
	logger := shared.NewLogger("Project.Save<%s>", project.Name)

	dir := path.Join(GlobalProjectRoot(), project.RelativePath)
	logger.Tracef("relative path of project <%s>", dir)
	return project.SaveToDir(dir)
}

func (project *Project) SaveToDir(dir string) error {
	err := project.PreSave()
	if err != nil {
		return err
	}
	return SaveToDir(project, dir)
}

func (project *Project) ListServices() ([]*ServiceReference, error) {
	logger := shared.NewLogger("Project.ListServices")
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
			config, err := LoadServiceFromDir(filepath.Dir(path))
			if err != nil {
				return fmt.Errorf("cannot load service configuration for <%s>: %v", path, err)
			}
			app, err := FindApplicationUp(path)
			if err != nil {
				return fmt.Errorf("cannot find applications for service <%s>: %v", path, err)
			}
			ref := &ServiceReference{
				Name:                 config.Name,
				RelativePathOverride: config.RelativePathOverride,
				Application:          app.Name,
			}
			references = append(references, ref)

		}
		return nil
	})
	if err != nil {
		return nil, logger.Errorf("error during walking root <%s>: %v", project.Dir(), err)
	}
	return references, nil
}

func (project *Project) GetService(name string) (*Service, error) {
	logger := shared.NewLogger("Project.GetService")
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

	return project.SaveToDir(path.Join(GlobalProjectRoot(), project.RelativePath))
}

func (project *Project) OtherApplications(app *Application) ([]*Application, error) {
	logger := shared.NewLogger("")
	apps, err := ListApplications(WithProject(project))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot list applications")
	}
	var others []*Application
	for _, other := range apps {
		if other.Name == app.Name {
			continue
		}
		others = append(others, other)
	}
	return others, nil
}

func (project *Project) GetPartial(name string) (*Partial, error) {
	for _, partial := range project.Partials {
		if partial.Name == name {
			return &partial, nil
		}
	}
	return nil, fmt.Errorf("cannot find partial <%s> in project <%s>", name, project.Name)
}

func (project *Project) ApplicationByName(override string) (*Application, error) {
	apps, err := project.ListApplications()
	if err != nil {
		return nil, err
	}
	for _, app := range apps {
		if app.Name == override {
			return app, nil
		}
	}
	return nil, fmt.Errorf("cannot find application <%s> in project <%s>", override, project.Name)
}

func (project *Project) ListApplications() ([]*Application, error) {
	logger := shared.NewLogger("Project.ListApplications")
	var applications []*Application
	for _, ref := range project.Applications {
		app, err := project.LoadApplicationFromReference(ref)
		if err != nil {
			return nil, logger.Wrapf(err, "cannot load application <%s>", ref.Name)
		}
		applications = append(applications, app)
	}
	return applications, nil
}

func (project *Project) LoadApplicationFromReference(ref *ApplicationReference) (*Application, error) {
	dir := path.Join(project.Dir(), ref.Name)
	app, err := LoadFromDir[Application](dir)
	if err != nil {
		return nil, err
	}
	return app, nil
}

func (project *Project) AddPartial(partial Partial) error {
	for _, p := range project.Partials {
		if p.Name == partial.Name {
			return nil
		}
	}
	project.Partials = append(project.Partials, partial)
	return project.Save()
}

func (project *Project) CurrentApplication() (*Application, error) {
	cur := project.Current()
	for _, ref := range project.Applications {
		if ref.Name == cur {
			return project.LoadApplicationFromReference(ref)
		}
	}
	return nil, fmt.Errorf("current application not defined")
}

func (project *Project) FindEnvironment(environment string) (*Environment, error) {
	logger := shared.NewLogger("Project.FindEnvironment")
	if environment == "" {
		return nil, logger.Errorf("environment cannot be empty")
	}
	for _, ref := range project.Environments {
		if ref.Name == environment {
			return LoadEnvironmentFromReference(ref)
		}
	}
	return nil, logger.Errorf("unnown environment %s", environment)

}

func LoadEnvironmentFromReference(ref EnvironmentReference) (*Environment, error) {
	return &Environment{Name: ref.Name}, nil
}
