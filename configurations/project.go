package configurations

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/golor"
	"os"
	"path"
	"path/filepath"
	"slices"
)

var currentProject *Project

const ProjectConfigurationName = "project.codefly.yaml"

type ApplicationEntry struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path"`
}

type Project struct {
	Name         string       `yaml:"name"`
	Style        ProjectStyle `yaml:"style"`
	Domain       string       `yaml:"domain"`
	Organization string       `yaml:"organization"`
	RelativePath string       `yaml:"relative-path"`

	// Applications in the project
	Applications       []ApplicationEntry `yaml:"applications"`
	CurrentApplication string             `yaml:"current-application"`

	// Libraries installed in the project
	//Libraries []*LibrarySummary `yaml:"libraries"`
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
	logger := shared.NewLogger("configurations.NewProject<%s>", builder.ProjectName())
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
	logger := shared.NewLogger("configurations.LoadCurrentProject")
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
	logger := shared.NewLogger("configurations.ListProjects")
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
	logger := shared.NewLogger("configurations.CurrentProject")
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
	logger := shared.NewLogger("configurations.LoadProjectFromDir<%s>", dir)
	conf, err := LoadFromDir[Project](dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project configuration")
	}
	conf.RelativePath = MustCurrent().Relative(dir)
	return conf, nil
}

func LoadProjectFromName(name string) (*Project, error) {
	logger := shared.NewLogger("configurations.LoadProjectFromName<%s>", name)
	reference, err := FindProjectReference(name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot find project reference")
	}
	return LoadProjectFromDir(ProjectPath(reference.RelativePath))
}

func (p *Project) Save() error {
	logger := shared.NewLogger("configurations.Project.Save<%s>", p.Name)
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
	logger := shared.NewLogger("configurations.Project.ListServices")
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
			override := ""
			if app.Name == MustCurrentApplication().Name {
				app = MustCurrentApplication()
			} else {
				override = app.Name
			}
			ref := &ServiceReference{
				Name:                config.Name,
				RelativePath:        app.Relative(path),
				ApplicationOverride: override,
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
	logger := shared.NewLogger("configurations.Project.GetService")
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

func AddApplication(app *ApplicationEntry) error {
	project := MustCurrentProject()
	for _, a := range project.Applications {
		if a.Name == app.Name {
			return nil
		}
	}
	project.Applications = append(project.Applications, *app)

	return project.SaveToDir(path.Join(GlobalProjectRoot(), project.RelativePath))
}

//
//func AddLibraryUsage(library *Library, destination string) error {
//	logger := shared.NewLogger("configurations.AddLibraryUsage<%s>", library.Name())
//	project := MustCurrentProject()
//	manager := NewLibraryManager(project.Libraries)
//	err := manager.Add(library, project.Relative(destination))
//	if err != nil {
//		return logger.Wrapf(err, "cannot add")
//	}
//	project.Libraries = manager.ToSummary()
//	return project.Save()
//}
