package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"

	"github.com/codefly-dev/core/templates"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

const WorkspaceConfigurationName = "workspace.codefly.yaml"

// WorkspaceGitops declares the git target ArgoCD / Flux watches for
// rendered manifests. Optional — when nil, gitops-aware tooling falls
// back to placeholders and emits warnings.
//
//	RepoURL: full SSH or HTTPS git remote, e.g.
//	         "git@github.com:my-org/saas-starter.git" or
//	         "https://github.com/my-org/saas-starter.git".
//	Path:    sub-tree within the repo where the rendered manifests
//	         live. Empty = repo root. Use this when the workspace is
//	         a sub-directory of a larger monorepo.
//	Branch:  branch ArgoCD Applications target (defaults to main when
//	         empty).
type WorkspaceGitops struct {
	RepoURL string `yaml:"repo-url,omitempty"`
	Path    string `yaml:"path,omitempty"`
	Branch  string `yaml:"branch,omitempty"`
}

type Workspace struct {
	Name string `yaml:"name"`

	Description string `yaml:"description,omitempty"`

	Layout string `yaml:"layout"`

	// Modules in the Workspace (used for non-flat layouts)
	Modules []*ModuleReference `yaml:"modules,omitempty"`

	// Services in flat layout (embedded directly, replaces module.codefly.yaml)
	Services []*ServiceReference `yaml:"services,omitempty"`

	// Jobs in flat layout (embedded directly, replaces module.codefly.yaml)
	Jobs []*JobReference `yaml:"jobs,omitempty"`

	// Environments declares deploy targets the CLI knows about.
	// Each entry can override cluster (kubeconfig), registry, namespace.
	// Empty list = legacy behavior: env is name-only, kubeconfig/registry
	// fall back to hardcoded defaults in cli/pkg/deployments + cli/cmd/build.
	Environments []*Environment `yaml:"environments,omitempty"`

	// Gitops declares where rendered manifests are committed for
	// ArgoCD/Flux to sync from. Used by `codefly deploy --render-only`
	// + the module-level scaffold to fill in Application repoURL fields.
	// nil = no gitops flow declared; CLI tools fall back to placeholders.
	Gitops *WorkspaceGitops `yaml:"gitops,omitempty"`

	Path string `yaml:"path,omitempty"`

	// internal
	dir string

	// helper
	layout Layout `yaml:"-"`
}

func (workspace *Workspace) Proto(_ context.Context) (*basev0.Workspace, error) {
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
	if workspace.Path != "" {
		return path.Join(workspace.dir, workspace.Path)
	}
	return workspace.dir
}

func NewWorkspace(ctx context.Context, name string, layout string) (*Workspace, error) {
	w := wool.Get(ctx).In("New", wool.NameField(name))
	if err := validateResourcePathComponent("workspace", name); err != nil {
		return nil, w.Wrap(err)
	}
	workspace := &Workspace{Name: name, Layout: layout}
	_, err := workspace.Proto(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot validate  name")
	}
	return workspace, nil
}

// CreateWorkspace creates a new Workspace
func CreateWorkspace(ctx context.Context, action *actionsv0.NewWorkspace) (createdWorkspace *Workspace, result error) {
	if action == nil {
		return nil, wool.Get(ctx).In("CreateWorkspace").NewError("workspace action is nil")
	}
	w := wool.Get(ctx).In("CreateWorkspace", wool.NameField(action.Name), wool.DirField(action.Path))
	if err := validateResourcePathComponent("workspace", action.Name); err != nil {
		return nil, w.Wrap(err)
	}

	dir := path.Join(action.Path, action.Name)

	exists, err := shared.DirectoryExists(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if exists {
		return nil, w.NewError(" directory already exists")
	}

	createdDir, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create  directory")
	}
	if !createdDir {
		return nil, w.NewError("workspace directory %s already exists", dir)
	}
	defer func() {
		if result == nil || !createdDir {
			return
		}
		if err := os.RemoveAll(dir); err != nil {
			result = errors.Join(result, w.Wrapf(err, "cannot remove partial workspace directory"))
		}
	}()

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
	if err := validateModuleReferencePath(ref); err != nil {
		return nil, wool.Get(ctx).In("Workspace::LoadModuleFromReference").Wrap(err)
	}
	w := wool.Get(ctx).In("Workspace::LoadModuleFromReference", wool.NameField(ref.Name))

	if workspace.Layout == LayoutKindFlat && ref.Name == workspace.Name {
		mod := &Module{
			Kind:              ModuleKind,
			Name:              workspace.Name,
			ServiceReferences: workspace.Services,
			JobReferences:     workspace.Jobs,
			dir:               workspace.Dir(),
			flatWorkspace:     workspace,
		}
		if err := mod.postLoad(ctx); err != nil {
			return nil, w.Wrapf(err, "cannot post-load flat module")
		}
		return mod, nil
	}

	dir := workspace.ModulePath(ctx, ref)
	mod, err := LoadModuleFromDir(ctx, dir)
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
	_, err := workspace.Proto(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot validate proto")
	}
	if err := workspace.validatePaths(); err != nil {
		return w.Wrapf(err, "invalid workspace path data")
	}
	if workspace.Layout == LayoutKindFlat {
		workspace.Modules = []*ModuleReference{{Name: workspace.Name}}

		// Backward compat: migrate from module.codefly.yaml if it exists
		moduleFile := path.Join(workspace.Dir(), ModuleConfigurationName)
		if ok, _ := shared.FileExists(ctx, moduleFile); ok {
			mod, loadErr := LoadFromDir[Module](ctx, workspace.Dir())
			if loadErr != nil {
				return w.Wrapf(loadErr, "cannot load legacy module for migration")
			}
			if pathErr := mod.validatePaths(); pathErr != nil {
				return w.Wrapf(pathErr, "legacy module contains invalid path data")
			}

			referenceConflict := hasServiceReferenceConflict(workspace.Services, mod.ServiceReferences) ||
				hasJobReferenceConflict(workspace.Jobs, mod.JobReferences)
			servicesBefore := len(workspace.Services)
			jobsBefore := len(workspace.Jobs)
			workspace.Services = mergeServiceReferences(workspace.Services, mod.ServiceReferences)
			workspace.Jobs = mergeJobReferences(workspace.Jobs, mod.JobReferences)
			if pathErr := workspace.validatePaths(); pathErr != nil {
				return w.Wrapf(pathErr, "migrated workspace contains invalid path data")
			}
			changed := len(workspace.Services) != servicesBefore || len(workspace.Jobs) != jobsBefore
			if changed {
				// Persist the complete destination before deleting the only legacy
				// copy. SaveToDir uses an atomic temp+rename write, so a crash leaves
				// either the old workspace plus module or the complete new workspace.
				clean := workspace.Clone()
				clean.Modules = nil
				if saveErr := SaveToDir[Workspace](ctx, clean, workspace.Dir()); saveErr != nil {
					return w.Wrapf(saveErr, "cannot save workspace after migration")
				}
				w.Info("migrated legacy module references into workspace",
					wool.Field("services", len(workspace.Services)-servicesBefore),
					wool.Field("jobs", len(workspace.Jobs)-jobsBefore))
			}

			// These fields have no representation in a flat Workspace. Keep the
			// legacy file so an automatic load can never discard them; an explicit
			// migration can decide where they belong.
			if referenceConflict || legacyModuleHasUnrepresentableData(mod) {
				w.Warn("legacy module contains data that cannot be represented in a flat workspace; keeping module.codefly.yaml")
			} else if removeErr := os.Remove(moduleFile); removeErr != nil {
				return w.Wrapf(removeErr, "cannot remove migrated legacy module")
			}
		}
	}
	workspace.layout, err = NewLayout(context.Background(), workspace.Dir(), workspace.Layout, nil)
	if err != nil {
		return w.Wrapf(err, "cannot create layout")
	}
	return err
}

func optionalPathValue(path *string) string {
	if path == nil {
		return ""
	}
	return *path
}

func hasServiceReferenceConflict(current, legacy []*ServiceReference) bool {
	paths := make(map[string]string, len(current))
	for _, ref := range current {
		if ref != nil {
			paths[ref.Name] = optionalPathValue(ref.PathOverride)
		}
	}
	for _, ref := range legacy {
		if ref == nil {
			continue
		}
		if path, exists := paths[ref.Name]; exists && path != optionalPathValue(ref.PathOverride) {
			return true
		}
	}
	return false
}

func hasJobReferenceConflict(current, legacy []*JobReference) bool {
	paths := make(map[string]string, len(current))
	for _, ref := range current {
		if ref != nil {
			paths[ref.Name] = optionalPathValue(ref.PathOverride)
		}
	}
	for _, ref := range legacy {
		if ref == nil {
			continue
		}
		if path, exists := paths[ref.Name]; exists && path != optionalPathValue(ref.PathOverride) {
			return true
		}
	}
	return false
}

func mergeServiceReferences(current, legacy []*ServiceReference) []*ServiceReference {
	seen := make(map[string]struct{}, len(current))
	for _, ref := range current {
		if ref != nil {
			seen[ref.Name] = struct{}{}
		}
	}
	for _, ref := range legacy {
		if ref == nil {
			continue
		}
		if _, exists := seen[ref.Name]; exists {
			continue
		}
		current = append(current, ref)
		seen[ref.Name] = struct{}{}
	}
	return current
}

func mergeJobReferences(current, legacy []*JobReference) []*JobReference {
	seen := make(map[string]struct{}, len(current))
	for _, ref := range current {
		if ref != nil {
			seen[ref.Name] = struct{}{}
		}
	}
	for _, ref := range legacy {
		if ref == nil {
			continue
		}
		if _, exists := seen[ref.Name]; exists {
			continue
		}
		current = append(current, ref)
		seen[ref.Name] = struct{}{}
	}
	return current
}

func legacyModuleHasUnrepresentableData(mod *Module) bool {
	return mod.Description != "" || mod.PathOverride != nil || mod.Interface != nil || mod.Agent != nil || len(mod.ApplicationReferences) > 0
}

func (workspace *Workspace) preSave(ctx context.Context) (*Workspace, error) {
	w := wool.Get(ctx).In("preSave", wool.NameField(workspace.Name))
	_, err := workspace.Proto(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot validate proto")
	}
	if err := workspace.validatePaths(); err != nil {
		return nil, w.Wrapf(err, "invalid workspace path data")
	}
	serialized := workspace.Clone()
	if workspace.Layout == LayoutKindFlat {
		serialized.Modules = nil
		// Clear the redundant Module field from ServiceReferences for flat layout
		// (Module is implied to be the workspace). Clone() is a shallow copy, so the
		// ServiceReference pointers are shared with the in-memory workspace — copy each
		// reference before blanking it to avoid corrupting the original.
		cleaned := make([]*ServiceReference, len(serialized.Services))
		for i, ref := range serialized.Services {
			cp := *ref
			cp.Module = ""
			cleaned[i] = &cp
		}
		serialized.Services = cleaned
		// For non-flat layouts, don't serialize Services
	} else {
		serialized.Services = nil
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
	if err := validateModuleReferencePath(modRef); err != nil {
		return err
	}
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

// FindEnvironment looks up a declared environment by name. Returns nil
// if the workspace declares no environments or the name doesn't match.
//
// Callers (cli/cmd/deploy, cli/cmd/build) treat a nil return as "fall
// back to legacy behavior" (bare-name env, hardcoded kubeconfig /
// registry defaults). That preserves backward-compat with workspace
// YAMLs that haven't been updated to declare environments.
//
// "local" is returned synthetically (LocalEnvironment) when not
// declared, so single-env dev workspaces don't have to add YAML
// boilerplate just to keep working.
func (workspace *Workspace) FindEnvironment(name string) *Environment {
	for _, env := range workspace.Environments {
		if env.Name == name {
			return env
		}
	}
	if name == "local" {
		return LocalEnvironment()
	}
	return nil
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
	mod, err := workspace.LoadModuleFromName(ctx, unique.Module)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load module")
	}
	svc, err := mod.LoadServiceFromName(ctx, unique.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service")
	}
	return svc, nil

}

// FindUniqueModuleServiceByName finds a service by name
// returns ResourceNotFound error if not found
func (workspace *Workspace) FindUniqueModuleServiceByName(ctx context.Context, name string) (*Service, *Module, error) {
	w := wool.Get(ctx).In("Workspace::FindUniqueServiceByName", wool.NameField(name))
	unique, err := workspace.FindUniqueServiceAndModuleByName(ctx, name)
	if err != nil {
		return nil, nil, w.Wrapf(err, "cannot find unique service")
	}
	mod, err := workspace.LoadModuleFromName(ctx, unique.Module)
	if err != nil {
		return nil, nil, w.Wrapf(err, "cannot load module")
	}
	svc, err := mod.LoadServiceFromName(ctx, unique.Name)
	if err != nil {
		return nil, nil, w.Wrapf(err, "cannot load service")
	}
	return svc, mod, nil

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
