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
	"github.com/codefly-dev/golor"
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
				logger.WarnUnique(shared.NewUserWarning("You are running in a directory that is not part of a project. Using current project from context: <%s>.", cur.Name))
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

func NewProject(builder ProjectBuilder) error {
	logger := shared.NewLogger("NewProject<%s>", builder.ProjectName())
	err := builder.Fetch()
	if err != nil {
		return logger.Wrapf(err, "cannot fetch project builder")
	}
	name := builder.ProjectName()
	// Uniqueness of project Name is enforced
	if slices.Contains(KnownProjects(), name) {
		if !shared.Debug() {
			return shared.NewUserError("project <%s> already exists", name).WithSuggestion("Try to use a different name")
		}
	}
	if err := ValidateProjectName(name); err != nil {
		return logger.Wrapf(err, "invalid project name")
	}
	relativePath := builder.RelativePath()
	dir := path.Join(GlobalProjectRoot(), relativePath)
	err = shared.CreateDirIf(dir)
	shared.UnexpectedExitOnError(err, "cannot create default project directory")

	p := &Project{
		Name:         name,
		Style:        builder.Style(),
		Organization: MustCurrent().Organization,
		Domain:       ExtendDomain(MustCurrent().Domain, name),
		RelativePath: relativePath,
	}
	logger.TODO("Depending on style we want to do git init, etc...")
	logger.Debugf("to %s", dir)
	err = SaveToDir[Project](p, dir)
	shared.UnexpectedExitOnError(err, "cannot save project configuration")

	// Templatize as usual
	err = templates.CopyAndApply(logger, templates.NewEmbeddedFileSystem(fs), shared.NewDir("templates/project"), shared.NewDir(dir), p)
	if err != nil {
		return logger.Wrapf(err, "cannot copy and apply template")
	}

	// And set as current
	MustCurrent().CurrentProject = name
	MustCurrent().Projects = append(MustCurrent().Projects, &ProjectReference{
		Name:         name,
		RelativePath: name,
	})
	golor.Println(`#(blue)[Creating new project <{{.Name}}> at {{.NewDir}}]`, map[string]any{"Name": name, "NewDir": dir})
	SaveCurrent()
	return nil
}

func (p *Project) Unique() string {
	return p.Name
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
	p, err := LoadFromDir[Project](path.Join(GlobalProjectRoot(), reference.RelativePath))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project")
	}
	p.RelativePath = reference.RelativePath
	for _, app := range p.Applications {
		if app.RelativePath == "" {
			app.RelativePath = app.Name
		}
	}
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
		project, err := LoadProjectFromDir(ProjectPath(p.RelativePath))
		if err != nil {
			logger.Warn(err)
			continue
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (p *Project) Dir() string {
	return path.Join(GlobalProjectRoot(), p.RelativePath)
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
	conf.RelativePath = MustCurrent().Relative(dir)
	return conf, nil
}

func LoadProjectFromName(name string) (*Project, error) {
	logger := shared.NewLogger("LoadProjectFromName<%s>", name)
	reference, err := FindProjectReference(name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot find project reference")
	}
	return LoadProjectFromDir(ProjectPath(reference.RelativePath))
}

func (p *Project) Save() error {
	logger := shared.NewLogger("Project.Save<%s>", p.Name)
	if p.RelativePath == "" {
		return logger.Errorf("project location is not set")
	}
	dir := path.Join(GlobalProjectRoot(), p.RelativePath)
	logger.Tracef("relative path of project <%s>", dir)
	return p.SaveToDir(dir)
}

func (p *Project) SaveToDir(dir string) error {
	return SaveToDir(p, dir)
}

func (p *Project) ListServices() ([]*ServiceReference, error) {
	logger := shared.NewLogger("Project.ListServices")
	logger.Debugf("Listing services in <%s>", p.Dir())
	var references []*ServiceReference
	err := filepath.Walk(p.Dir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return logger.Errorf("error during walking root <%s>: %v", p.Dir(), err)
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
				Name:         config.Name,
				RelativePath: app.Relative(path),
				Application:  app.Name,
			}
			references = append(references, ref)

		}
		return nil
	})
	if err != nil {
		return nil, logger.Errorf("error during walking root <%s>: %v", p.Dir(), err)
	}
	return references, nil
}

func (p *Project) GetService(name string) (*Service, error) {
	logger := shared.NewLogger("Project.GetService")
	// Unique can be scoped to applications or not
	entries, err := p.ListServices()
	if err != nil {
		return nil, logger.Errorf("cannot list services for project <%s>: %v", p.Name, err)
	}
	for _, entry := range entries {
		if entry.Name == name {
			return LoadServiceFromReference(entry)
		}
	}
	return nil, logger.Errorf("cannot find service <%s> in project <%s>", name, p.Name)
}

func (p *Project) Relative(absolute string) string {
	s, err := filepath.Rel(p.Dir(), absolute)
	shared.ExitOnError(err, "cannot compute relative path from project")
	return s
}

func (p *Project) AddApplication(app *ApplicationReference) error {
	for _, a := range p.Applications {
		if a.Name == app.Name {
			return nil
		}
	}
	p.Applications = append(p.Applications, app)

	return p.SaveToDir(path.Join(GlobalProjectRoot(), p.RelativePath))
}

func (p *Project) AddProvider(provider *Provider) error {
	logger := shared.NewLogger("Project.AddProvider<%s>", provider.Name)
	for _, prov := range p.Providers {
		if prov.Name == provider.Name {
			return nil
		}
	}
	ref, err := provider.Reference()
	if err != nil {
		return logger.Wrapf(err, "cannot get reference")
	}
	p.Providers = append(p.Providers, *ref)

	return p.SaveToDir(path.Join(GlobalProjectRoot(), p.RelativePath))
}

func (p *Project) OtherApplications(app *Application) ([]*Application, error) {
	logger := shared.NewLogger("")
	apps, err := ListApplications(WithProject(p))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot list applications")
	}
	var others []*Application
	for _, other := range apps {
		if other.Unique() == app.Unique() {
			continue
		}
		others = append(others, other)
	}
	return others, nil
}

func (p *Project) GetPartial(name string) *Partial {
	for _, partial := range p.Partials {
		if partial.Name == name {
			return &partial
		}
	}
	return nil
}

func (p *Project) ApplicationByName(override string) (*Application, error) {
	apps, err := p.ListApplications()
	if err != nil {
		return nil, err
	}
	for _, app := range apps {
		if app.Name == override {
			return app, nil
		}
	}
	return nil, fmt.Errorf("cannot find application <%s> in project <%s>", override, p.Name)
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
	dir := path.Join(project.Dir(), ref.RelativePath)
	return LoadApplicationFromDir(dir)
}
