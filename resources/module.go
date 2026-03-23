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
	"github.com/codefly-dev/wool"
)

const (
	ModuleKind              = "module"
	ModuleConfigurationName = "module.codefly.yaml"
)

// ModuleAgent is the agent kind for module templates
const ModuleAgent = AgentKind("codefly:module")

func init() {
	RegisterAgent(ModuleAgent, basev0.Agent_MODULE)
}

// InterfaceEndpoint declares a single endpoint that the module exposes to other modules.
type InterfaceEndpoint struct {
	Service    string `yaml:"service"`
	Endpoint   string `yaml:"endpoint"`
	Visibility string `yaml:"visibility,omitempty"` // "module" or "public"; defaults to "module"
}

// ModuleInterface declares the contract of a module: what it exposes to the outside world.
type ModuleInterface struct {
	Endpoints []*InterfaceEndpoint `yaml:"endpoints,omitempty"`
}

// An Module is a collection of services that are deployed together.
type Module struct {
	Kind         string  `yaml:"kind"`
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`

	Description string `yaml:"description,omitempty"`

	// Module interface: the formal contract for what this module exposes
	Interface *ModuleInterface `yaml:"interface,omitempty"`

	// Module template agent (if this module was created from a template)
	Agent *Agent `yaml:"agent,omitempty"`

	ServiceReferences     []*ServiceReference     `yaml:"services"`
	JobReferences         []*JobReference         `yaml:"jobs,omitempty"`
	ApplicationReferences []*ApplicationReference `yaml:"applications,omitempty"`

	// internal
	dir string

	// For flat layout: back-reference to workspace so Save() writes there instead of module.codefly.yaml
	flatWorkspace *Workspace `yaml:"-"`
}

func (mod *Module) Unique() string {
	return mod.Name
}

func (mod *Module) Proto(_ context.Context) (*basev0.Module, error) {
	proto := &basev0.Module{
		Name:        mod.Name,
		Description: mod.Description,
	}

	// Convert interface
	if mod.Interface != nil {
		protoInterface := &basev0.ModuleInterface{}
		for _, ie := range mod.Interface.Endpoints {
			protoInterface.Endpoints = append(protoInterface.Endpoints, &basev0.InterfaceEndpoint{
				Service:    ie.Service,
				Endpoint:   ie.Endpoint,
				Visibility: ie.Visibility,
			})
		}
		proto.Interface = protoInterface
	}

	// Convert agent
	if mod.Agent != nil {
		proto.Agent = mod.Agent.Proto()
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

// ServicesDir returns the services directory of the module
func (mod *Module) ServicesDir() string {
	return path.Join(mod.dir, "services")
}

// ApplicationsDir returns the applications directory of the module
func (mod *Module) ApplicationsDir() string {
	return path.Join(mod.dir, "applications")
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
		Kind:          ModuleKind,
		Name:          workspace.Name,
		flatWorkspace: workspace,
	}
	mod.WithDir(workspace.layout.ModulePath(workspace.Name))

	err := workspace.AddModuleReference(mod.Reference())
	if err != nil {
		return nil, w.Wrapf(err, "cannot add module to workspace")
	}

	// Flat layout: no separate module.codefly.yaml -- services are in workspace.codefly.yaml
	return mod, nil
}

func LoadModuleFromDir(ctx context.Context, dir string) (*Module, error) {
	w := wool.Get(ctx).In("configurations.LoadModuleFromDir", wool.DirField(dir))
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

// LoadModuleFromCurrentPath loads an module from a path
func LoadModuleFromCurrentPath(ctx context.Context) (*Module, error) {
	dir, err := FindUp[Module](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, nil
	}
	return LoadModuleFromDir(ctx, *dir)
}

func (mod *Module) postLoad(ctx context.Context) error {
	for _, ref := range mod.ServiceReferences {
		ref.Module = mod.Name
	}
	for _, ref := range mod.JobReferences {
		ref.Module = mod.Name
	}
	// Application references don't need module set since they use ApplicationReference
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
	if mod.flatWorkspace != nil {
		// Clear internal Module field from refs before persisting
		for _, ref := range mod.ServiceReferences {
			ref.Module = ""
		}
		mod.flatWorkspace.Services = mod.ServiceReferences
		return mod.flatWorkspace.Save(ctx)
	}
	return mod.SaveToDir(ctx, mod.Dir())
}

// Pre-save deals with some optimization
func (mod *Module) preSave(_ context.Context) error {
	for _, ref := range mod.ServiceReferences {
		// Don't write Module in yaml
		ref.Module = ""
	}
	for _, ref := range mod.JobReferences {
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
	return LoadModuleFromDir(ctx, app.Dir())
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

// ExposedEndpoints returns the endpoints declared in the module interface.
// Unlike PublicEndpoints which scans all services, this only returns formally declared exports.
func (mod *Module) ExposedEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("Module::ExposedEndpoints", wool.ThisField(mod))
	if mod.Interface == nil || len(mod.Interface.Endpoints) == 0 {
		return nil, nil
	}

	var exposed []*basev0.Endpoint
	for _, ie := range mod.Interface.Endpoints {
		service, err := mod.LoadServiceFromName(ctx, ie.Service)
		if err != nil {
			return nil, w.Wrapf(err, "interface references unknown service %q", ie.Service)
		}
		found := false
		for _, ep := range service.Endpoints {
			if ep.Name == ie.Endpoint {
				found = true
				ep.Module = mod.Name
				proto, err := ep.Proto()
				if err != nil {
					return nil, w.Wrapf(err, "cannot create endpoint proto")
				}
				exposed = append(exposed, proto)
				break
			}
		}
		if !found {
			return nil, w.NewError("interface references unknown endpoint %q on service %q", ie.Endpoint, ie.Service)
		}
	}
	return exposed, nil
}

// ValidateInterface checks that all interface endpoints reference valid services and endpoints
// with appropriate visibility (must be "module" or "public", not "private").
func (mod *Module) ValidateInterface(ctx context.Context) error {
	w := wool.Get(ctx).In("Module::ValidateInterface", wool.ThisField(mod))
	if mod.Interface == nil || len(mod.Interface.Endpoints) == 0 {
		return nil
	}

	for _, ie := range mod.Interface.Endpoints {
		// Default visibility to "module" if not set
		if ie.Visibility == "" {
			ie.Visibility = VisibilityModule
		}
		// Only "module" and "public" are valid for interface endpoints
		if ie.Visibility != VisibilityModule && ie.Visibility != VisibilityPublic {
			return w.NewError("interface endpoint %s/%s has invalid visibility %q (must be %q or %q)",
				ie.Service, ie.Endpoint, ie.Visibility, VisibilityModule, VisibilityPublic)
		}

		// Check service exists
		service, err := mod.LoadServiceFromName(ctx, ie.Service)
		if err != nil {
			return w.Wrapf(err, "interface references unknown service %q", ie.Service)
		}

		// Check endpoint exists and has compatible visibility
		found := false
		for _, ep := range service.Endpoints {
			if ep.Name == ie.Endpoint {
				found = true
				if ep.Visibility == VisibilityPrivate || ep.Visibility == "" {
					return w.NewError("interface exposes endpoint %s/%s but service marks it as private",
						ie.Service, ie.Endpoint)
				}
				break
			}
		}
		if !found {
			return w.NewError("interface references unknown endpoint %q on service %q", ie.Endpoint, ie.Service)
		}
	}
	return nil
}

// HasInterface returns true if the module has a declared interface
func (mod *Module) HasInterface() bool {
	return mod.Interface != nil && len(mod.Interface.Endpoints) > 0
}

// IsModule returns true if this agent is a module agent
func (p *Agent) IsModule() bool {
	return p.Kind == ModuleAgent
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

// Application management methods

// ExistsApplication returns true if the application exists in the module
func (mod *Module) ExistsApplication(ctx context.Context, name string) bool {
	w := wool.Get(ctx).In("Module.ExistsApplication", wool.NameField(name))
	for _, a := range mod.ApplicationReferences {
		if a.Name == name {
			return true
		}
	}
	w.Debug("current applications", wool.Field("applications", mod.ApplicationReferences))
	return false
}

// ApplicationPath returns the absolute path of an Application
func (mod *Module) ApplicationPath(_ context.Context, ref *ApplicationReference) string {
	return path.Join(mod.ApplicationsDir(), ref.Name)
}

// AddApplicationReference adds an application reference to the module
func (mod *Module) AddApplicationReference(_ context.Context, ref *ApplicationReference) error {
	w := wool.Get(context.Background()).In("Module.AddApplicationReference", wool.NameField(ref.Name))
	w.Trace("adding application reference", wool.Field("application", ref))
	for _, a := range mod.ApplicationReferences {
		if a.Name == ref.Name {
			return nil
		}
	}
	mod.ApplicationReferences = append(mod.ApplicationReferences, ref)
	return nil
}

// LoadApplicationFromReference loads an application from a reference
func (mod *Module) LoadApplicationFromReference(ctx context.Context, ref *ApplicationReference) (*Application, error) {
	w := wool.Get(ctx).In("Module.LoadApplicationFromReference", wool.Field("application", ref))
	dir := mod.ApplicationPath(ctx, ref)
	app, err := LoadApplicationFromDir(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	app.SetModule(mod.Name)
	return app, nil
}

// LoadApplicationFromName loads an application from a module by name
func (mod *Module) LoadApplicationFromName(ctx context.Context, name string) (*Application, error) {
	w := wool.Get(ctx).In("Module.LoadApplicationFromName", wool.NameField(name))
	for _, ref := range mod.ApplicationReferences {
		if ref.Name == name {
			return mod.LoadApplicationFromReference(ctx, ref)
		}
	}
	return nil, w.Wrap(shared.NewErrorResourceNotFound("application", name))
}

// LoadApplications loads all applications in the module
func (mod *Module) LoadApplications(ctx context.Context) ([]*Application, error) {
	var applications []*Application
	for _, ref := range mod.ApplicationReferences {
		app, err := mod.LoadApplicationFromReference(ctx, ref)
		if err != nil {
			return nil, err
		}
		applications = append(applications, app)
	}
	return applications, nil
}

// DeleteApplication deletes an application from a module
func (mod *Module) DeleteApplication(ctx context.Context, name string) error {
	w := wool.Get(ctx).In("Module.DeleteApplication", wool.NameField(name))
	var applications []*ApplicationReference
	for _, a := range mod.ApplicationReferences {
		if a.Name != name {
			applications = append(applications, a)
		}
	}
	mod.ApplicationReferences = applications
	err := mod.Save(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot save module")
	}
	err = os.RemoveAll(mod.ApplicationPath(ctx, &ApplicationReference{Name: name}))
	if err != nil {
		return w.Wrapf(err, "cannot remove application directory")
	}
	return nil
}

// NewApplication creates an application in a module
func (mod *Module) NewApplication(ctx context.Context, action *actionsv0.AddApplication) (*Application, error) {
	w := wool.Get(ctx).In("mod.NewApplication", wool.NameField(action.Name))
	if mod.ExistsApplication(ctx, action.Name) {
		// Check for override
		override := shared.GetOverride(ctx)
		if !override.Replace(action.Name) {
			return nil, w.NewError("application already exists")
		}
	}
	agent, err := LoadAgent(ctx, action.Agent)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent")
	}

	app := &Application{
		Kind:        "application",
		Name:        action.Name,
		Description: action.Description,
		Version:     "0.0.1",
		Agent:       agent,
		Spec:        make(map[string]any),
	}

	dir := path.Join(mod.ApplicationsDir(), action.Name)
	app.dir = dir

	_, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	err = mod.AddApplicationReference(ctx, &ApplicationReference{Name: action.Name})
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = mod.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return app, nil
}

type NoModuleError struct {
	workspace string
}

func (e NoModuleError) Error() string {
	return fmt.Sprintf("no modules found in <%s>", e.workspace)
}
