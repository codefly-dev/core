package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/core/templates"

	"github.com/codefly-dev/core/standards"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

const ServiceConfigurationName = "service.codefly.yaml"

const RuntimeServiceAgent = "codefly:service:runtime"
const BuilderServiceAgent = "codefly:service:builder"
const CodeServiceAgent = "codefly:service:code"

/*
A Service
*/
type Service struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Version     string `yaml:"version"`

	PathOverride *string `yaml:"path,omitempty"`

	Agent *Agent `yaml:"agent"`

	// ServiceDependencies are the other services required
	ServiceDependencies []*ServiceDependency `yaml:"service-dependencies,omitempty"`

	// LibraryDependencies are the internal libraries required
	LibraryDependencies []*LibraryDependency `yaml:"library-dependencies,omitempty"`

	// Dependencies
	WorkspaceConfigurationDependencies []string `yaml:"workspace-configuration-dependencies,omitempty"`

	// Endpoints exposed by the service
	Endpoints []*Endpoint `yaml:"endpoints,omitempty"`

	// Spec is the specialized configuration of the service
	Spec map[string]any `yaml:"spec,omitempty"`

	// Autoscale, when set, renders a HorizontalPodAutoscaler next to this
	// service's Deployment so replicas scale under load instead of relying on
	// node autoscale alone. CLI-side; not serialized to proto.
	Autoscale *ServiceAutoscale `yaml:"autoscale,omitempty"`

	// Test is the LANGUAGE-AGNOSTIC test formula for this service: the command
	// to run plus provisioning, all data, set ONCE here so callers need not
	// pass a formula on every Test RPC. A per-call runtime TestRequest.formula
	// overrides it. Read by every language agent (the agent translates the
	// generic provisioning map into its own toolchain). No framework/toolchain
	// name lives in this config.
	Test *TestFormula `yaml:"test,omitempty"`

	// ExtraFields captures top-level manifest keys that are valid on disk but not
	// modeled as struct fields here (e.g. secret-service-configurations). Without
	// this catch-all, any load → mutate → Save round-trip silently drops them.
	// The inline tag makes yaml store unmatched top-level keys here on load and
	// re-emit them on save, so callers that mutate a modeled field don't erase
	// the rest. Redundant legacy identity keys (kind/module/project) are stripped
	// on save by preSave — they are implied by the file's location, not data.
	ExtraFields map[string]any `yaml:",inline"`

	// internal
	dir    string
	module string
}

// TestFormula is the language-agnostic test invocation as DATA — the yaml mirror
// of runtimev0.TestFormula. The command is captured from the project; the
// provisioning map is interpreted by the language plugin (python/uv reads
// python/editable/with/requirements/no_project). Nothing here is framework- or
// toolchain-specific.
type TestFormula struct {
	Command      []string          `yaml:"command,omitempty"`
	Output       string            `yaml:"output,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	Provisioning map[string]string `yaml:"provisioning,omitempty"`
}

// ServiceAutoscale renders a HorizontalPodAutoscaler that scales the service's
// Deployment between Min and Max replicas to hold average CPU utilization at
// TargetCPU percent of each pod's requested CPU.
type ServiceAutoscale struct {
	Min       int32 `yaml:"min"`
	Max       int32 `yaml:"max"`
	TargetCPU int32 `yaml:"target-cpu"`
}

// Validate reports whether a declared autoscale block would render a usable
// HorizontalPodAutoscaler. A nil receiver is a valid "not declared" state.
func (a *ServiceAutoscale) Validate() error {
	if a == nil {
		return nil
	}
	if a.Min < 1 {
		return fmt.Errorf("autoscale.min must be at least 1")
	}
	if a.Max < a.Min {
		return fmt.Errorf("autoscale.max (%d) must be greater than or equal to autoscale.min (%d)", a.Max, a.Min)
	}
	if a.TargetCPU < 1 || a.TargetCPU > 100 {
		return fmt.Errorf("autoscale.target-cpu must be between 1 and 100")
	}
	return nil
}

func (s *Service) Proto(_ context.Context) (*basev0.Service, error) {
	proto := &basev0.Service{
		Name:        s.Name,
		Description: s.Description,
	}
	if err := Validate(proto); err != nil {
		return nil, err
	}
	return proto, nil
}

func ServiceUnique(module string, service string) string {
	return fmt.Sprintf("%s/%s", module, service)
}

// Identity is the proto version of Unique
func (s *Service) Identity() (*ServiceIdentity, error) {
	if s.module == "" {
		return nil, fmt.Errorf("module not set")
	}
	return &ServiceIdentity{
		Name:    s.Name,
		Module:  s.module,
		Version: s.Version,
	}, nil
}

type ServiceWithModule struct {
	Name   string
	Module string
}

func ParseServiceWithOptionalModule(input string) (*ServiceWithModule, error) {
	parts := strings.Split(input, "/")
	switch len(parts) {
	case 1:
		return &ServiceWithModule{Name: parts[0]}, nil
	case 2:
		return &ServiceWithModule{Name: parts[1], Module: parts[0]}, nil
	default:
		return nil, fmt.Errorf("invalid service input: %s", input)
	}
}

func (s ServiceWithModule) Unique() string {
	return fmt.Sprintf("%s/%s", s.Module, s.Name)
}

func (s ServiceWithModule) String() string {
	return s.Unique()
}

// NewService creates a service in an module
func (mod *Module) NewService(ctx context.Context, action *actionsv0.AddService) (createdService *Service, result error) {
	if action == nil {
		return nil, wool.Get(ctx).In("mod.NewService").NewError("service action is nil")
	}
	w := wool.Get(ctx).In("mod.NewService", wool.NameField(action.Name))
	if err := validateResourcePathComponent("service", action.Name); err != nil {
		return nil, w.Wrap(err)
	}
	hadReference := mod.ExistsService(ctx, action.Name)
	if hadReference {
		// Check for override
		override := shared.GetOverride(ctx)
		if !override.Replace(action.Name) {
			return nil, w.NewError("service already exists")
		}
	}
	agent, err := LoadAgent(ctx, action.Agent, ServiceAgent)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent")
	}

	service := &Service{
		Name:    action.Name,
		Version: "0.0.0",
		Agent:   agent,
		Spec:    make(map[string]any),
	}

	ref := &ServiceReference{Name: action.Name}
	if hadReference {
		existing, err := mod.GetServiceReferences(action.Name)
		if err != nil {
			return nil, w.Wrap(err)
		}
		if existing == nil {
			return nil, w.NewError("service reference <%s> disappeared", action.Name)
		}
		ref = existing
		service.PathOverride = existing.PathOverride
	}
	dir := mod.ServicePath(ctx, ref)
	service.dir = dir

	originalReferences := append([]*ServiceReference(nil), mod.ServiceReferences...)
	createdDir := false
	defer func() {
		if result == nil {
			return
		}
		mod.ServiceReferences = originalReferences
		if createdDir {
			if err := os.RemoveAll(dir); err != nil {
				result = errors.Join(result, w.Wrapf(err, "cannot remove partial service directory"))
			}
		}
	}()

	createdDir, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if !createdDir && !hadReference {
		return nil, w.NewError("service directory %s already exists without a module reference", dir)
	}
	err = service.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	// Templatize as usual
	err = templates.CopyAndApply(ctx, shared.Embed(fs), "templates/service", service.dir, service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot copy and apply template")
	}

	err = mod.AddServiceReference(ctx, ref)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = mod.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return service, nil
}

// ServiceReference is a reference to a service used by Module configuration
type ServiceReference struct {
	Name         string  `yaml:"name"`
	Module       string  `yaml:"module,omitempty"`
	PathOverride *string `yaml:"path,omitempty"`
}

func (ref *ServiceReference) String() string {
	if wool.IsDebug() && ref.Module == "" {
		panic(fmt.Sprintf("module is empty in reference %s", ref.Name))
	}
	return fmt.Sprintf("%s/%s", ref.Module, ref.Name)
}

func ParseServiceReference(input string) (*ServiceReference, error) {
	parts := strings.Split(input, "/")
	switch len(parts) {
	case 1:
		return &ServiceReference{Name: parts[0]}, nil
	case 2:
		return &ServiceReference{Name: parts[1], Module: parts[0]}, nil
	default:
		return nil, fmt.Errorf("invalid service input: %s", input)
	}
}

// ServiceIdentity defines exactly the scope of the service
// Name: the name of the service
// It will be unique within an module
// Module: the name of the module the service belongs to
// Recall that module names are unique within a workspace
// Workspace: the name of the workspace the service belongs to
type ServiceIdentity struct {
	Name                string
	Version             string
	Module              string
	Workspace           string
	WorkspacePath       string
	RelativeToWorkspace string
}

func (s *ServiceIdentity) Unique() string {
	return fmt.Sprintf("%s/%s", s.Module, s.Name)
}

func (s *ServiceIdentity) AsResource() *wool.Resource {
	return &wool.Resource{
		Kind:   "service",
		Unique: s.Unique(),
	}
}

func (s *ServiceIdentity) AsAgentResource() *wool.Resource {
	return &wool.Resource{
		Kind:   "agent",
		Unique: s.Unique(),
	}
}

func (s *ServiceIdentity) Clone() *ServiceIdentity {
	return &ServiceIdentity{
		Name:                s.Name,
		Module:              s.Module,
		Workspace:           s.Workspace,
		WorkspacePath:       s.WorkspacePath,
		RelativeToWorkspace: s.RelativeToWorkspace,
		Version:             s.Version,
	}
}

func ServiceIdentityFromProto(proto *basev0.ServiceIdentity) *ServiceIdentity {
	return &ServiceIdentity{
		Name:                proto.Name,
		Module:              proto.Module,
		Workspace:           proto.Workspace,
		WorkspacePath:       proto.WorkspacePath,
		RelativeToWorkspace: proto.RelativeToWorkspace,
		Version:             proto.Version,
	}
}

func (s *ServiceIdentity) Proto() (*basev0.ServiceIdentity, error) {
	proto := &basev0.ServiceIdentity{
		Name:                s.Name,
		Module:              s.Module,
		Workspace:           s.Workspace,
		WorkspacePath:       s.WorkspacePath,
		RelativeToWorkspace: s.RelativeToWorkspace,
		Version:             s.Version,
	}
	err := Validate(proto)
	if err != nil {
		return nil, err
	}
	return proto, nil
}

func (s *Service) Reference() *ServiceReference {
	entry := &ServiceReference{
		Name:         s.Name,
		PathOverride: s.PathOverride,
	}
	return entry
}

func (s *Service) Dir() string {
	return s.dir
}

func (s *Service) WithDir(dir string) {
	s.dir = dir
}

// LoadServiceFromDir loads a service from a directory
func LoadServiceFromDir(ctx context.Context, dir string) (*Service, error) {
	return loadServiceFromDir(ctx, dir, "")
}

// loadServiceFromDir loads a service and runs postLoad EXACTLY ONCE with the
// module already set. Callers that know the owning module (module-based loads)
// pass it here rather than loading module-less and re-running postLoad, which
// would repeat every postLoad side effect — including the deprecated-visibility
// warnings — for the same service.
func loadServiceFromDir(ctx context.Context, dir, module string) (*Service, error) {
	w := wool.Get(ctx).In("LoadServiceFromDir", wool.DirField(dir))
	service, err := LoadFromDir[Service](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	service.dir = dir
	service.module = module
	if err = service.postLoad(ctx); err != nil {
		return nil, w.Wrap(err)
	}
	if _, err = service.Proto(ctx); err != nil {
		return nil, w.Wrap(err)
	}
	return service, nil
}

// LoadServiceFromCurrentPath loads an service from a path
func LoadServiceFromCurrentPath(ctx context.Context) (*Service, error) {
	dir, err := FindUp[Service](ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, nil
	}
	return LoadServiceFromDir(ctx, *dir)
}

func (s *Service) SaveAtDir(ctx context.Context, dir string) error {
	s.dir = dir
	return s.Save(ctx)
}

func (s *Service) Save(ctx context.Context) error {
	w := wool.Get(ctx).In("Service::Save", wool.NameField(s.Name))
	if err := s.validatePaths(); err != nil {
		return w.Wrap(err)
	}
	if err := validateEndpointNames(s.Endpoints); err != nil {
		return w.Wrap(err)
	}
	// preSave blanks fields that are redundant on disk (module/service are
	// implied by location). It returns a restore func so the in-memory model
	// is put back AFTER marshalling — otherwise Save corrupts live objects
	// (and, in flat layout, pointers shared with workspace.Services).
	restore := s.preSave()
	defer restore()
	if err := SaveToDir(ctx, s, s.Dir()); err != nil {
		return w.Wrapf(err, "cannot save service")
	}
	return nil
}

func (s *Service) UpdateSpecFromSettings(spec any) error {
	if s.Spec == nil {
		s.Spec = make(map[string]any)
	}

	config := &mapstructure.DecoderConfig{
		Metadata: nil,
		Result:   &s.Spec,
		TagName:  "yaml",
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return fmt.Errorf("cannot create decoder: %w", err)
	}

	if err := decoder.Decode(spec); err != nil {
		return fmt.Errorf("cannot decode service spec: %w", err)
	}
	return nil
}

func (s *Service) LoadSettingsFromSpec(t any) error {
	// write down the spec to []byte
	content, err := yaml.Marshal(s.Spec)
	if err != nil {
		return fmt.Errorf("cannot marshal service spec: %w", err)
	}
	// decode the spec into the target
	err = yaml.Unmarshal(content, t)
	if err != nil {
		return fmt.Errorf("cannot unmarshal service spec: %w", err)
	}
	return nil
}

// AddDependency adds a dependency to the service
func (s *Service) AddDependency(ctx context.Context, requirement *ServiceIdentity, requiredEndpoints []*Endpoint) error {
	w := wool.Get(ctx).In("Service::AddDependency", wool.NameField(s.Name))
	dep, ok := s.ExistsDependency(requirement)
	if !ok {
		dep = &ServiceDependency{
			Name:   requirement.Name,
			Module: requirement.Module,
		}
		s.ServiceDependencies = append(s.ServiceDependencies, dep)
	}
	err := dep.UpdateEndpoints(ctx, requiredEndpoints)
	if err != nil {
		return w.Wrapf(err, "cannot update endpoints")
	}
	return nil
}

// ReloadService from directory
func ReloadService(ctx context.Context, service *Service) (*Service, error) {
	return LoadServiceFromDir(ctx, service.Dir())
}

func (s *Service) postLoad(ctx context.Context) error {
	w := wool.Get(ctx).In("Service::postLoad", wool.NameField(s.Name), wool.ModuleField(s.module))
	if err := s.validatePaths(); err != nil {
		return w.Wrap(err)
	}
	if err := validateEndpointNames(s.Endpoints); err != nil {
		return w.Wrap(err)
	}
	if err := s.Autoscale.Validate(); err != nil {
		return w.Wrap(err)
	}
	for _, dep := range s.ServiceDependencies {
		if dep.Module == "" && s.module != "" {
			w.Trace("setting module for dependency", wool.NameField(dep.Name))
			dep.Module = s.module
		}
	}
	for _, endpoint := range s.Endpoints {
		endpoint.Service = s.Name
		endpoint.Module = s.module
		endpoint.postLoad(ctx)
	}
	return nil
}

// preSave blanks fields that should not be written to disk (they are redundant
// — implied by the file's location) and returns a func that restores every
// mutated value. Callers MUST defer the returned restore so the in-memory model
// is not left corrupted after marshalling.
func (s *Service) preSave() func() {
	type epSnap struct {
		ep              *Endpoint
		module, svc     string
		visibility, api string
	}
	type depSnap struct {
		dep    *ServiceDependency
		module string
	}
	var eps []epSnap
	var deps []depSnap

	// Redundant legacy identity keys are implied by the file's location and were
	// never written back before ExtraFields existed. Strip them from the inline
	// catch-all so a rewrite normalizes them away instead of relocating them to
	// the bottom of the file or immortalizing a value that has since drifted
	// (e.g. a stale top-level module after a rename). Genuinely-unknown keys stay.
	var strippedExtras map[string]any
	for _, k := range []string{"kind", "module", "project"} {
		if v, ok := s.ExtraFields[k]; ok {
			if strippedExtras == nil {
				strippedExtras = map[string]any{}
			}
			strippedExtras[k] = v
			delete(s.ExtraFields, k)
		}
	}

	for _, dep := range s.ServiceDependencies {
		if dep.Module == s.module {
			deps = append(deps, depSnap{dep: dep, module: dep.Module})
			dep.Module = ""
		}
	}
	for _, endpoint := range s.Endpoints {
		eps = append(eps, epSnap{ep: endpoint, module: endpoint.Module, svc: endpoint.Service, visibility: endpoint.Visibility, api: endpoint.API})
		endpoint.Module = ""
		endpoint.Service = ""
		endpoint.preSave()
	}

	return func() {
		for k, v := range strippedExtras {
			s.ExtraFields[k] = v
		}
		for _, d := range deps {
			d.dep.Module = d.module
		}
		for _, e := range eps {
			e.ep.Module = e.module
			e.ep.Service = e.svc
			e.ep.Visibility = e.visibility
			e.ep.API = e.api
		}
	}
}

func (s *Service) HasEndpoints(_ context.Context, endpoints []string) ([]string, error) {
	known := map[string]bool{}
	for _, endpoint := range s.Endpoints {
		known[endpoint.Name] = true
	}
	var unknowns []string
	for _, endpoint := range endpoints {
		if !known[endpoint] {
			unknowns = append(unknowns, endpoint)
		}
	}
	if len(unknowns) > 0 {
		return unknowns, fmt.Errorf("unknown endpoints: %v", unknowns)
	}
	return nil, nil
}

// ConsumedEndpoints returns the endpoints a dependency consumes from this
// service: the named subset, or every endpoint when names is empty (an
// unnamed dependency receives them all). It errors if a name matches no
// endpoint, so a stale reference is surfaced rather than silently dropped.
func (s *Service) ConsumedEndpoints(names []string) ([]*Endpoint, error) {
	if len(names) == 0 {
		return s.Endpoints, nil
	}
	return s.EndpointsFromNames(names)
}

// EndpointsFromNames return matching endpoints
func (s *Service) EndpointsFromNames(endpoints []string) ([]*Endpoint, error) {
	known := map[string]*Endpoint{}
	for _, endpoint := range s.Endpoints {
		known[endpoint.Name] = endpoint
	}
	var out []*Endpoint
	for _, endpoint := range endpoints {
		if known[endpoint] == nil {
			return nil, fmt.Errorf("unknown info: %s", endpoint)
		}
		out = append(out, known[endpoint])
	}
	return out, nil
}

func (s *Service) ExistsDependency(requirement *ServiceIdentity) (*ServiceDependency, bool) {
	for _, dep := range s.ServiceDependencies {
		if dep.Name == requirement.Name && dep.Module == requirement.Module {
			return dep, true
		}
	}
	return nil, false
}

func (s *Service) DeleteServiceDependencies(ctx context.Context, ref *ServiceReference) error {
	var deps []*ServiceDependency
	for _, dep := range s.ServiceDependencies {
		if dep.Name == ref.Name && dep.Module == ref.Module {
			continue
		}
		deps = append(deps, dep)
	}
	s.ServiceDependencies = deps
	return s.Save(ctx)
}

type MustServiceUnique struct {
	*Service
}

func (m *MustServiceUnique) Unique() string {
	return m.Service.MustUnique()
}

func WithUnique(s *Service) *MustServiceUnique {
	return &MustServiceUnique{s}
}

func (s *ServiceIdentity) UniqueWithWorkspace(workspace string) string {
	if workspace == s.Module {
		return s.Unique()
	}
	return fmt.Sprintf("%s-%s", workspace, s.Unique())
}

func (s *ServiceIdentity) UniqueWithWorkspaceAndScope(workspace string, scope string) string {
	return fmt.Sprintf("%s-%s", s.UniqueWithWorkspace(workspace), scope)
}

func (s *ServiceIdentity) BaseEndpoint(name string) *Endpoint {
	return &Endpoint{Name: name, Module: s.Module, Service: s.Name, Visibility: VisibilityPrivate}
}

func (s *Service) LoadEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("core.Service.LoadEndpoints", wool.NameField(s.Name))
	w.Debug("processing endpoints", wool.SliceCountField(s.Endpoints))
	if s.module == "" {
		return nil, fmt.Errorf("module not set")
	}
	if err := validateEndpointNames(s.Endpoints); err != nil {
		return nil, err
	}
	var multi error
	var out []*basev0.Endpoint
	for _, ed := range s.Endpoints {
		ed.Module = s.module
		base, err := ed.Proto()
		if err != nil {
			multi = multierror.Append(multi, err)
			continue
		}
		switch ed.API {
		case standards.REST:
			w.Debug("loading REST endpoint", wool.Path(standards.OpenAPIPath))
			rest, err := LoadRestAPI(ctx, s.LocalOrNil(ctx, standards.OpenAPIPath))
			if err != nil {
				multi = multierror.Append(multi, err)
				w.Debug("couldn't load endpoints", wool.ErrField(err))
				continue
			}
			base.ApiDetails = ToRestAPI(rest)
			out = append(out, base)
		case standards.GRPC:
			contract := s.LocalOrNil(ctx, standards.DependencyProtoPath)
			if contract == nil {
				contract = s.LocalOrNil(ctx, standards.ProtoPath)
			}
			w.Debug("loading gRPC endpoint", wool.Field("contract", contract))
			grpc, err := LoadGrpcAPI(ctx, contract)
			if err != nil {
				multi = multierror.Append(multi, err)
				continue
			}
			base.Api = standards.GRPC
			base.ApiDetails = ToGrpcAPI(grpc)
			out = append(out, base)
		case standards.HTTP:
			http, err := LoadHTTPAPI(ctx)
			if err != nil {
				multi = multierror.Append(multi, err)
			}
			base.Api = standards.HTTP
			base.ApiDetails = ToHTTPAPI(http)
			out = append(out, base)
		case standards.TCP:
			tcp, err := LoadTCPAPI(ctx)
			if err != nil {
				multi = multierror.Append(multi, err)
			}
			base.Api = standards.TCP
			base.ApiDetails = ToTCPAPI(tcp)
			out = append(out, base)
		case standards.CONNECT:
			// Connect uses HTTP/2 transport — same API shape as HTTP.
			base.Api = standards.CONNECT
			base.ApiDetails = ToHTTPAPI(&basev0.HttpAPI{})
			out = append(out, base)
		case standards.MCP:
			// MCP is served over Streamable HTTP — same API shape as HTTP.
			base.Api = standards.MCP
			base.ApiDetails = ToHTTPAPI(&basev0.HttpAPI{})
			out = append(out, base)
		}
	}
	w.Debug("loaded endpoints", wool.SliceCountField(out))
	return out, multi
}

func (s *Service) Local(_ context.Context, f string) string {
	return path.Join(s.Dir(), f)
}

func (s *Service) LocalOrNil(ctx context.Context, f string) *string {
	p := path.Join(s.Dir(), f)
	exists, err := shared.FileExists(ctx, p)
	if err == nil && exists {
		return shared.Pointer(p)
	}
	return nil
}

func (s *Service) WithModule(mod string) {
	s.module = mod
	for _, dep := range s.ServiceDependencies {
		if dep.Module == "" {
			dep.Module = s.module
		}
	}
}

func (s *Service) MustUnique() string {
	if s.module == "" {
		panic("module can no be empty")
	}
	return fmt.Sprintf("%s/%s", s.module, s.Name)
}

func (s *ServiceDependency) AsReference() *ServiceReference {
	return &ServiceReference{
		Name:   s.Name,
		Module: s.Module,
	}
}

func (s *ServiceDependency) Unique() string {
	return fmt.Sprintf("%s/%s", s.Module, s.Name)
}

// Placement is where a dependency's workload runs relative to the consumer. It
// is declared by the DEPENDENT (a property of the edge, not of the producer):
// "service" (default) runs the dependency as its own Deployment reached over
// cluster DNS; "sidecar" places the dependency's container in the consumer's own
// pod, reached over localhost.
const (
	PlacementService = "service"
	PlacementSidecar = "sidecar"
)

type ServiceDependency struct {
	Name   string `yaml:"name,omitempty"`
	Module string `yaml:"module,omitempty"`

	// GrpcClientDir, when set, overrides the consuming service's service-level
	// grpc-client-dir for THIS dependency's generated gRPC client. It lets a
	// single dependency's client land in a different crate (e.g. a plugin crate
	// that owns it) instead of the default shared external dir — so a feature can
	// hold its own external client without coupling the engine to it. Relative to
	// the consuming service's directory, like the service-level setting.
	GrpcClientDir string `yaml:"grpc-client-dir,omitempty"`

	// Placement is "service" (default) or "sidecar" — see the Placement consts.
	// "sidecar" asks composition to contribute this dependency's container into
	// the consumer's pod (via PodTemplateOverlay.Containers) and resolve its
	// endpoint to localhost, instead of deploying it separately. Empty == service.
	Placement string `yaml:"placement,omitempty"`

	Endpoints []*EndpointReference `yaml:"endpoints,omitempty"`
}

// PlacementOrDefault returns the declared placement, defaulting empty to
// "service" so callers never branch on the empty string.
func (s *ServiceDependency) PlacementOrDefault() string {
	if s.Placement == "" {
		return PlacementService
	}
	return s.Placement
}

// IsSidecar reports whether this dependency is placed in the consumer's pod.
func (s *ServiceDependency) IsSidecar() bool {
	return s.Placement == PlacementSidecar
}

func (s *ServiceDependency) String() string {
	return fmt.Sprintf("ServiceDependency<%s/%s>", s.Module, s.Name)
}

// ConsumesEndpoint reports whether this dependency pulls in the producer
// endpoint identified by name and API. A dependency that lists no endpoints
// consumes them all.
func (s *ServiceDependency) ConsumesEndpoint(name, api string) bool {
	if len(s.Endpoints) == 0 {
		return true
	}
	for _, ref := range s.Endpoints {
		if (ref.Name == "" || ref.Name == name) && (ref.API == "" || ref.API == api) {
			return true
		}
	}
	return false
}

func (s *ServiceDependency) UpdateEndpoints(ctx context.Context, endpoints []*Endpoint) error {
	w := wool.Get(ctx).In("ServiceDependency::UpdateEndpoints", wool.NameField(s.Name))
	known := map[string]*EndpointReference{}
	for _, endpoint := range s.Endpoints {
		known[endpoint.Name] = endpoint
	}
	for _, endpoint := range endpoints {
		if _, exists := known[endpoint.Name]; exists {
			return fmt.Errorf("info already exists: %s", endpoint.Name)
		}
		w.Debug("adding info %s", wool.NameField(endpoint.Name))
		s.Endpoints = append(s.Endpoints, endpoint.AsReference())
	}
	return nil
}

type ClientEntry struct {
	Name string   `yaml:"name"`
	APIs []string `yaml:"apis"`
}

func (c *ClientEntry) Validate() error {
	for _, api := range c.APIs {
		if err := standards.IsSupportedAPI(api); err != nil {
			return err
		}
	}
	return nil
}

func MakeManyServicesSummary(services []*ServiceIdentity) string {
	var out []string
	for _, service := range services {
		out = append(out, service.Unique())
	}
	return strings.Join(out, ", ")
}

func LoadModuleAndServiceFromCurrentPath(ctx context.Context) (*Module, *Service, error) {
	dir, err := FindUp[Module](ctx)
	if err != nil {
		return nil, nil, err
	}
	var mod *Module
	if dir != nil {
		mod, err = LoadModuleFromDir(ctx, *dir)
		if err != nil {
			return nil, nil, err
		}
	}

	dir, err = FindUp[Service](ctx)
	if err != nil {
		return nil, nil, err
	}
	var svc *Service
	if dir != nil {
		svc, err = LoadServiceFromDir(ctx, *dir)
		if err != nil {
			return nil, nil, err
		}
		if mod != nil {
			svc.WithModule(mod.Name)
		}
	}
	return mod, svc, nil
}
