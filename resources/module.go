package resources

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

const (
	ModuleKind              = "module"
	ModuleConfigurationName = "module.codefly.yaml"
)

// An Module is a collection of services that are deployed together.
type Module struct {
	Kind         string  `yaml:"kind"`
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`

	Description string `yaml:"description,omitempty"`

	ServiceReferences []*ServiceReference `yaml:"services"`

	// internal
	dir string
}

func (mod *Module) Unique() string {
	return mod.Name
}

func (mod *Module) Proto(_ context.Context) (*basev0.Module, error) {
	proto := &basev0.Module{
		Name:        mod.Name,
		Description: mod.Description,
	}
	if err := Validate(proto); err != nil {
		return nil, err
	}
	return proto, nil
}

// Dir returns the directory of the module
func (mod *Module) Dir() string {
	return mod.dir
}

// An ModuleReference
type ModuleReference struct {
	Name          string              `yaml:"name"`
	PathOverride  *string             `yaml:"path,omitempty"`
	Services      []*ServiceReference `yaml:"services,omitempty"`
	ActiveService string              `yaml:"active-service,omitempty"`
}

func (ref *ModuleReference) String() string {
	return ref.Name
}

func (ref *ModuleReference) GetActive(ctx context.Context) (*ServiceReference, error) {
	w := wool.Get(ctx).In("configurations.ModuleReference.GetActiveService")
	if len(ref.Services) == 0 {
		return nil, w.NewError("no services")
	}
	if len(ref.Services) == 1 {
		return ref.Services[0], nil
	}
	if ref.ActiveService == "" {
		return nil, w.NewError("no active service")
	}
	for _, r := range ref.Services {
		if r.Name == ref.ActiveService {
			w.Debug("found active service", wool.Field("service", r.Name))
			return r, nil
		}
	}
	return nil, w.NewError("cannot find active service")
}

func (ref *ModuleReference) GetActiveService(ctx context.Context) (*ServiceReference, error) {
	w := wool.Get(ctx).In("configurations.ModuleReference.GetActiveService")
	if len(ref.Services) == 0 {
		return nil, w.NewError("no services")
	}
	if len(ref.Services) == 1 {
		return ref.Services[0], nil
	}
	if ref.ActiveService == "" {
		return nil, w.NewError("no active service")
	}
	return ref.GetServiceFromName(ctx, ref.ActiveService)
}

func (ref *ModuleReference) GetServiceFromName(ctx context.Context, serviceName string) (*ServiceReference, error) {
	w := wool.Get(ctx).In("configurations.ModuleReference.GetActiveService")
	for _, r := range ref.Services {
		if r.Name == serviceName {
			return r, nil
		}
	}
	return nil, w.NewError("cannot find active service")
}

func (ref *ModuleReference) AddService(_ context.Context, service *ServiceReference) error {
	for _, s := range ref.Services {
		if s.Name == service.Name {
			return nil
		}
	}
	ref.Services = append(ref.Services, service)
	return nil
}

type NewModuleInput struct {
	Name string
}

// NewModule creates an module in a workspace
func (workspace *Workspace) NewModule(ctx context.Context, action *actionsv0.NewModule) (*Module, error) {
	w := wool.Get(ctx).In("configurations.NewModule", wool.NameField(action.Name))
	if workspace.ExistsModule(action.Name) {
		return nil, w.NewError("module already exists: %s", action.Name)
	}
	// layout dictates the path of the module
	dir := workspace.layout.ModulePath(action.Name)

	mod := &Module{
		Kind: ModuleKind,
		Name: action.Name,
	}
	mod.WithDir(dir)

	err := workspace.AddModuleReference(mod.Reference())
	if err != nil {
		return nil, w.Wrapf(err, "cannot add module to workspace")
	}

	err = workspace.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save workspace configuration")
	}

	exists, err := shared.DirectoryExists(ctx, mod.dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory: %s", mod.dir)
	}
	if exists {
		return nil, w.NewError("directory %s already exists", mod.dir)
	}

	_, err = shared.CheckDirectoryOrCreate(ctx, mod.dir)

	if err != nil {
		return nil, w.Wrapf(err, "cannot create module directory")
	}
	err = mod.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save module configuration")
	}
	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), "templates/module", mod.dir, mod)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}

	return mod, nil
}

// WithRootModule creates the module in Flat layout
func (workspace *Workspace) WithRootModule(ctx context.Context) (*Module, error) {
	w := wool.Get(ctx).In("configurations.WithRootModule")
	if err := workspace.Valid(); err != nil {
		return nil, w.Wrap(err)
	}
	if workspace.ExistsModule(workspace.Name) {
		return nil, w.NewError("root module already exists")
	}

	mod := &Module{
		Kind: ModuleKind,
		Name: workspace.Name,
	}
	mod.WithDir(workspace.layout.ModulePath(workspace.Name))

	err := workspace.AddModuleReference(mod.Reference())
	if err != nil {
		return nil, w.Wrapf(err, "cannot add module to workspace")
	}

	err = mod.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save module configuration")
	}
	return mod, nil
}

func LoadModuleFromDirUnsafe(ctx context.Context, dir string) (*Module, error) {
	w := wool.Get(ctx).In("configurations.LoadModuleFromDirUnsafe", wool.DirField(dir))
	mod, err := LoadFromDir[Module](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = mod.postLoad(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot post load")
	}
	mod.dir = dir
	return mod, nil
}

// LoadModuleFromPath loads an module from a path
func LoadModuleFromPath(ctx context.Context) (*Module, error) {
	dir, err := FindUp[Module](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, nil
	}
	return LoadModuleFromDirUnsafe(ctx, *dir)
}

func (mod *Module) postLoad(ctx context.Context) error {
	for _, ref := range mod.ServiceReferences {
		ref.Module = mod.Name
	}
	_, err := mod.Proto(ctx)
	return err
}

func (mod *Module) SaveToDir(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("configurations.SaveToDir", wool.DirField(dir))
	if dir == "" {
		return w.NewError("can't save module to empty directory")
	}
	err := mod.preSave(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot pre-save")
	}
	return SaveToDir(ctx, mod, dir)
}

func (mod *Module) Save(ctx context.Context) error {
	return mod.SaveToDir(ctx, mod.Dir())
}

// Pre-save deals with some optimization
func (mod *Module) preSave(_ context.Context) error {
	for _, ref := range mod.ServiceReferences {
		// Don't write Module in yaml
		ref.Module = ""
	}
	return nil
}

func (mod *Module) AddServiceReference(_ context.Context, ref *ServiceReference) error {
	w := wool.Get(context.Background()).In("configurations.AddServiceReference", wool.NameField(ref.Name))
	w.Trace("adding service reference", wool.Field("service", ref))
	for _, s := range mod.ServiceReferences {
		if s.Name == ref.Name {
			return nil
		}
	}
	mod.ServiceReferences = append(mod.ServiceReferences, ref)
	return nil
}

func (mod *Module) GetServiceReferences(name string) (*ServiceReference, error) {
	for _, ref := range mod.ServiceReferences {
		if ref.Name == name {
			return ref, nil
		}
	}
	return nil, nil
}

func (mod *Module) Reference() *ModuleReference {
	return &ModuleReference{
		Name:         mod.Name,
		PathOverride: mod.PathOverride,
	}
}

// ExistsService returns true if the service exists in the module
func (mod *Module) ExistsService(ctx context.Context, name string) bool {
	w := wool.Get(ctx).In("configurations.ExistsService", wool.NameField(name))
	for _, s := range mod.ServiceReferences {
		if s.Name == name {
			return true
		}
	}
	w.Debug("current services", wool.Field("services", mod.ServiceReferences))
	return false
}

// ServicePath returns the absolute path of an Service
// Cases for Reference.Dir
// nil: relative path to module with name
// rel: relative path
// /abs: absolute path
func (mod *Module) ServicePath(_ context.Context, ref *ServiceReference) string {
	if ref.PathOverride == nil {
		return path.Join(mod.Dir(), "services", ref.Name)
	}
	if filepath.IsAbs(*ref.PathOverride) {
		return *ref.PathOverride
	}
	return path.Join(mod.Dir(), "services", *ref.PathOverride)
}

func (mod *Module) LoadServiceFromReference(ctx context.Context, ref *ServiceReference) (*Service, error) {
	w := wool.Get(ctx).In("configurations.LoadServiceFromReference", wool.Field("service", ref))
	dir := mod.ServicePath(ctx, ref)
	service, err := LoadServiceFromDir(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	service.module = mod.Name
	if err = service.postLoad(ctx); err != nil {
		return nil, w.Wrap(err)
	}
	return service, nil
}

// LoadServiceFromName loads a service from a module
// returns ResourceNotFound error if not found
func (mod *Module) LoadServiceFromName(ctx context.Context, name string) (*Service, error) {
	w := wool.Get(ctx).In("configurations.LoadServiceFromName", wool.NameField(name))
	for _, ref := range mod.ServiceReferences {
		if ReferenceMatch(ref.Name, name) {
			return mod.LoadServiceFromReference(ctx, ref)
		}
	}
	return nil, w.Wrap(shared.NewErrorResourceNotFound("service", name))
}

func (mod *Module) LoadServices(ctx context.Context) ([]*Service, error) {
	var services []*Service
	for _, ref := range mod.ServiceReferences {
		service, err := mod.LoadServiceFromReference(ctx, ref)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, nil
}

func ReloadModule(ctx context.Context, app *Module) (*Module, error) {
	return LoadModuleFromDirUnsafe(ctx, app.Dir())
}

// DeleteService deletes a service from an module
func (mod *Module) DeleteService(ctx context.Context, name string) error {
	w := wool.Get(ctx).In("configurations.DeleteService", wool.NameField(name))
	var services []*ServiceReference
	for _, s := range mod.ServiceReferences {
		if s.Name != name {
			services = append(services, s)
		}
	}
	mod.ServiceReferences = services
	err := mod.Save(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot save module")
	}
	err = os.RemoveAll(mod.ServicePath(ctx, &ServiceReference{Name: name}))
	if err != nil {
		return w.Wrapf(err, "cannot remove service directory")
	}
	return nil
}

func (mod *Module) PublicEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("Module::PublicEndpoints", wool.ThisField(mod))
	var publicEndpoints []*basev0.Endpoint
	// InitAndWait services
	services, err := mod.LoadServices(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load services")
	}
	for _, service := range services {
		for _, endpoint := range service.Endpoints {
			if endpoint.Visibility != VisibilityPublic {
				continue
			}
			endpoint.Module = mod.Name
			proto, err := endpoint.Proto()
			if err != nil {
				return nil, w.Wrapf(err, "cannot create info")
			}
			publicEndpoints = append(publicEndpoints, proto)
		}
	}
	return publicEndpoints, nil
}

func (mod *Module) DeleteServiceDependencies(ctx context.Context, ref *ServiceReference) error {
	w := wool.Get(ctx).In("Module::DeleteServiceDependencies", wool.ThisField(mod), wool.Field("service", ref))
	for _, serviceRef := range mod.ServiceReferences {
		service, err := mod.LoadServiceFromReference(ctx, serviceRef)
		if err != nil {
			return w.Wrapf(err, "can't load service from ref")
		}
		err = service.DeleteServiceDependencies(ctx, ref)
		if err != nil {
			return w.Wrapf(err, "can't delete service dependencies")
		}
	}
	return nil
}

func (mod *Module) WithDir(dir string) {
	mod.dir = dir
}

type NoModuleError struct {
	workspace string
}

func (e NoModuleError) Error() string {
	return fmt.Sprintf("no modules found in <%s>", e.workspace)
}
