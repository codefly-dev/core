package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"

	"github.com/codefly-dev/core/shared"
)

const WorkspaceConfigurationName = "codefly.yaml"

// Workspace configuration for codefly CLI
type Workspace struct {
	Name         string       `yaml:"name"`
	Organization Organization `yaml:"organization,omitempty"`
	Domain       string       `yaml:"domain,omitempty"`

	// Projects in the Workspace configuration
	Projects []*ProjectReference `yaml:"projects"`

	// Configuration
	ProjectsRoot string `yaml:"projects-root"`

	// Internal
	dir           string
	activeProject string
}

// NewWorkspace creates a new workspace
func NewWorkspace(ctx context.Context, action *v1actions.AddWorkspace) (*Workspace, error) {
	logger := shared.GetLogger(ctx).With("NewWorkspace<%s>", action.Name)
	org, err := OrganizationFromProto(ctx, action.Organization)
	if err != nil {
		return nil, err
	}
	projectRoot := action.ProjectRoot
	if projectRoot == "" {
		projectRoot = defaultProjectsRoot
	}
	logger.Debugf("workspace project root: <%s>", projectRoot)
	workspace := &Workspace{
		Name:         action.Name,
		Organization: *org,
		Domain:       org.Domain,
		ProjectsRoot: projectRoot,
	}
	if action.Dir != "" {
		workspace.dir = action.Dir
		workspaceConfigDir = workspace.dir
	} else {
		workspace.dir = WorkspaceConfigurationDir()
	}
	return workspace, nil
}

// LoadWorkspace returns the active Workspace configuration
func LoadWorkspace(ctx context.Context) (*Workspace, error) {
	if workspace != nil {
		return workspace, nil
	}
	logger := shared.GetLogger(ctx).With("configurations.LoadWorkspace")
	logger.Tracef("loading active workspace in %s", WorkspaceConfigurationDir())
	dir := WorkspaceConfigurationDir()
	return LoadWorkspaceFromDirUnsafe(ctx, dir)
}

// LoadWorkspaceFromDirUnsafe loads a Workspace configuration from a directory
func LoadWorkspaceFromDirUnsafe(ctx context.Context, dir string) (*Workspace, error) {
	logger := shared.GetLogger(ctx).With("LoadWorkspaceFromDirUnsafe")
	dir = SolveDir(dir)
	w, err := LoadFromDir[Workspace](ctx, dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load Workspace configuration")
	}
	w.dir = dir
	err = w.postLoad(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot post load Workspace configuration")
	}
	return w, nil
}

func (workspace *Workspace) postLoad(_ context.Context) error {
	for _, ref := range workspace.Projects {
		if name, ok := strings.CutSuffix(ref.Name, "*"); ok {
			ref.Name = name
			workspace.activeProject = name
		}
	}
	return nil
}

// Pre-save deals with the * style of active
func (workspace *Workspace) preSave(_ context.Context) error {
	if len(workspace.Projects) == 1 {
		workspace.Projects[0].Name = MakeInactive(workspace.Projects[0].Name)
		return nil
	}
	for _, ref := range workspace.Projects {
		if ref.Name == workspace.activeProject {
			ref.Name = MakeActive(ref.Name)
		} else {
			ref.Name = MakeInactive(ref.Name)
		}
	}
	return nil
}

// Dir returns the absolute path to the Workspace configuration directory
func (workspace *Workspace) Dir() string {
	return workspace.dir
}

// ProjectRoot returns the absolute path to the Workspace project root
func (workspace *Workspace) ProjectRoot() string {
	return workspace.ProjectsRoot
}

// ReloadWorkspace a project configuration
func ReloadWorkspace(ctx context.Context, workspace *Workspace) (*Workspace, error) {
	updated, err := LoadWorkspaceFromDirUnsafe(ctx, workspace.Dir())
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// ReloadProject a project configuration
func (workspace *Workspace) ReloadProject(ctx context.Context, project *Project) (*Project, error) {
	return workspace.LoadProjectFromName(ctx, project.Name)
}

// LoadProjectFromReference loads a project from  a reference
func (workspace *Workspace) LoadProjectFromReference(ctx context.Context, ref *ProjectReference) (*Project, error) {
	logger := shared.GetLogger(ctx).With("LoadProject<%s>", ref.Name)
	p, err := workspace.LoadProjectFromDir(ctx, workspace.ProjectPath(ctx, ref))
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project")
	}
	return p, nil
}

// ProjectPath returns the absolute path of a project
// Cases for Reference.Path
// nil: relative path to workspace with name
// rel: relative path
// /abs: absolute path
func (workspace *Workspace) ProjectPath(_ context.Context, ref *ProjectReference) string {
	if ref.PathOverride == nil {
		return path.Join(workspace.ProjectRoot(), ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(workspace.ProjectRoot(), *ref.PathOverride)
}

// Save Workspaces
func (workspace *Workspace) Save(ctx context.Context) error {
	logger := shared.GetLogger(ctx).With("SaveWorkspace")
	logger.Tracef("saving at <%s>", workspace.Dir())
	err := SaveToDir[Workspace](ctx, workspace, workspace.Dir())
	if err != nil {
		return shared.GetLogger(ctx).With("Saving Workspace configuration").Wrap(err)
	}
	err = workspace.preSave(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot pre-save Workspace configuration")
	}
	return nil
}

func IsInitialized(_ context.Context) bool {
	return shared.DirectoryExists(WorkspaceConfigurationDir())
}

/*

Workspaces have a active project, so we don't always have to specify it

*/

// SetProjectActive sets the active project
func (workspace *Workspace) SetProjectActive(ctx context.Context, input *v1actions.SetProjectActive) error {
	if len(workspace.Projects) == 1 {
		workspace.activeProject = workspace.Projects[0].Name
		return nil
	}
	for _, ref := range workspace.Projects {
		if ref.Name == input.Name {
			ref.MarkAsActive()
		} else {
			ref.MarkAsInactive()
		}
	}
	return workspace.Save(ctx)
}

func (workspace *Workspace) ActiveProject(ctx context.Context) (*ProjectReference, error) {
	logger := shared.GetLogger(ctx).With("LoadActiveProject")
	if len(workspace.Projects) == 0 {
		return nil, logger.Errorf("no projects in Workspace configuration")
	}
	if len(workspace.Projects) == 1 {
		return workspace.Projects[0], nil
	}
	for _, ref := range workspace.Projects {
		if ref.Name == workspace.activeProject {
			return ref, nil
		}
	}
	return nil, logger.Errorf("no active project in Workspace configuration")
}

// LoadActiveProject loads the active project
func (workspace *Workspace) LoadActiveProject(ctx context.Context) (*Project, error) {
	logger := shared.GetLogger(ctx).With("LoadActiveProject")
	ref, err := workspace.ActiveProject(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load active project")
	}
	return workspace.LoadProjectFromReference(ctx, ref)
}

// ProjectNames returns the names of the projects in the Workspace configuration
func (workspace *Workspace) ProjectNames() []string {
	var names []string
	for _, p := range workspace.Projects {
		names = append(names, p.Name)
	}
	return names
}

// FindProjectReference finds a project reference by name
func (workspace *Workspace) FindProjectReference(name string) (*ProjectReference, error) {
	for _, p := range workspace.Projects {
		if ReferenceMatch(p.Name, name) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("cannot find project <%s>", name)
}

// LoadProjects loads all the projects in the Workspace
func (workspace *Workspace) LoadProjects(ctx context.Context) ([]*Project, error) {
	var projects []*Project
	for _, ref := range workspace.Projects {
		p, err := workspace.LoadProjectFromReference(ctx, ref)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// LoadProjectFromName loads a project from a name
func (workspace *Workspace) LoadProjectFromName(ctx context.Context, name string) (*Project, error) {
	logger := shared.GetLogger(ctx).With("LoadProjectFromName<%s>", name)
	ref, err := workspace.FindProjectReference(name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot find project reference")
	}
	return workspace.LoadProjectFromDir(ctx, workspace.ProjectPath(ctx, ref))
}

// LoadProjectFromDir loads a project from a directory
func (workspace *Workspace) LoadProjectFromDir(ctx context.Context, dir string) (*Project, error) {
	logger := shared.GetLogger(ctx).With("LoadProjectFromDir<%s>", dir)
	logger.Tracef("loading project from <%s>", dir)
	project, err := LoadFromDir[Project](ctx, dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project configuration")
	}
	project.dir = dir
	err = project.postLoad(ctx)
	if err != nil {
		return nil, err
	}

	return project, nil
}

/*
Global Workspace Configuration
*/

// WorkspaceConfigurationDir returns the directory where the Workspace configuration is stored
// Initialized to the default user folder
func WorkspaceConfigurationDir() string {
	return workspaceConfigDir
}

// ExistsProject returns true if the project exists
func (workspace *Workspace) ExistsProject(name string) bool {
	for _, p := range workspace.Projects {
		if p.Name == name {
			return true
		}
	}
	return false
}

var (
	workspaceConfigDir string
	// This is where the Workspace configuration is stored
	// default to ~/.codefly

	defaultProjectsRoot string
	// This is where the projects are stored
	// default to ~/codefly

)

func init() {
	workspaceConfigDir = path.Join(HomeDir(), ".codefly")
	defaultProjectsRoot = path.Join(HomeDir(), "codefly")
}

/*

CLEAN


*/

func (workspace *Workspace) DeleteProject(ctx context.Context, name string) error {
	project, err := workspace.LoadProjectFromName(ctx, name)
	if err != nil {
		return err
	}
	var projects []*ProjectReference
	for _, p := range workspace.Projects {
		if p.Name == name {
			continue
		}
		projects = append(projects, p)
	}
	workspace.Projects = projects
	err = workspace.Save(ctx)
	if err != nil {
		return shared.GetLogger(ctx).With("Deleting project <%s>", name).Wrap(err)
	}
	os.RemoveAll(project.Dir())
	return nil
}

var workspace *Workspace

func SetLoadWorkspaceUnsafe(w *Workspace) {
	workspace = w
}
