package configurations

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/core/templates"

	"github.com/codefly-dev/core/configurations/standards"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
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
	Application string `yaml:"application"`
	Project     string `yaml:"project,omitempty"`

	PathOverride *string `yaml:"path,omitempty"`

	Agent *Agent `yaml:"agent"`

	// ServiceDependencies are the other services required
	ServiceDependencies []*ServiceDependency `yaml:"service-dependencies"`

	// ProjectDependencies
	ProjectConfigurationDependencies []string `yaml:"project-configuration-dependencies"`

	// Endpoints exposed by the service
	Endpoints []*Endpoint `yaml:"endpoints"`

	// Spec is the specialized configuration of the service
	Spec map[string]any `yaml:"spec"`

	// RuntimeSpec are additional specs passed on by the runtime
	RuntimeSpec map[string]any `yaml:"-"`

	// internal
	dir string
}

func (s *Service) Validate() error {
	return Validate(s.Proto())
}

func (s *Service) Proto() *basev0.Service {
	return &basev0.Service{
		Name:        s.Name,
		Description: s.Description,
		Application: s.Application,
		Project:     s.Project,
	}
}

// Unique identifies a service within a project
// We use a REST like convention rather then a subdomain one
func (s *Service) Unique() string {
	if wool.IsDebug() && s.Application == "" {
		panic(fmt.Sprintf("application is empty in unique %s", s.Name))
	}
	return ServiceUnique(s.Application, s.Name)
}

func ServiceUnique(app string, service string) string {
	return fmt.Sprintf("%s/%s", app, service)
}

// Identity is the proto version of Unique
func (s *Service) Identity() *ServiceIdentity {
	return &ServiceIdentity{
		Name:        s.Name,
		Application: s.Application,
		Project:     s.Project,
	}
}

type ServiceWithApplication struct {
	Name        string
	Application string
}

func ParseService(input string) (*ServiceWithApplication, error) {
	parts := strings.Split(input, "/")
	switch len(parts) {
	case 1:
		return &ServiceWithApplication{Name: parts[0]}, nil
	case 2:
		return &ServiceWithApplication{Name: parts[1], Application: parts[0]}, nil
	default:
		return nil, fmt.Errorf("invalid service input: %s", input)
	}
}

func (s ServiceWithApplication) Unique() string {
	return fmt.Sprintf("%s/%s", s.Application, s.Name)
}

// ParseServiceUnique returns a Service/Application pair from a unique or an error
func ParseServiceUnique(unique string) (*ServiceWithApplication, error) {
	parts := strings.Split(unique, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid unique: %s", unique)
	}
	return &ServiceWithApplication{
		Name:        parts[1],
		Application: parts[0],
	}, nil
}

// NewService creates a service in an application
func (app *Application) NewService(ctx context.Context, action *actionsv0.AddService) (*Service, error) {
	w := wool.Get(ctx).In("app.NewService", wool.NameField(action.Name))
	if app.ExistsService(ctx, action.Name) && !action.Override {
		return nil, w.NewError("service already exists")
	}
	agent, err := LoadAgent(ctx, action.Agent)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent")
	}

	service := &Service{
		Name:        action.Name,
		Version:     "0.0.0",
		Application: app.Name,
		Project:     app.Project,
		Agent:       agent,
		Spec:        make(map[string]any),
	}

	dir := path.Join(app.Dir(), "services", action.Name)
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

	err = app.AddServiceReference(ctx, service.Reference())
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return service, nil
}

// ServiceReference is a reference to a service used by Application configuration
type ServiceReference struct {
	Name         string  `yaml:"name"`
	PathOverride *string `yaml:"path,omitempty"`
	Application  string  `yaml:"application,omitempty"`
}

func (ref *ServiceReference) String() string {
	if wool.IsDebug() && ref.Application == "" {
		panic(fmt.Sprintf("application is empty in reference %s", ref.Name))
	}
	return fmt.Sprintf("%s/%s", ref.Application, ref.Name)
}

func ParseServiceReference(input string) (*ServiceReference, error) {
	parts := strings.Split(input, "/")
	switch len(parts) {
	case 1:
		return &ServiceReference{Name: parts[0]}, nil
	case 2:
		return &ServiceReference{Name: parts[1], Application: parts[0]}, nil
	default:
		return nil, fmt.Errorf("invalid service input: %s", input)
	}
}

// ServiceIdentity defines exactly the scope of the service
// Name: the name of the service
// It will be unique within an application
// Application: the name of the application the service belongs to
// Recall that application names are unique within a project
// Project the service belongs to
type ServiceIdentity struct {
	Name        string
	Application string
	Project     string
}

func (s *ServiceIdentity) Unique() string {
	return fmt.Sprintf("%s/%s", s.Application, s.Name)
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

func (s *ServiceIdentity) Clone() *ServiceIdentity {
	return &ServiceIdentity{
		Name:        s.Name,
		Application: s.Application,
		Project:     s.Project,
	}
}

func ServiceIdentityFromProto(proto *basev0.ServiceIdentity) *ServiceIdentity {
	return &ServiceIdentity{
		Name:        proto.Name,
		Application: proto.Application,
		Project:     proto.Project,
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
	if err != nil {
		return nil, err
	}
	err = service.postLoad(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = service.Validate()
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
func (s *Service) AddDependency(ctx context.Context, requirement *Service, requiredEndpoints []*Endpoint) error {
	w := wool.Get(ctx).In("Service::AddDependency", wool.NameField(s.Name))
	dep, ok := s.ExistsDependency(requirement)
	if !ok {
		dep = &ServiceDependency{
			Name:        requirement.Name,
			Application: requirement.Application,
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
	for _, ref := range s.ServiceDependencies {
		if ref.Application == "" {
			ref.Application = s.Application
		}
	}
	for _, endpoint := range s.Endpoints {
		endpoint.Application = s.Application
		endpoint.Service = s.Name
		endpoint.postLoad()
	}
	return nil
}

func (s *Service) preSave(_ context.Context) error {
	// Don't include redundant information
	for _, ref := range s.ServiceDependencies {
		if ref.Application == s.Application {
			ref.Application = ""
		}
	}
	for _, endpoint := range s.Endpoints {
		endpoint.Application = ""
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

func (s *Service) ExistsDependency(requirement *Service) (*ServiceDependency, bool) {
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
		if dep.Name != ref.Name && dep.Application != ref.Application {
			deps = append(deps, dep)
		}
	}
	s.ServiceDependencies = deps
	return s.Save(ctx)
}

func (s *Service) ProviderDirectory(ctx context.Context, env *Environment) (string, error) {
	w := wool.Get(ctx).In("Service::ProviderDirectory", wool.NameField(s.Name))
	dir := path.Join(s.Dir(), "providers", env.Name)
	exists, err := shared.CheckDirectory(ctx, dir)
	if err != nil {
		return "", w.Wrapf(err, "cannot check directory")
	}
	if !exists {
		return "", nil
	}
	return dir, nil
}

func (s *Service) Global() string {
	return fmt.Sprintf("%s/%s", s.Project, s.Unique())
}

func (s *Service) BaseEndpoint(name string) *Endpoint {
	return &Endpoint{Name: name, Application: s.Application, Service: s.Name, Visibility: VisibilityPrivate}
}

func (s *Service) LoadEndpoints(ctx context.Context) ([]*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("Service::LoadEndpoints", wool.NameField(s.Name))
	var multi error
	var out []*basev0.Endpoint
	for _, ed := range s.Endpoints {
		base, err := ed.Proto()
		if err != nil {
			multi = multierror.Append(multi, err)
			continue
		}
		switch ed.API {
		case standards.REST:
			w.Debug("loading rest endpoint", wool.PathField(standards.OpenAPIPath))
			rest, err := LoadRestAPI(ctx, s.Local(standards.OpenAPIPath))
			if err != nil {
				multi = multierror.Append(multi, err)
				continue
			}
			base.ApiDetails = ToRestAPI(rest)
			out = append(out, base)
		case standards.GRPC:
			w.Debug("loading grpc endpoint", wool.PathField(standards.ProtoPath))
			grpc, err := LoadGrpcAPI(ctx, s.Local(standards.ProtoPath))
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
	return out, multi
}

func (s *Service) Local(f string) string {
	return path.Join(s.Dir(), f)
}

func (s *ServiceDependency) AsReference() *ServiceReference {
	return &ServiceReference{
		Name:        s.Name,
		Application: s.Application,
	}
}

func (s *ServiceDependency) Unique() string {
	return fmt.Sprintf("%s/%s", s.Application, s.Name)
}

type ServiceDependency struct {
	Name        string `yaml:"name"`
	Application string `yaml:"application,omitempty"`

	Endpoints []*EndpointReference `yaml:"endpoints,omitempty"`
}

func (s *ServiceDependency) String() string {
	return fmt.Sprintf("ServiceDependency<%s/%s>", s.Application, s.Name)
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
