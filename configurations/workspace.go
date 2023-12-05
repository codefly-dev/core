package configurations

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"

	"github.com/codefly-dev/core/shared"
)

const WorkspaceConfigurationName = "codefly.yaml"

// Workspace configuration for codefly CLI
type Workspace struct {
	Name         string `yaml:"name"`
	Organization string `yaml:"organization"`
	Domain       string `yaml:"domain"`

	// Projects in the Workspace configuration
	Projects []*ProjectReference `yaml:"projects"`

	// Internal
	dir            string
	currentProject string
}

// NewWorkspace creates a new workspace
func NewWorkspace(ctx context.Context, action *v1actions.AddWorkspace) (*Workspace, error) {
	org, err := OrganizationFromProto(ctx, action.Organization)
	if err != nil {
		return nil, err
	}
	workspace := &Workspace{
		Name:         action.Name,
		Organization: org.Name,
		Domain:       org.Domain,
	}
	if action.Dir != "" {
		workspace.dir = action.Dir
		workspaceConfigDir = workspace.dir
	} else {
		workspace.dir = WorkspaceConfigurationDir()
	}
	return workspace, nil
}

// LoadWorkspaceFromDir loads a Workspace configuration from a directory
func LoadWorkspaceFromDir(ctx context.Context, dir string) (*Workspace, error) {
	logger := shared.GetBaseLogger(ctx).With("LoadWorkspaceFromDir")
	dir = SolveDir(dir)
	w, err := LoadFromDir[Workspace](dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load Workspace configuration")
	}
	w.dir = dir
	return w, nil
}

// Dir returns the absolute path to the Workspace configuration directory
func (w *Workspace) Dir() string {
	return w.dir
}

// Relative returns the relative path to the Workspace configuration directory
func (w *Workspace) Relative(dir string) string {
	rel, err := filepath.Rel(w.Dir(), dir)
	shared.ExitOnError(err, "cannot compute relative path from workspace")
	return rel
}

// LoadProject loads a project from  a reference
func (w *Workspace) LoadProject(ctx context.Context, ref *ProjectReference) (*Project, error) {
	logger := shared.GetBaseLogger(ctx).With("LoadProject<%s>", ref.Name)
	p, err := LoadProjectFromDir(w.ProjectPath(ref))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project")
	}
	err = p.Process()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot process project")
	}
	return p, nil
}

// ProjectPath returns the absolute path of a project
// Cases for Reference.Path
// nil: relative path to workspace with name
// rel: relative path
// /abs: absolute path
func (w *Workspace) ProjectPath(ref *ProjectReference) string {
	if ref.PathOverride == nil {
		return path.Join(w.Dir(), ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(w.Dir(), *ref.PathOverride)
}

// Save Workspaces
func (w *Workspace) Save(ctx context.Context, opts ...SaveOptionFunc) error {
	err := SaveToDir[Workspace](w, w.Dir(), opts...)
	if err != nil {
		return shared.GetBaseLogger(ctx).With("Saving Workspace configuration").Wrap(err)
	}
	return nil
}

// ProjectNames returns the names of the projects in the Workspace configuration
func (w *Workspace) ProjectNames() []string {
	var names []string
	for _, p := range w.Projects {
		names = append(names, p.Name)
	}
	return names
}

// WorkspaceConfigurationDir returns the directory where the Workspace configuration is stored
// Initialized to the default user folder
func WorkspaceConfigurationDir() string {
	return workspaceConfigDir
}

func init() {
	workspaceConfigDir = path.Join(HomeDir(), ".codefly")
}

/*

CLEAN


*/

func LoadCurrentProject() (*Project, error) {
	return nil, nil
	//logger := shared.NewLogger("LoadCurrentProject")
	//current := Workspace().CurrentProject()
	//if current == "" {
	//	return nil, shared.NewUserError("no current project")
	//}
	//reference, err := FindProjectReference(current)
	//if err != nil {
	//	return nil, shared.NewUserError("cannot find current project <%s> in Workspace configuration", current)
	//}
	//p, err := LoadProjectFromDir(path.Join(WorkspaceProjectRoot(), reference.OverridePath()))
	//if err != nil {
	//	return nil, logger.Wrapf(err, "cannot load project")
	//}
	//err = p.Process()
	//if err != nil {
	//	return nil, logger.Wrapf(err, "cannot process project")
	//}
}
func KnownProjects() []string {
	var names []string
	//for _, p := range workspace().Projects {
	//	names = append(names, p.Name)
	//}
	return names
}

func WorkspaceMatch(entry string, name string) bool {
	return entry == name || entry == fmt.Sprintf("%s*", name)
}

func FindProjectReference(name string) (*ProjectReference, error) {
	//for _, p := range Workspace().Projects {
	//	if WorkspaceMatch(p.Name, name) {
	//		return p, nil
	//	}
	//}
	return nil, fmt.Errorf("cannot find project <%s>", name)
}

var currentProject *Project

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

func (w *Workspace) SetCurrentProject(ctx context.Context, p *Project) error {
	logger := shared.GetBaseLogger(ctx).With("SetCurrentProject: %s", p.Name)
	currentProject = p
	if w.CurrentProject() == p.Name {
		return nil
	}
	for _, ref := range w.Projects {
		if ref.Name == p.Name {
			ref.Name = fmt.Sprintf("%s*", ref.Name)
		}
	}
	err := w.Save(ctx, SilentOverride())
	if err != nil {
		return logger.Wrap(err)
	}
	return nil
}

func (w *Workspace) CurrentProject() string {
	return w.currentProject
}

func (w *Workspace) DeleteProject(ctx context.Context, name string) error {
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
	err := w.Save(ctx)
	if err != nil {
		return shared.GetBaseLogger(ctx).With("Deleting project <%s>", name).Wrap(err)
	}
	return nil
}

// LoadCurrentWorkspace returns the current Workspace configuration
func LoadCurrentWorkspace() (*Workspace, error) {
	logger := shared.NewLogger("configurations.Current")
	if workspace != nil {
		return workspace, nil
	}
	logger.Tracef("getting current")
	g, err := LoadFromDir[Workspace](WorkspaceConfigurationDir())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load Workspace configuration")
	}
	g.dir = WorkspaceConfigurationDir()
	workspace = g
	for _, p := range g.Projects {
		if name, ok := strings.CutSuffix(p.Name, "*"); ok {
			p.Name = name
			g.currentProject = name
		}
	}
	return workspace, nil
}

func Reset() {
	workspace = nil
	currentProject = nil
}

// CurrentWorkspace returns the current Workspace configuration
func CurrentWorkspace(_ context.Context) (*Workspace, error) {
	if workspace == nil {
		g, err := LoadCurrentWorkspace()
		if err != nil {
			return nil, err
		}
		workspace = g
	}
	return workspace, nil
}

func SetCurrentWorkspace(w *Workspace) {
	workspace = w
}

var (
	workspaceConfigDir string
	// This is where the Workspace configuration is stored
	// default to ~/.codefly/.

	workspace *Workspace
)

func init() {
	workspaceConfigDir = path.Join(HomeDir(), ".codefly")
	//WorkspaceProjectRoot = path.Join(HomeDir(), "codefly")
}

func LoadWorkspaceConfiguration() {
	//logger := shared.NewLogger("configurations.LoadWorkspaceConfiguration")
	//p := Dir[Workspace](WorkspaceConfigDir)
	//logger.Debugf("from <%s>", p)
}

func OverrideWorkspaceConfigDir(dir string) {
	logger := shared.NewLogger("configurations.OverrideWorkspaceConfigDir")
	logger.Debugf("overriding Workspace workspace configuration directory to <%s>", dir)
	//WorkspaceConfigDir = dir
}

func OverrideWorkspaceProjectRoot(dir string) {
	logger := shared.NewLogger("configurations.OverrideWorkspaceProjectRoot")
	logger.Debugf("overriding Workspace project root to <%s>", dir)
	//WorkspaceProjectRoot = dir
}
