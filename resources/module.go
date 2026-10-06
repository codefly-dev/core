package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/codefly-dev/core/wool"
)

// ModuleKind is the `kind` a module manifest declares; ModuleConfigurationName
// is the file that carries it.
const (
	ModuleKind              = "module"
	ModuleConfigurationName = "module.codefly.yaml"
)

// InterfaceEndpoint declares a single endpoint that the module exposes to other modules.
type InterfaceEndpoint struct {
	Service  string `yaml:"service"`
	Endpoint string `yaml:"endpoint"`
	// Visibility is the reach the module grants across its boundary for this
	// endpoint: "public", or "internal" — reachable from within the
	// composition, by whatever declares a dependency on it. It defaults to
	// "internal". An entry names nobody: which modules reach the endpoint is
	// derived from their declared dependencies (DeriveAllowModules).
	Visibility string `yaml:"visibility,omitempty"`
	// AllowModules is never a grant. The YAML key is refused by presence before
	// decoding (UnmarshalYAML) and a value set here in memory by validate: an
	// interface entry that listed its consumers would be the module naming
	// what composes it, which the boundary rules forbid.
	AllowModules []string `yaml:"-"`
	// Implements lists the published interface versions the endpoint serves,
	// each <publisher>/<name>@<version>. One endpoint can serve several: a
	// gRPC port carries several protobuf services, and often two major
	// versions of one side by side. It is what lets a consumer depend on an
	// interface instead of on this service.
	Implements []string `yaml:"implements,omitempty"`
}

// interfaceEndpointKeys are the keys an interface entry may carry, read from
// the type.
var interfaceEndpointKeys = yamlKeysOf(InterfaceEndpoint{})

// UnmarshalYAML decodes an interface entry after judging its keys: an unknown
// key and the forbidden allow-modules key, in any spelling with any value, are
// refused by presence BEFORE decoding loses them — the same two refusals an
// endpoint mapping meets, for the same reason: a module that listed its
// consumers would be naming what composes it.
func (ie *InterfaceEndpoint) UnmarshalYAML(node *yaml.Node) error {
	keys, _ := mappingKeys(node)
	for _, key := range keys {
		if canonicalKey(key) == allowModulesKeyCanonical {
			return fmt.Errorf("interface endpoint authors allow-modules (key %q): an allow-list is derived from the consumers' declared service dependencies, never written by the module it would grant — a module asks for what it consumes, and the target names nobody", key)
		}
		if !slices.Contains(interfaceEndpointKeys, key) {
			return fmt.Errorf("interface endpoint declares unknown key %q (an entry declares %s)", key, strings.Join(interfaceEndpointKeys, ", "))
		}
	}
	type plain InterfaceEndpoint
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*ie = InterfaceEndpoint(decoded)
	return nil
}

// InterfaceCapabilityExport declares the capability interfaces a service
// provides: configuration groups consumers read through a library driver
// rather than an endpoint they call.
type InterfaceCapabilityExport struct {
	Service    string   `yaml:"service"`
	Implements []string `yaml:"implements"`
}

// exportedVisibility is the reach this entry grants across module boundaries.
// An entry that names none exports at "internal": reachable from within the
// composition and from nowhere outside it.
func (ie *InterfaceEndpoint) exportedVisibility() Visibility {
	if ie.Visibility == "" {
		return VisibilityInternal
	}
	return ie.Visibility
}

// validate judges the entry on its own: a visibility an export can carry, and
// no allow-list — an export names nobody, whatever its reach. An authored
// allow-list is refused by name, the wildcard included, for the reason
// ValidateEndpointDeclaration refuses one on the endpoint: the list is derived
// from the consumers' declared dependencies, and a module that wrote it would
// be naming its own consumers.
func (ie *InterfaceEndpoint) validate() error {
	switch ie.exportedVisibility() {
	case VisibilityInternal, VisibilityPublic:
	default:
		return fmt.Errorf("interface endpoint %s/%s has invalid visibility %q (must be %q or %q)",
			ie.Service, ie.Endpoint, ie.Visibility, VisibilityInternal, VisibilityPublic)
	}
	if ie.AllowModules != nil {
		return fmt.Errorf("interface endpoint %s/%s authors allow-modules %q: an allow-list is derived from the consumers' declared service dependencies, never written by the module it would grant — a module asks for what it consumes, and the target names nobody",
			ie.Service, ie.Endpoint, ie.AllowModules)
	}
	return nil
}

// ModuleInterface declares the contract of a module: what it exposes to the
// outside world and which published interfaces that implements. It is the
// module's side of an Interface: the definition is published by whoever owns
// the interface, and a module states here that it implements it.
type ModuleInterface struct {
	Endpoints    []*InterfaceEndpoint         `yaml:"endpoints,omitempty"`
	Capabilities []*InterfaceCapabilityExport `yaml:"capabilities,omitempty"`
}

// An Module is a collection of services that are deployed together.
type Module struct {
	Kind         string  `yaml:"kind"`
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`

	Description string `yaml:"description,omitempty"`

	// ServiceEntry is the module's default runnable service. Commands that
	// operate from the module or a single-module workspace can use it without
	// prompting the user to choose from every service in the module.
	ServiceEntry string `yaml:"service-entry,omitempty"`

	// Module interface: the formal contract for what this module exposes
	Interface *ModuleInterface `yaml:"interface,omitempty"`

	// Module template agent (if this module was created from a template)
	Agent *Agent `yaml:"agent,omitempty"`

	ServiceReferences     []*ServiceReference     `yaml:"services"`
	JobReferences         []*JobReference         `yaml:"jobs,omitempty"`
	RunnableReferences    []*RunnableReference    `yaml:"runnables,omitempty"`
	ApplicationReferences []*ApplicationReference `yaml:"applications,omitempty"`

	// internal
	dir string

	// declaredName is the name read from module.codefly.yaml on disk, retained
	// when the module is composed under a workspace-local alias that differs from
	// it (see adoptWorkspaceName). Empty means Name still matches the on-disk
	// name. It guards SaveToDir against writing the alias back over the real
	// source file at dir.
	declaredName string

	// For flat layout: back-reference to workspace so Save() writes there instead of module.codefly.yaml
	flatWorkspace *Workspace `yaml:"-"`

	// agentOverrides are the composing workspace's committed agent-overrides,
	// stamped by Workspace.LoadModuleFromReference and applied to every service
	// this module loads (see AgentOverridesKey).
	agentOverrides []AgentOverride `yaml:"-"`

	// workspace is the workspace that composed this module, stamped by
	// Workspace.LoadModuleFromReference. It binds the interface dependencies
	// of the services this module loads; a module loaded on its own has none.
	workspace *Workspace `yaml:"-"`
}

func (mod *Module) Unique() string {
	return mod.Name
}

func (mod *Module) Proto(_ context.Context) (*basev0.Module, error) {
	proto := &basev0.Module{
		Name:         mod.Name,
		Description:  mod.Description,
		ServiceEntry: mod.ServiceEntry,
	}

	// Convert interface
	if mod.Interface != nil {
		protoInterface := &basev0.ModuleInterface{}
		for _, ie := range mod.Interface.Endpoints {
			// Judged here as the loader judges it: a module built in memory
			// must not publish an export declaration a manifest could not
			// carry.
			if err := ie.validate(); err != nil {
				return nil, err
			}
			protoInterface.Endpoints = append(protoInterface.Endpoints, &basev0.InterfaceEndpoint{
				Service:    ie.Service,
				Endpoint:   ie.Endpoint,
				Visibility: ie.exportedVisibility(),
			})
		}
		proto.Interface = protoInterface
	}

	// Convert agent
	if mod.Agent != nil {
		agent, err := mod.Agent.Proto()
		if err != nil {
			return nil, err
		}
		proto.Agent = agent
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

// ModuleReference composes a module into a workspace.
//
// A reference composes a module into the workspace one of two ways:
//
//   - by committed location: PathOverride ("path:") points at the module
//     directory, relative to the workspace or absolute. Portable only when the
//     path is right for every checkout.
//   - by identity: Source ("source:") names the canonical repo (e.g.
//     "obin-ai/module-saas-starter"), Module ("module:") an optional subpath
//     within it, and Version ("version:") the wanted version. Where the module
//     physically lives is decided per-machine by the codefly.local.yaml overlay
//     (see LocalOverlay); with no overlay it defaults to the pinned artifact at
//     Version. This keeps committed config portable across every worktree.
type ModuleReference struct {
	Name          string              `yaml:"name"`
	PathOverride  *string             `yaml:"path,omitempty"`
	Source        string              `yaml:"source,omitempty"`
	Module        string              `yaml:"module,omitempty"`
	Version       string              `yaml:"version,omitempty"`
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
func (workspace *Workspace) NewModule(ctx context.Context, action *actionsv0.NewModule) (createdModule *Module, result error) {
	if action == nil {
		return nil, wool.Get(ctx).In("configurations.NewModule").NewError("module action is nil")
	}
	w := wool.Get(ctx).In("configurations.NewModule", wool.NameField(action.Name))
	if err := validateResourcePathComponent("module", action.Name); err != nil {
		return nil, w.Wrap(err)
	}
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

	exists, err := shared.DirectoryExists(ctx, mod.dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory: %s", mod.dir)
	}
	if exists {
		return nil, w.NewError("directory %s already exists", mod.dir)
	}

	createdDir, err := shared.CheckDirectoryOrCreate(ctx, mod.dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create module directory")
	}
	if !createdDir {
		return nil, w.NewError("module directory %s already exists", mod.dir)
	}
	originalReferences := append([]*ModuleReference(nil), workspace.Modules...)
	defer func() {
		if result == nil {
			return
		}
		workspace.Modules = originalReferences
		if removeErr := os.RemoveAll(mod.dir); removeErr != nil {
			result = errors.Join(result, w.Wrapf(removeErr, "cannot remove partial module directory"))
		}
	}()

	err = mod.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save module configuration")
	}
	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), "templates/module", mod.dir, mod)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}

	if err := workspace.AddModuleReference(mod.Reference()); err != nil {
		return nil, w.Wrapf(err, "cannot add module to workspace")
	}
	if err := workspace.Save(ctx); err != nil {
		return nil, w.Wrapf(err, "cannot save workspace configuration")
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
	if err := mod.ValidateInterface(ctx); err != nil {
		return nil, w.Wrapf(err, "cannot load module %s: invalid interface", mod.Name)
	}
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

// adoptWorkspaceName makes name the module's workspace-local identity and
// re-propagates it to the service and job references so services, endpoints, and
// dependency wiring key off the composed handle rather than the module's own
// declared name (which a coordinate-identified module may have renamed at its
// source). A no-op when the declared name already equals name.
func (mod *Module) adoptWorkspaceName(name string) {
	if mod.Name == name {
		return
	}
	mod.declaredName = mod.Name
	mod.Name = name
	for _, ref := range mod.ServiceReferences {
		ref.Module = name
	}
	for _, ref := range mod.JobReferences {
		ref.Module = name
	}
	for _, ref := range mod.RunnableReferences {
		ref.Module = name
	}
}

func (mod *Module) postLoad(ctx context.Context) error {
	if err := mod.validatePaths(); err != nil {
		return err
	}
	for _, ref := range mod.ServiceReferences {
		ref.Module = mod.Name
	}
	for _, ref := range mod.JobReferences {
		ref.Module = mod.Name
	}
	for _, ref := range mod.RunnableReferences {
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
	// A module composed under a workspace-local alias carries the alias in Name
	// while dir still points at its real (often shared, out-of-repo or worktree)
	// checkout. Persisting it would overwrite the on-disk `name` with the alias,
	// silently corrupting the source for every other workspace that composes it.
	if mod.declaredName != "" && mod.declaredName != mod.Name {
		return w.NewError("refusing to save module composed under alias <%s>: its on-disk name is <%s> at %s, and writing would overwrite the source; edit the module from its own checkout", mod.Name, mod.declaredName, dir)
	}
	if err := mod.validatePaths(); err != nil {
		return w.Wrap(err)
	}
	// preSave blanks the redundant Module field; restore it AFTER marshalling
	// so the in-memory references (shared with the workspace in flat layout)
	// are not left corrupted.
	restore := mod.preSave()
	defer restore()
	return SaveToDir(ctx, mod, dir)
}

func (mod *Module) Save(ctx context.Context) error {
	if mod.flatWorkspace != nil {
		// Publish the new references in memory only after persistence succeeds.
		// Otherwise a failed creation can roll back the module and its directory
		// while leaving a dangling reference for a later workspace save.
		pending := mod.flatWorkspace.Clone()
		pending.Services = mod.ServiceReferences
		pending.Runnables = mod.RunnableReferences
		if err := pending.Save(ctx); err != nil {
			return err
		}
		mod.flatWorkspace.Services = pending.Services
		mod.flatWorkspace.Runnables = pending.Runnables
		return nil
	}
	return mod.SaveToDir(ctx, mod.Dir())
}

// preSave blanks fields that are redundant on disk (Module is implied by the
// file's location) and returns a func that restores them. Callers MUST defer
// the returned restore so the in-memory model is not left corrupted.
func (mod *Module) preSave() func() {
	svcMods := make([]string, len(mod.ServiceReferences))
	for i, ref := range mod.ServiceReferences {
		svcMods[i] = ref.Module
		ref.Module = ""
	}
	jobMods := make([]string, len(mod.JobReferences))
	for i, ref := range mod.JobReferences {
		jobMods[i] = ref.Module
		ref.Module = ""
	}
	runnableMods := make([]string, len(mod.RunnableReferences))
	for i, ref := range mod.RunnableReferences {
		runnableMods[i] = ref.Module
		ref.Module = ""
	}
	return func() {
		for i, ref := range mod.ServiceReferences {
			ref.Module = svcMods[i]
		}
		for i, ref := range mod.JobReferences {
			ref.Module = jobMods[i]
		}
		for i, ref := range mod.RunnableReferences {
			ref.Module = runnableMods[i]
		}
	}
}

func (mod *Module) AddServiceReference(_ context.Context, ref *ServiceReference) error {
	if err := validateServiceReferencePath(ref); err != nil {
		return err
	}
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
	return mod.loadServiceFromReference(ctx, ref, true)
}

// loadServiceFromReference loads a service as its module composes it. Only a
// check of the module's own declarations may skip binding: it runs while the
// module is still being loaded, before any workspace can bind.
func (mod *Module) loadServiceFromReference(ctx context.Context, ref *ServiceReference, bind bool) (*Service, error) {
	if err := validateServiceReferencePath(ref); err != nil {
		return nil, wool.Get(ctx).In("configurations.LoadServiceFromReference").Wrap(err)
	}
	w := wool.Get(ctx).In("configurations.LoadServiceFromReference", wool.Field("service", ref))
	dir := mod.ServicePath(ctx, ref)
	service, err := loadServiceFromDir(ctx, dir, mod.Name)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if err := mod.applyInterface(service); err != nil {
		return nil, w.Wrap(err)
	}
	mod.applyAgentOverride(service)
	if bind {
		if err := mod.bindInterfaceDependencies(ctx, service); err != nil {
			return nil, w.Wrap(err)
		}
	}
	return service, nil
}

// bindInterfaceDependencies resolves the service's interface dependencies
// against the providers of the workspace that composed this module. A module
// loaded on its own leaves them unbound: reading its endpoints or exports needs
// no provider, and every path that uses a dependency refuses an unbound one.
func (mod *Module) bindInterfaceDependencies(ctx context.Context, service *Service) error {
	if mod.workspace == nil {
		return nil
	}
	return mod.workspace.BindInterfaceDependencies(ctx, service)
}

// applyInterface stamps each endpoint with the visibility the module's
// interface exports it at, and refuses an export the endpoint's own
// declaration contradicts — an endpoint declaring `exposure: public` that the
// interface omits or exports at internal would carry an outward address on a
// reach that stops inside the workspace — here, at module load, naming the
// interface entry in the author's own words rather than later, by the
// conversion or the derivation, in the exported ones. A declared interface is the module's export
// boundary: an endpoint it lists crosses module lines at the interface's
// visibility, and an endpoint it omits does not cross them at all, however the
// service itself declares it. Reachability within the module is unaffected —
// visibility never restricts that. This is the single place the boundary is
// applied, so every reader of an endpoint's visibility observes it: the static
// passes, the module graph, the run and deploy resolution, and the protos a
// module publishes.
func (mod *Module) applyInterface(service *Service) error {
	if !mod.HasInterface() {
		return nil
	}
	for _, endpoint := range service.Endpoints {
		// Where the endpoint lives is its Location and whether it is addressed
		// from outside is its Exposure, which the interface never touches: an
		// external endpoint is exported or kept like any other, and keeps
		// resolving from DNS either way.
		exported, listed := VisibilityPrivate, false
		for _, ie := range mod.Interface.Endpoints {
			if ReferenceMatch(ie.Service, service.Name) && ie.Endpoint == endpoint.Name {
				exported, listed = ie.exportedVisibility(), true
				break
			}
		}
		if endpoint.Exposed() && exported != VisibilityPublic {
			entry := fmt.Sprintf("exports it at %q", exported)
			if !listed {
				entry = "omits it, which keeps it private"
			}
			return fmt.Errorf("%w: module %q interface: endpoint %s/%s declares exposure %q — an address reachable from outside the workspace — but the interface %s; export it public, or drop its exposure",
				ErrInvalidEndpointDeclaration, mod.Name, service.Name, endpoint.Name, endpoint.Exposure, entry)
		}
		endpoint.exportAs(exported)
	}
	return nil
}

// Composition is the workspace that composed this module, which carries its
// provenance and that of every module beside it; nil for a module loaded on
// its own, which no verdict on an edge can be asked for.
func (mod *Module) Composition() *Workspace {
	return mod.workspace
}

// DependencyDeclarer is one declarer of service dependencies a module carries
// — a service, a job, a runnable or an application. The composition judges
// and derives over all of them alike: a runnable's runtime edge is handed
// addresses like a service's, so it is judged like one and asks like one.
type DependencyDeclarer struct {
	Kind         string
	Name         string
	Dependencies []*ServiceDependency
}

// LoadDependencyDeclarers loads every declarer of service dependencies this
// module carries, with each dependency's module defaulted to this one as a
// service's load defaults it, so a declarer never asks for a producer whose
// module it did not name.
func (mod *Module) LoadDependencyDeclarers(ctx context.Context) ([]DependencyDeclarer, error) {
	var declarers []DependencyDeclarer
	services, err := mod.LoadServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		declarers = append(declarers, DependencyDeclarer{Kind: "service", Name: service.Name, Dependencies: mod.defaultedDependencies(service.ServiceDependencies)})
	}
	jobs, err := mod.LoadJobs(ctx)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		declarers = append(declarers, DependencyDeclarer{Kind: "job", Name: job.Name, Dependencies: mod.defaultedDependencies(job.ServiceDependencies)})
	}
	runnables, err := mod.LoadRunnables(ctx)
	if err != nil {
		return nil, err
	}
	for _, runnable := range runnables {
		declarers = append(declarers, DependencyDeclarer{Kind: "runnable", Name: runnable.Name, Dependencies: mod.defaultedDependencies(runnable.ServiceDependencies)})
	}
	applications, err := mod.LoadApplications(ctx)
	if err != nil {
		return nil, err
	}
	for _, application := range applications {
		declarers = append(declarers, DependencyDeclarer{Kind: "application", Name: application.Name, Dependencies: mod.defaultedDependencies(application.ServiceDependencies)})
	}
	return declarers, nil
}

// defaultedDependencies returns the dependencies with an unnamed module read
// as this one, as a service's load reads it, without touching the declarer's
// own list.
func (mod *Module) defaultedDependencies(dependencies []*ServiceDependency) []*ServiceDependency {
	out := make([]*ServiceDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency == nil {
			continue
		}
		if dependency.Module == "" && !dependency.interfaceOnly() {
			copied := *dependency
			copied.Module = mod.Name
			dependency = &copied
		}
		out = append(out, dependency)
	}
	return out
}

// LoadServiceFromName loads a service from a module
// returns ResourceNotFound error if not found
func (mod *Module) LoadServiceFromName(ctx context.Context, name string) (*Service, error) {
	w := wool.Get(ctx).In("configurations.LoadServiceFromName", wool.NameField(name))
	if err := validateResourcePathComponent("service", name); err != nil {
		return nil, w.Wrap(err)
	}
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
	if err := validateResourcePathComponent("service", name); err != nil {
		return w.Wrap(err)
	}
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

// ExportedEndpoints returns the endpoints that cross this module's boundary: the
// ones its interface declares when it has one, else every endpoint whose own
// visibility is not private. Reach is the question — internal and public both
// cross module lines — never whether an outward address exists, which is
// exposure.
func (mod *Module) ExportedEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	if mod.HasInterface() {
		return mod.InterfaceEndpoints(ctx)
	}
	w := wool.Get(ctx).In("Module::ExportedEndpoints", wool.ThisField(mod))
	var exported []*basev0.Endpoint
	services, err := mod.LoadServices(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load services")
	}
	for _, service := range services {
		for _, endpoint := range service.Endpoints {
			if endpoint.Visibility != VisibilityInternal && endpoint.Visibility != VisibilityPublic {
				continue
			}
			endpoint.Module = mod.Name
			proto, err := endpoint.Proto()
			if err != nil {
				return nil, w.Wrapf(err, "cannot create info")
			}
			exported = append(exported, proto)
		}
	}
	return exported, nil
}

// InterfaceEndpoints returns the endpoints declared in the module interface:
// the formally declared exports, and nothing a service merely declares.
func (mod *Module) InterfaceEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("Module::InterfaceEndpoints", wool.ThisField(mod))
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

// ExportedEndpointsForPackage returns the interface endpoints to export as API
// contracts. It is InterfaceEndpoints guarded by a declared interface: a module
// without one cannot publish contracts by accident. A module whose interface
// declares only capabilities has declared one, and exports no endpoint.
func (mod *Module) ExportedEndpointsForPackage(ctx context.Context) ([]*basev0.Endpoint, error) {
	if mod.Interface == nil || (len(mod.Interface.Endpoints) == 0 && len(mod.Interface.Capabilities) == 0) {
		return nil, wool.Get(ctx).In("Module::ExportedEndpointsForPackage", wool.ThisField(mod)).
			NewError("module %s declares no interface; an interface is required to export API contracts", mod.Name)
	}
	return mod.InterfaceEndpoints(ctx)
}

// ValidateInterface checks that all interface endpoints reference valid services
// and endpoints, and that each entry names a visibility an export can carry. The
// endpoint's own visibility is not consulted: the interface entry is what
// exports it, so a service that keeps an endpoint private to itself and a module
// that exports it are not in disagreement.
func (mod *Module) ValidateInterface(ctx context.Context) error {
	w := wool.Get(ctx).In("Module::ValidateInterface", wool.ThisField(mod))
	if mod.Interface == nil {
		return nil
	}

	for _, ie := range mod.Interface.Endpoints {
		if err := ie.validate(); err != nil {
			return w.Wrap(err)
		}

		// Check service exists
		service, err := mod.loadDeclaredService(ctx, ie.Service)
		if err != nil {
			return w.Wrapf(err, "interface references unknown service %q", ie.Service)
		}

		// Check endpoint exists
		found := false
		for _, ep := range service.Endpoints {
			if ep.Name == ie.Endpoint {
				found = true
				break
			}
		}
		if !found {
			return w.NewError("interface references unknown endpoint %q on service %q", ie.Endpoint, ie.Service)
		}
	}
	capabilityServices := make(map[string]struct{}, len(mod.Interface.Capabilities))
	for _, capability := range mod.Interface.Capabilities {
		if capability == nil {
			return w.NewError("interface contains a nil capability")
		}
		if _, exists := capabilityServices[capability.Service]; exists {
			return w.NewError("interface declares the capabilities of service %q twice; list them in one entry", capability.Service)
		}
		capabilityServices[capability.Service] = struct{}{}
		if len(capability.Implements) == 0 {
			return w.NewError("interface capability entry for service %q implements nothing", capability.Service)
		}
		if _, err := mod.loadDeclaredService(ctx, capability.Service); err != nil {
			return w.Wrapf(err, "interface capability references unknown service %q", capability.Service)
		}
	}
	if _, err := mod.InterfaceProviders(); err != nil {
		return w.Wrap(err)
	}
	return nil
}

// loadDeclaredService loads a service as declared, without binding it.
func (mod *Module) loadDeclaredService(ctx context.Context, name string) (*Service, error) {
	if err := validateResourcePathComponent("service", name); err != nil {
		return nil, err
	}
	for _, ref := range mod.ServiceReferences {
		if ReferenceMatch(ref.Name, name) {
			return mod.loadServiceFromReference(ctx, ref, false)
		}
	}
	return nil, shared.NewErrorResourceNotFound("service", name)
}

// InterfaceProviders lists the interfaces this module implements and which of
// its services and endpoints implement each. It reads the module declaration
// only. An identity implemented twice in one module is refused: a consumer
// bound to it could not tell which of the two it reaches.
func (mod *Module) InterfaceProviders() ([]*InterfaceProvider, error) {
	if mod.Interface == nil {
		return nil, nil
	}
	var providers []*InterfaceProvider
	seen := make(map[string]string)
	// add registers what one entry implements. Within an entry, two versions
	// of one compatible line are refused: the higher already covers the lower,
	// and every consumer of that line would find two providers in one place.
	add := func(implements []string, service, endpoint string) error {
		var entry []*InterfaceIdentity
		for _, value := range implements {
			identity, err := ParseInterfaceIdentity(value)
			if err != nil {
				return fmt.Errorf("module %s: %w", mod.Name, err)
			}
			provider := &InterfaceProvider{Identity: identity, Module: mod.Name, Service: service, Endpoint: endpoint}
			if previous, exists := seen[identity.String()]; exists {
				return fmt.Errorf("module %s implements %s twice: by %s and by %s", mod.Name, identity, previous, provider.location())
			}
			for _, other := range entry {
				if other.Key() == identity.Key() && sameCompatibleLine(other, identity) {
					return fmt.Errorf("module %s: %s implements %s at both %s and %s, one compatible line; list only the higher version",
						mod.Name, provider.location(), identity.Key(), other.Version, identity.Version)
				}
			}
			entry = append(entry, identity)
			seen[identity.String()] = provider.location()
			providers = append(providers, provider)
		}
		return nil
	}
	for _, ie := range mod.Interface.Endpoints {
		if err := add(ie.Implements, ie.Service, ie.Endpoint); err != nil {
			return nil, err
		}
	}
	for _, capability := range mod.Interface.Capabilities {
		if err := add(capability.Implements, capability.Service, ""); err != nil {
			return nil, err
		}
	}
	return providers, nil
}

// ValidateInterfaceConformance checks every interface the module implements
// against its published definition: an endpoint implements an interface of
// its own API, and a capability entry implements a capability interface.
// Definitions come from the resolver attached to ctx.
func (mod *Module) ValidateInterfaceConformance(ctx context.Context) error {
	w := wool.Get(ctx).In("Module::ValidateInterfaceConformance", wool.ThisField(mod))
	providers, err := mod.InterfaceProviders()
	if err != nil {
		return w.Wrap(err)
	}
	for _, provider := range providers {
		if err := mod.validateImplementation(ctx, provider); err != nil {
			return w.Wrap(err)
		}
	}
	return nil
}

// validateImplementation checks one implementation this module declares
// against the published definition: an endpoint implements an interface of
// its own API, and a capability entry implements a capability interface.
func (mod *Module) validateImplementation(ctx context.Context, provider *InterfaceProvider) error {
	definition, err := ResolveInterface(ctx, provider.Identity)
	if err != nil {
		return err
	}
	if provider.Endpoint == "" {
		if definition.Type != InterfaceTypeCapability {
			return fmt.Errorf("%s declares capability %s, which is a %s interface; an endpoint must implement it", provider.location(), provider.Identity, definition.Type)
		}
		return nil
	}
	if !definition.Type.ImplementedByEndpoint() {
		return fmt.Errorf("%s implements %s, which is a capability; declare it under capabilities", provider.location(), provider.Identity)
	}
	service, err := mod.loadDeclaredService(ctx, provider.Service)
	if err != nil {
		return err
	}
	for _, endpoint := range service.Endpoints {
		if endpoint.Name == provider.Endpoint && endpoint.API != string(definition.Type) {
			return fmt.Errorf("%s serves %s but implements %s, a %s interface", provider.location(), endpoint.API, provider.Identity, definition.Type)
		}
	}
	return nil
}

// ValidateProvidedConfiguration checks the configuration a service of this
// module emits for its consumers against every capability interface the module
// declares that service provides. Definitions come from the resolver attached
// to ctx.
func (mod *Module) ValidateProvidedConfiguration(ctx context.Context, service string, configuration *basev0.Configuration) error {
	providers, err := mod.InterfaceProviders()
	if err != nil {
		return err
	}
	for _, provider := range providers {
		if provider.Service != service || provider.Endpoint != "" {
			continue
		}
		definition, err := ResolveInterface(ctx, provider.Identity)
		if err != nil {
			return err
		}
		if err := definition.ValidateConfiguration(configuration); err != nil {
			return fmt.Errorf("%s: %w", provider.location(), err)
		}
	}
	return nil
}

// HasInterface returns true if the module declares endpoint exports, which
// makes its interface the module's export boundary. Capability entries do not:
// they state what a service provides, not which endpoints cross module lines.
func (mod *Module) HasInterface() bool {
	return mod.Interface != nil && len(mod.Interface.Endpoints) > 0
}

// ValidateEndpointVisibility reports whether a service in consumerModule may
// depend on the producer endpoint declared. The declaration is judged first
// (ValidateEndpointDeclaration — a visibility, location or exposure the model
// does not define, an authored allow-list, or a contradiction between the axes
// is ErrInvalidEndpointDeclaration before any consumer is considered, the
// owning module included); then a private endpoint remains inside its owning
// module, and an internal or public one permits the dependency — internal
// stops at the workspace boundary, and every module judged here is inside it.
// This is the consuming-side counterpart to ValidateInterface, which guards
// the producing side.
func ValidateEndpointVisibility(consumerModule, producerModule string, declaration EndpointDeclaration) error {
	if err := ValidateEndpointDeclaration(declaration); err != nil {
		return err
	}
	if consumerModule == producerModule {
		return nil
	}
	producer := &Endpoint{Module: producerModule, Visibility: declaration.Visibility}
	if producer.AllowsModule(consumerModule) {
		return nil
	}
	// ValidateEndpointDeclaration and AllowsModule leave exactly private (or
	// unset) here.
	return fmt.Errorf("endpoint %s/%s is private to module %q; module %q may not depend on it",
		declaration.Service, declaration.Name, producerModule, consumerModule)
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
	if err := validateApplicationReferencePath(ref); err != nil {
		return err
	}
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
	if err := validateApplicationReferencePath(ref); err != nil {
		return nil, wool.Get(ctx).In("Module.LoadApplicationFromReference").Wrap(err)
	}
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
	if err := validateResourcePathComponent("application", name); err != nil {
		return nil, w.Wrap(err)
	}
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
	if err := validateResourcePathComponent("application", name); err != nil {
		return w.Wrap(err)
	}
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
func (mod *Module) NewApplication(ctx context.Context, action *actionsv0.AddApplication) (createdApplication *Application, result error) {
	if action == nil {
		return nil, wool.Get(ctx).In("mod.NewApplication").NewError("application action is nil")
	}
	w := wool.Get(ctx).In("mod.NewApplication", wool.NameField(action.Name))
	if err := validateResourcePathComponent("application", action.Name); err != nil {
		return nil, w.Wrap(err)
	}
	hadReference := mod.ExistsApplication(ctx, action.Name)
	if hadReference {
		// Check for override
		override := shared.GetOverride(ctx)
		if !override.Replace(action.Name) {
			return nil, w.NewError("application already exists")
		}
	}
	agent, err := LoadAgent(ctx, action.Agent, ApplicationAgent)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent")
	}

	app := &Application{
		Kind:        ApplicationKind,
		Name:        action.Name,
		Description: action.Description,
		Version:     InitialVersion,
		Agent:       agent,
		Spec:        make(map[string]any),
	}

	dir := path.Join(mod.ApplicationsDir(), action.Name)
	app.dir = dir

	originalReferences := append([]*ApplicationReference(nil), mod.ApplicationReferences...)
	createdDir := false
	defer func() {
		if result == nil {
			return
		}
		mod.ApplicationReferences = originalReferences
		if createdDir {
			if removeErr := os.RemoveAll(dir); removeErr != nil {
				result = errors.Join(result, w.Wrapf(removeErr, "cannot remove partial application directory"))
			}
		}
	}()

	createdDir, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !createdDir && !hadReference {
		return nil, w.NewError("application directory %s already exists without a module reference", dir)
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
