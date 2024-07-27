package resources

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/core/templates"

	"github.com/codefly-dev/core/standards"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/codefly-dev/core/shared"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

const ServiceConfigurationName = "service.codefly.yaml"

const ServiceAgent = AgentKind("codefly:service")

func init() {
	RegisterAgent(ServiceAgent, basev0.Agent_SERVICE)
}

const RuntimeServiceAgent = "codefly:service:runtime"
const BuilderServiceAgent = "codefly:service:builder"

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

	// Dependencies
	WorkspaceConfigurationDependencies []string `yaml:"workspace-configuration-dependencies,omitempty"`

	// Endpoints exposed by the service
	Endpoints []*Endpoint `yaml:"endpoints,omitempty"`

	// Spec is the specialized configuration of the service
	Spec map[string]any `yaml:"spec,omitempty"`

	// internal
	dir    string
	module string
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
func (mod *Module) NewService(ctx context.Context, action *actionsv0.AddService) (*Service, error) {
	w := wool.Get(ctx).In("mod.NewService", wool.NameField(action.Name))
	if mod.ExistsService(ctx, action.Name) {
		// Check for override
		override := shared.GetOverride(ctx)
		if !override.Replace(action.Name) {
			return nil, w.NewError("service already exists")
		}
	}
	agent, err := LoadAgent(ctx, action.Agent)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent")
	}

	service := &Service{
		Name:    action.Name,
		Version: "0.0.0",
		Agent:   agent,
		Spec:    make(map[string]any),
	}

	dir := path.Join(mod.Dir(), "services", action.Name)
	service.dir = dir

	_, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
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

	err = mod.AddServiceReference(ctx, service.Reference())
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
	r := resource.NewSchemaless()
	return &wool.Resource{
		Identifier: &wool.Identifier{
			Kind:   "service",
			Unique: s.Unique(),
		},
		Resource: r}
}

func (s *ServiceIdentity) AsAgentResource() *wool.Resource {
	r := resource.NewSchemaless()
	return &wool.Resource{
		Identifier: &wool.Identifier{
			Kind:   "agent",
			Unique: s.Unique(),
		},
		Resource: r}
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
	w := wool.Get(ctx).In("LoadServiceFromDir", wool.DirField(dir))
	service, err := LoadFromDir[Service](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	service.dir = dir
	err = service.postLoad(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	_, err = service.Proto(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return service, nil
}

// LoadServiceFromPath loads an service from a path
func LoadServiceFromPath(ctx context.Context) (*Service, error) {
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
	err := s.preSave(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot pre-save")
	}
	return SaveToDir(ctx, s, s.Dir())
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

func (s *Service) postLoad(_ context.Context) error {
	for _, dep := range s.ServiceDependencies {
		if dep.Module == "" && s.module != "" {
			dep.Module = s.module
		}
	}
	for _, endpoint := range s.Endpoints {
		endpoint.Service = s.Name
		endpoint.postLoad()
	}
	return nil
}

func (s *Service) preSave(_ context.Context) error {
	for _, dep := range s.ServiceDependencies {
		if dep.Module == s.module {
			dep.Module = ""
		}
	}
	for _, endpoint := range s.Endpoints {
		endpoint.Module = ""
		endpoint.Service = ""
		endpoint.preSave()
	}
	return nil
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
		if dep.Name == requirement.Name {
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
			w.Debug("loading gRPC endpoint", wool.Path(standards.ProtoPath))
			grpc, err := LoadGrpcAPI(ctx, s.LocalOrNil(ctx, standards.ProtoPath))
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

type ServiceDependency struct {
	Name   string `yaml:"name"`
	Module string `yaml:"module,omitempty"`

	Endpoints []*EndpointReference `yaml:"endpoints,omitempty"`
}

func (s *ServiceDependency) String() string {
	return fmt.Sprintf("ServiceDependency<%s/%s>", s.Module, s.Name)
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
