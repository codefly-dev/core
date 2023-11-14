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
	CurrentApplication string                  `yaml:"current-application,omitempty"`

	// Partials are convenient way to run several applications
	Partials []Partial `yaml:"partials"`

	// Providers in the project
	Providers []ProviderReference `yaml:"providers"`
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
				//logger.WarnUnique(shared.NewUserWarning("You are running in a directory that is not part of a project. Using current project from context: <%s>.", cur.Name))
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
		if !shared.Debug() {
			return nil, shared.NewUserError("project <%s> already exists", name).WithSuggestion("Try to use a different name")
		}
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
	err = templates.CopyAndApply(logger, shared.Embed(fs), shared.NewDir("templates/project"), shared.NewDir(dir), p, nil)
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

func LoadCurrentProject() (*Project, error) {
	logger := shared.NewLogger("LoadCurrentProject")
	if MustCurrent().CurrentProject == "" {
		return nil, shared.NewUserError("no current project")
	}
	reference, err := FindProjectReference(MustCurrent().CurrentProject)
	if err != nil {
		return nil, shared.NewUserError("cannot find current project <%s> in global configuration", MustCurrent().CurrentProject)
	}
	p, err := LoadFromDir[Project](path.Join(GlobalProjectRoot(), reference.RelativePath()))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project")
	}
	//p.RelativePathOverride = reference.RelativePathOverride
	//for _, app := range p.Applications {
	//	if app.RelativePathOverride == "" {
	//		app.RelativePathOverride = app.Name
	//	}
	//}
	return p, err
}

func KnownProjects() []string {
	var names []string
	for _, p := range MustCurrent().Projects {
		names = append(names, p.Name)
	}
	return names
}

func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid project name")
	}
	return nil
}

func ListProjects() ([]*Project, error) {
	logger := shared.NewLogger("ListProjects")
	var projects []*Project
	for _, p := range MustCurrent().Projects {
		project, err := LoadProjectFromDir(ProjectPath(p.RelativePath()))
		if err != nil {
			logger.Warn(err)
			continue
		}
		projects = append(projects, project)
	}
	return projects, nil
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

func FindProjectReference(name string) (*ProjectReference, error) {
	for _, p := range MustCurrent().Projects {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("cannot find project <%s>", name)
}

func CurrentProject() (*Project, error) {
	logger := shared.NewLogger("CurrentProject")
	if currentProject == nil {
		project, err := LoadCurrentProject()
		if err != nil {
			return nil, logger.Wrapf(err, "cannot load current project")
		}
		currentProject = project
	}
	return currentProject, nil
}

func MustCurrentProject() *Project {
	if currentProject == nil {
		project, err := CurrentProject()
		shared.ExitOnError(err, "cannot load current project")
		currentProject = project
	}
	return currentProject
}

func SetCurrentProject(p *Project) {
	currentProject = p
	MustCurrent().CurrentProject = p.Name
	SaveCurrent()
}

func LoadProjectFromDir(dir string) (*Project, error) {
	logger := shared.NewLogger("LoadProjectFromDir<%s>", dir)
	conf, err := LoadFromDir[Project](dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project configuration")
	}
	return conf, nil
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
	if project.RelativePath == "" {
		return logger.Errorf("project location is not set")
	}
	dir := path.Join(GlobalProjectRoot(), project.RelativePath)
	logger.Tracef("relative path of project <%s>", dir)
	return project.SaveToDir(dir)
}

func (project *Project) SaveToDir(dir string) error {
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

func (project *Project) AddProvider(provider *Provider) error {
	logger := shared.NewLogger("Project.AddProvider<%s>", provider.Name)
	for _, prov := range project.Providers {
		if prov.Name == provider.Name {
			return nil
		}
	}
	ref, err := provider.Reference()
	if err != nil {
		return logger.Wrapf(err, "cannot get reference")
	}
	project.Providers = append(project.Providers, *ref)

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
	config, err := LoadFromDir[Application](dir)
	if err != nil {
		return nil, err
	}
	return config, nil
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
