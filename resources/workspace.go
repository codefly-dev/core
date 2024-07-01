package resources

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"

	"github.com/codefly-dev/core/templates"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

const WorkspaceConfigurationName = "workspace.codefly.yaml"

type Workspace struct {
	Name string `yaml:"name"`

	Description string `yaml:"description,omitempty"`

	Layout string `yaml:"layout"`

	// Modules in the Workspace
	Modules []*ModuleReference `yaml:"modules,omitempty"`

	// internal
	dir string

	// helper
	layout Layout `yaml:"-"`
}

func (workspace *Workspace) Proto() (*basev0.Workspace, error) {
	proto := &basev0.Workspace{
		Name:        workspace.Name,
		Description: workspace.Description,
		Layout:      workspace.Layout,
	}
	if err := Validate(proto); err != nil {
		return nil, err
	}
	return proto, nil
}

// Dir is the directory of the
func (workspace *Workspace) Dir() string {
	return workspace.dir
}

func NewWorkspace(ctx context.Context, name string, layout string) (*Workspace, error) {
	w := wool.Get(ctx).In("New", wool.NameField(name))
	workspace := &Workspace{Name: name, Layout: layout}
	_, err := workspace.Proto()
	if err != nil {
		return nil, w.Wrapf(err, "cannot validate  name")
	}
	return workspace, nil
}

// CreateWorkspace creates a new Workspace
func CreateWorkspace(ctx context.Context, action *actionsv0.NewWorkspace) (*Workspace, error) {
	w := wool.Get(ctx).In("CreateWorkspace", wool.NameField(action.Name), wool.DirField(action.Path))

	dir := path.Join(action.Path, action.Name)

	exists, err := shared.DirectoryExists(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if exists {
		return nil, w.NewError(" directory already exists")
	}

	_, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create  directory")
	}

	workspace, err := NewWorkspace(ctx, action.Name, action.Layout)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create workspace")
	}
	workspace.WithDir(dir)

	// Create layout
	workspace.layout, err = NewLayout(ctx, workspace.Dir(), action.Layout, nil)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create layout")
	}

	// Scaffold the workspace
	template := fmt.Sprintf("templates/workspace/%s", workspace.Layout)
	err = templates.CopyAndApply(ctx, shared.Embed(fs), template, workspace.dir, workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}
	if workspace.Layout == LayoutKindFlat {
		_, err = workspace.WithRootModule(ctx)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create module for flat")
		}
	}

	err = workspace.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save ")
	}

	return workspace, nil
}

func (workspace *Workspace) Save(ctx context.Context) error {
	return workspace.SaveToDirUnsafe(ctx, workspace.Dir())
}

func (workspace *Workspace) SaveToDirUnsafe(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("Save", wool.NameField(workspace.Name))
	w.Debug("modules", wool.SliceCountField(workspace.Modules))
	serialized, err := workspace.preSave(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot pre-save ")
	}
	err = SaveToDir[Workspace](ctx, serialized, dir)
	if err != nil {
		return w.Wrapf(err, "cannot save ")
	}
	return nil
}

/*
Loaders
*/

// LoadWorkspaceFromDir loads a Workspace configuration from a directory
func LoadWorkspaceFromDir(ctx context.Context, dir string) (*Workspace, error) {
	w := wool.Get(ctx).In("LoadFromDir")
	var err error
	dir, err = shared.SolvePath(dir)
	if err != nil {
		return nil, w.Wrap(err)
	}

	workspace, err := LoadFromDir[Workspace](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	workspace.dir = dir

	err = workspace.postLoad(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return workspace, nil
}

func FindWorkspaceUp(ctx context.Context) (*Workspace, error) {
	w := wool.Get(ctx).In("LoadFromPath")
	dir, err := FindUp[Workspace](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		w.Debug("no  found from path")
		return nil, nil
	}
	return LoadWorkspaceFromDir(ctx, *dir)
}

// LoadModuleFromReference loads an module from a reference
func (workspace *Workspace) LoadModuleFromReference(ctx context.Context, ref *ModuleReference) (*Module, error) {
	w := wool.Get(ctx).In("Workspace::LoadModuleFromReference", wool.NameField(ref.Name))
	dir := workspace.ModulePath(ctx, ref)
	mod, err := LoadModuleFromDirUnsafe(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load module")
	}
	return mod, nil
}

// LoadModuleFromName loads an module from a name
func (workspace *Workspace) LoadModuleFromName(ctx context.Context, name string) (*Module, error) {
	w := wool.Get(ctx).In("Workspace::LoadModuleFromName", wool.NameField(name))
	for _, ref := range workspace.Modules {
		if ReferenceMatch(ref.Name, name) {
			return workspace.LoadModuleFromReference(ctx, ref)
		}
	}
	var present []string
	for _, ref := range workspace.Modules {
		present = append(present, ref.Name)
	}
	return nil, w.NewError("cannot find module <%s> in  <%s>, present: %v", name, workspace.Name, present)
}

// LoadModules returns the modules in the
func (workspace *Workspace) LoadModules(ctx context.Context) ([]*Module, error) {
	w := wool.Get(ctx).In("Workspace::.LoadModules", wool.NameField(workspace.Name))
	var modules []*Module
	for _, ref := range workspace.Modules {
		mod, err := workspace.LoadModuleFromReference(ctx, ref)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load module: <%s>", ref.Name)
		}
		modules = append(modules, mod)
	}
	return modules, nil
}

// ModulesNames returns the names of the modules in the
func (workspace *Workspace) ModulesNames() []string {
	var names []string
	for _, app := range workspace.Modules {
		names = append(names, app.Name)
	}
	return names
}

// ModulePath returns the absolute path of an module
// Cases for Reference.Dir
// nil: relative path to  with name
// rel: relative path
// /abs: absolute path
func (workspace *Workspace) ModulePath(ctx context.Context, ref *ModuleReference) string {
	w := wool.Get(ctx).In("Workspace::ModulePath", wool.Field("module ref", ref))
	if ref.PathOverride == nil {
		p := workspace.layout.ModulePath(ref.Name)
		w.Trace("no path override", wool.Path(p))
		return p
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(workspace.Dir(), *ref.PathOverride)
}

// postLoad ensures the workspace is valid after loading
func (workspace *Workspace) postLoad(ctx context.Context) error {
	w := wool.Get(ctx).In("Workspace::postLoad", wool.NameField(workspace.Name))
	_, err := workspace.Proto()
	if err != nil {
		return w.Wrapf(err, "cannot validate proto")
	}
	if workspace.Layout == LayoutKindFlat {
		workspace.Modules = []*ModuleReference{{Name: workspace.Name}}
	}
	workspace.layout, err = NewLayout(context.Background(), workspace.Dir(), workspace.Layout, nil)
	if err != nil {
		return w.Wrapf(err, "cannot create layout")
	}
	return err
}

func (workspace *Workspace) preSave(ctx context.Context) (*Workspace, error) {
	w := wool.Get(ctx).In("preSave", wool.NameField(workspace.Name))
	_, err := workspace.Proto()
	if err != nil {
		return nil, w.Wrapf(err, "cannot validate proto")
	}
	serialized := workspace.Clone()
	if workspace.Layout == LayoutKindFlat {
		serialized.Modules = nil
	}
	return serialized, nil
}

// ExistsModule returns true if the module exists in the
func (workspace *Workspace) ExistsModule(name string) bool {
	for _, app := range workspace.Modules {
		if app.Name == name {
			return true
		}
	}
	return false
}

// AddModuleReference adds an module to the
func (workspace *Workspace) AddModuleReference(modRef *ModuleReference) error {
	for _, mod := range workspace.Modules {
		if mod.Name == modRef.Name {
			return nil
		}
	}
	workspace.Modules = append(workspace.Modules, modRef)
	return nil
}

// DeleteModule deletes an module from the
func (workspace *Workspace) DeleteModule(ctx context.Context, name string) error {
	w := wool.Get(ctx).In(".DeleteModule")
	if !workspace.ExistsModule(name) {
		return w.NewError("module <%s> does not exist in  <%s>", name, workspace.Name)
	}
	var modRefs []*ModuleReference
	for _, modRef := range workspace.Modules {
		if modRef.Name != name {
			modRefs = append(modRefs, modRef)
		}
	}
	workspace.Modules = modRefs
	return workspace.Save(ctx)
}

// DeleteServiceDependencies deletes all service dependencies from a
func (workspace *Workspace) DeleteServiceDependencies(ctx context.Context, ref *ServiceReference) error {
	w := wool.Get(ctx).In("configurations.DeleteService", wool.NameField(ref.String()))
	mods, err := workspace.LoadModules(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot load services")
	}
	for _, mod := range mods {
		err = mod.DeleteServiceDependencies(ctx, ref)
		if err != nil {
			return w.Wrapf(err, "cannot delete service dependencies")
		}
	}

	return nil
}

// LoadService loads a service from a reference
// returns NotFoundError if not found
func (workspace *Workspace) LoadService(ctx context.Context, input *ServiceWithModule) (*Service, error) {
	w := wool.Get(ctx).In("Workspace::LoadService", wool.NameField(input.Name))
	mod, err := workspace.LoadModuleFromName(ctx, input.Module)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load module")
	}
	return mod.LoadServiceFromName(ctx, input.Name)
}

func Reload(ctx context.Context, workspace *Workspace) (*Workspace, error) {
	return LoadWorkspaceFromDir(ctx, workspace.Dir())
}

func (workspace *Workspace) LoadServices(ctx context.Context) ([]*Service, error) {
	w := wool.Get(ctx).In("Workspace.LoadServices")
	refs, err := workspace.LoadServiceWithModules(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service references")
	}
	var services []*Service
	for _, ref := range refs {
		svc, err := workspace.LoadService(ctx, ref)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load service: %s", ref.Name)
		}
		services = append(services, svc)
	}
	return services, nil
}

func (workspace *Workspace) LoadServiceWithModules(ctx context.Context) ([]*ServiceWithModule, error) {
	w := wool.Get(ctx).In("Workspace.LoadServices")
	var services []*ServiceWithModule
	for _, modRef := range workspace.Modules {
		mod, err := workspace.LoadModuleFromReference(ctx, modRef)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load module")
		}
		for _, svc := range mod.ServiceReferences {
			services = append(services, &ServiceWithModule{Name: svc.Name, Module: mod.Name})
		}
	}
	return services, nil
}

type NonUniqueServiceNameError struct {
	name string
}

func (n NonUniqueServiceNameError) Error() string {
	return fmt.Sprintf("service name %s is not unique in ", n.name)
}

// FindUniqueServiceAndModuleByName finds a service by name
// returns ResourceNotFound error if not found
func (workspace *Workspace) FindUniqueServiceAndModuleByName(ctx context.Context, name string) (*ServiceWithModule, error) {
	w := wool.Get(ctx).In("Workspace::FindUniqueServiceByName", wool.NameField(name))
	svcRef, err := ParseServiceWithOptionalModule(name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot parse service name")
	}
	if svcRef.Module != "" {
		return svcRef, nil
	}
	// We look at all the services and check if the name is unique
	var found *ServiceWithModule
	svcs, err := workspace.LoadServiceWithModules(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load services")
	}
	for _, s := range svcs {
		if s.Name == svcRef.Name {
			if found != nil {
				return nil, NonUniqueServiceNameError{name}
			}
			found = s
		}
	}
	if found == nil {
		return nil, shared.NewErrorResourceNotFound("service", name)
	}
	return found, nil
}

// FindUniqueServiceByName finds a service by name
// returns ResourceNotFound error if not found
func (workspace *Workspace) FindUniqueServiceByName(ctx context.Context, name string) (*Service, error) {
	w := wool.Get(ctx).In("Workspace::FindUniqueServiceByName", wool.NameField(name))
	unique, err := workspace.FindUniqueServiceAndModuleByName(ctx, name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot find unique service")
	}
	svc, err := workspace.LoadService(ctx, unique)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service")
	}
	return svc, nil

}

// Valid checks if the workspace is valid
func (workspace *Workspace) Valid() error {
	if workspace.layout == nil {
		return fmt.Errorf("layout is nil")
	}
	return nil
}

// WithDir sets the directory of the workspace
func (workspace *Workspace) WithDir(dir string) {
	workspace.dir = dir
}

// RootModule only applies to Flat layout
func (workspace *Workspace) RootModule(ctx context.Context) (*Module, error) {
	w := wool.Get(ctx).In("Workspace.RootModule")
	if workspace.Layout != LayoutKindFlat {
		return nil, w.NewError("root module only applies to flat layout")
	}
	return workspace.LoadModuleFromName(ctx, workspace.Name)
}

// Clone returns a copy of the workspace
func (workspace *Workspace) Clone() *Workspace {
	clone := *workspace
	return &clone
}

func (workspace *Workspace) RelativeDir(service *Service) (string, error) {
	return filepath.Rel(workspace.Dir(), service.Dir())
}
