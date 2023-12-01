package configurations

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/shared"
)

const GlobalConfigurationName = "codefly.yaml"

// Workspace configuration for codefly CLI
type Workspace struct {
	Organization string `yaml:"organization"`
	Domain       string `yaml:"domain"`

	// Projects in the global configuration
	Projects []*ProjectReference `yaml:"projects"`

	// Internal
	dir            string
	currentProject string
}

func LoadCurrentProject() (*Project, error) {
	logger := shared.NewLogger("LoadCurrentProject")
	current := Global().CurrentProject()
	if current == "" {
		return nil, shared.NewUserError("no current project")
	}
	reference, err := FindProjectReference(current)
	if err != nil {
		return nil, shared.NewUserError("cannot find current project <%s> in global configuration", current)
	}
	p, err := LoadProjectFromDir(path.Join(GlobalProjectRoot(), reference.RelativePath()))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project")
	}
	err = p.Process()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot process project")
	}
	return p, nil
}

func ListProjects() ([]*Project, error) {
	logger := shared.NewLogger("ListProjects")
	var projects []*Project
	for _, p := range Global().Projects {
		project, err := LoadProjectFromDir(ProjectPath(p.RelativePath()))
		if err != nil {
			return nil, logger.Wrapf(err, "cannot load project <%s>", p.Name)
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func KnownProjects() []string {
	var names []string
	for _, p := range Global().Projects {
		names = append(names, p.Name)
	}
	return names
}

func WorkspaceMatch(entry string, name string) bool {
	return entry == name || entry == fmt.Sprintf("%s*", name)
}

func FindProjectReference(name string) (*ProjectReference, error) {
	for _, p := range Global().Projects {
		if WorkspaceMatch(p.Name, name) {
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

func (w *Workspace) SetCurrentProject(p *Project) {
	currentProject = p
	if w.CurrentProject() == p.Name {
		return
	}
	for _, ref := range w.Projects {
		if ref.Name == p.Name {
			ref.Name = fmt.Sprintf("%s*", ref.Name)
		}
	}
	w.SaveCurrent(SilentOverride())
}

func (w *Workspace) AddProject(p *Project) {
	for _, project := range Global().Projects {
		if project.Name == p.Name {
			return
		}
	}
	w.Projects = append(Global().Projects, &ProjectReference{
		Name: p.Name,
	})
	w.SaveCurrent()
}

// A GlobalConfigurationInputer abstracts away global configuration and default of project creation
type GlobalConfigurationInputer interface {
	// Fetch instantiates the input
	Fetch() error
	// Organization is now global
	Organization() string
	// Domain associated with the organization
	Domain() string
}

// InitGlobal initializes the global configuration of codefly
func InitGlobal(getter GlobalConfigurationInputer) {
	logger := shared.NewLogger("configurations.InitCodefly")
	logger.Tracef("creating if needed global configuration dir: %v", globalConfigDir)

	dir := SolveDirOrCreate(globalConfigDir)

	if ExistsAtDir[Workspace](dir) {
		logger.Debugf("global configuration already exists and no override")
		return
	}
	logger.Debugf("to <%s>", dir)

	err := getter.Fetch()
	if err != nil {
		shared.UnexpectedExitOnError(err, "cannot fetch global configuration")
	}
	global := Workspace{
		dir:          dir,
		Organization: getter.Organization(),
		Domain:       getter.Domain(),
	}
	err = SaveToDir[Workspace](&global, dir, SkipOverride())
	shared.ExitOnError(err, "cannot save global configuration")
}

// Dir returns the absolute path to the global configuration directory
func (w *Workspace) Dir() string {
	return w.dir
}

func (w *Workspace) Relative(dir string) string {
	rel, err := filepath.Rel(w.Dir(), dir)
	shared.ExitOnError(err, "cannot compute relative path from workspace")
	return rel
}

func (w *Workspace) CurrentProject() string {
	return w.currentProject
}

func (w *Workspace) DeleteProject(name string) {
	var projects []*ProjectReference
	for _, p := range w.Projects {
		if p.Name == name {
			continue
		}
		projects = append(projects, p)
	}
	w.Projects = projects
	if w.currentProject == name {
		w.currentProject = ""
	}
	w.SaveCurrent()
}

// Current returns the current global configuration
func Current() (*Workspace, error) {
	logger := shared.NewLogger("configurations.Current")
	if global != nil {
		return global, nil
	}
	logger.Tracef("getting current")
	g, err := LoadFromDir[Workspace](GlobalConfigurationDir())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load global configuration")
	}
	g.dir = GlobalConfigurationDir()
	global = g
	for _, p := range g.Projects {
		if name, ok := strings.CutSuffix(p.Name, "*"); ok {
			p.Name = name
			g.currentProject = name
		}
	}
	return global, nil
}

func (w *Workspace) SaveCurrent(opts ...SaveOptionFunc) {
	err := SaveToDir[Workspace](w, w.Dir(), opts...)
	shared.UnexpectedExitOnError(err, "cannot save global configuration")
}

func Reset() {
	global = nil
	currentProject = nil
}

func GlobalConfigurationDir() string {
	return globalConfigDir
}

func GlobalProjectRoot() string {
	return globalProjectRoot
}

// Global returns the current global configuration
func Global() *Workspace {
	if global == nil {
		g, err := Current()
		shared.ExitOnError(err, "cannot load current global configuration")
		global = g
	}
	return global
}

// This is where the global configuration is stored
// default to ~/.codefly/.
var globalConfigDir string

// This is where we create projects from:
// default to ~/codefly

var (
	globalProjectRoot string
	global            *Workspace
)

func init() {
	globalConfigDir = path.Join(HomeDir(), ".codefly")
	globalProjectRoot = path.Join(HomeDir(), "codefly")
}

func LoadGlobalConfiguration() {
	logger := shared.NewLogger("configurations.LoadGlobalConfiguration")
	p := Path[Workspace](globalConfigDir)
	logger.Debugf("from <%s>", p)
}

func OverrideWorkspaceConfigDir(dir string) {
	logger := shared.NewLogger("configurations.OverrideWorkspaceConfigDir")
	logger.Debugf("overriding global workspace configuration directory to <%s>", dir)
	globalConfigDir = dir
}

func OverrideWorkspaceProjectRoot(dir string) {
	logger := shared.NewLogger("configurations.OverrideWorkspaceProjectRoot")
	logger.Debugf("overriding global project root to <%s>", dir)
	globalProjectRoot = dir
}
