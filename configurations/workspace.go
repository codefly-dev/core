package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	wool "github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"
)

const WorkspaceConfigurationName = "workspace.codefly.yaml"

// Workspace configuration for codefly CLI
type Workspace struct {
	Name         string       `yaml:"name"`
	Organization Organization `yaml:"organization,omitempty"`
	Domain       string       `yaml:"domain,omitempty"`

	// Projects in the Workspace configuration
	Projects []*ProjectReference `yaml:"projects"`

	// Internal
	dir           string
	activeProject string
}

func (workspace *Workspace) Unique() string {
	return workspace.Name
}

// NewWorkspace creates a new workspace
func NewWorkspace(ctx context.Context, action *actionsv0.AddWorkspace) (*Workspace, error) {
	w := wool.Get(ctx).In("NewWorkspace", wool.NameField(action.Name))
	org, err := OrganizationFromProto(ctx, action.Organization)
	if err != nil {
		return nil, err
	}
	projectRoot := action.ProjectRoot
	w.Debug("ws project root", wool.DirField(projectRoot))
	ws := &Workspace{
		Name:         action.Name,
		Organization: *org,
		Domain:       org.Domain,
	}
	if action.Dir != "" {
		ws.dir = action.Dir
		workspaceConfigDir = ws.dir
	} else {
		ws.dir = WorkspaceConfigurationDir()
	}
	return ws, nil
}

func (workspace *Workspace) AddProjectReference(ctx context.Context, project *Project) error {
	w := wool.Get(ctx).In("Workspace::AddProject", wool.ThisField(workspace), wool.NameField(project.Name))
	if workspace.ExistsProject(project.Name) {
		return w.NewError("project already exists")
	}
	workspace.Projects = append(workspace.Projects, &ProjectReference{
		Name: project.Name,
		Path: project.Dir(),
	})
	return nil
}

func (workspace *Workspace) AddProject(ctx context.Context, project *Project) error {
	w := wool.Get(ctx).In("Workspace::AddProject", wool.ThisField(workspace), wool.NameField(project.Name))
	err := workspace.AddProjectReference(ctx, project)
	if err != nil {
		return w.Wrapf(err, "cannot add project reference")
	}
	err = workspace.Save(ctx)
	if err != nil {
		return w.Wrap(err)
	}
	return nil
}

// LoadWorkspace returns the active Workspace configuration
func LoadWorkspace(ctx context.Context) (*Workspace, error) {
	if workspace != nil {
		return workspace, nil
	}
	w := wool.Get(ctx).In("configurations.LoadWorkspace")
	w.Trace("loading active", wool.DirField(WorkspaceConfigurationDir()))
	dir := WorkspaceConfigurationDir()
	return LoadWorkspaceFromDirUnsafe(ctx, dir)
}

// LoadWorkspaceFromDirUnsafe loads a Workspace configuration from a directory
func LoadWorkspaceFromDirUnsafe(ctx context.Context, dir string) (*Workspace, error) {
	w := wool.Get(ctx).In("configurations.LoadWorkspace")
	var err error
	dir, err = SolveDir(dir)
	w.With(wool.DirField(dir)).Trace("resolved")
	if err != nil {
		return nil, w.Wrap(err)
	}
	ws, err := LoadFromDir[Workspace](ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load Workspace configuration")
	}
	ws.dir = dir
	err = ws.postLoad(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot post load Workspace configuration")
	}
	return ws, nil
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
	w := wool.Get(ctx).In("configurations.LoadProjectFromReference", wool.Field("ref", ref))
	p, err := workspace.LoadProjectFromDir(ctx, ref.Path)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load project")
	}
	return p, nil
}

// Save Workspaces
func (workspace *Workspace) Save(ctx context.Context) error {
	w := wool.Get(ctx).In("Workspace::Save", wool.DirField(workspace.Dir()))
	w.Trace("saving")
	err := SaveToDir[Workspace](ctx, workspace, workspace.Dir())
	if err != nil {
		return w.Wrap(err)
	}
	err = workspace.preSave(ctx)
	if err != nil {
		return w.Wrap(err)
	}
	return nil
}

func IsInitialized(ctx context.Context) (bool, error) {
	w := wool.Get(ctx)
	w.Info("checking if workspace is initialized")
	return shared.DirectoryExists(WorkspaceConfigurationDir()), nil
}

/*

Workspaces have a active project, so we don't always have to specify it

*/

// SetProjectActive sets the active project
func (workspace *Workspace) SetProjectActive(ctx context.Context, input *actionsv0.SetProjectActive) error {
	if len(workspace.Projects) == 1 {
		workspace.activeProject = workspace.Projects[0].Name
		return nil
	}
	for _, ref := range workspace.Projects {
		if ref.Name == input.Name {
			workspace.activeProject = input.Name
			ref.MarkAsActive()
		} else {
			ref.MarkAsInactive()
		}
	}
	return workspace.Save(ctx)
}

func (workspace *Workspace) ActiveProject(ctx context.Context) (*ProjectReference, error) {
	w := wool.Get(ctx).In("configurations.ActiveProject")
	if len(workspace.Projects) == 0 {
		return nil, w.NewError("no projects in Workspace configuration")
	}
	if len(workspace.Projects) == 1 {
		return workspace.Projects[0], nil
	}
	for _, ref := range workspace.Projects {
		if ref.Name == workspace.activeProject {
			return ref, nil
		}
	}
	return nil, w.NewError("no active project in Workspace configuration")
}

// LoadActiveProject loads the active project
func (workspace *Workspace) LoadActiveProject(ctx context.Context) (*Project, error) {
	w := wool.Get(ctx).In("configurations.LoadActiveProject")
	ref, err := workspace.ActiveProject(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load active project")
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
	w := wool.Get(ctx).In("Workspace::LoadProjectFromName", wool.NameField(name))
	ref, err := workspace.FindProjectReference(name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot find project reference")
	}
	return workspace.LoadProjectFromDir(ctx, ref.Path)
}

// LoadProjectFromDir loads a project from a directory
func (workspace *Workspace) LoadProjectFromDir(ctx context.Context, dir string) (*Project, error) {
	w := wool.Get(ctx).In("configurations.LoadProjectFromDir", wool.Field("dir", dir))
	w.Trace("loading")
	project, err := LoadFromDir[Project](ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load project configuration")
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
)

func init() {
	workspaceConfigDir = path.Join(shared.Must(HomeDir()), ".codefly")
}

/*

CLEAN


*/

func (workspace *Workspace) DeleteProject(ctx context.Context, name string) error {
	w := wool.Get(ctx).In("Workspace::DeleteProject", wool.ThisField(workspace), wool.NameField(name))
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
		return w.Wrap(err)
	}
	os.RemoveAll(project.Dir())
	return nil
}

var workspace *Workspace

func SetLoadWorkspaceUnsafe(w *Workspace) {
	workspace = w
}
