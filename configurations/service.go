package configurations

import (
	"context"
	"fmt"
	"strings"

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
	Domain      string `yaml:"domain"`
	Namespace   string `yaml:"namespace"`

	PathOverride *string `yaml:"path,omitempty"`

	Agent *Agent `yaml:"agent"`

	// ServiceDependencies are the other services required
	ServiceDependencies []*ServiceDependency `yaml:"service-dependencies"`

	// ProviderDependencies are the providers required
	ProviderDependencies []string `yaml:"provider-dependencies"`

	// Endpoints exposed by the service
	Endpoints []*Endpoint `yaml:"endpoints"`

	// Spec is the specialized configuration of the service
	Spec map[string]any `yaml:"spec"`

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
		Domain:      s.Domain,
		Namespace:   s.Namespace,
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
		Domain:      app.ServiceDomain(action.Name),
		Namespace:   shared.DefaultTo(action.Namespace, app.Name),
		Agent:       agent,
		Spec:        make(map[string]any),
	}

	ref := &ServiceReference{Name: action.Name, PathOverride: OverridePath(action.Name, action.Path), Application: app.Name}
	dir := app.ServicePath(ctx, ref)

	service.dir = dir

	_, err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = service.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = app.AddService(ctx, service)
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
// This is a logical partitioning
// Namespace: the namespace the service belongs to
// This is a resource partitioning
// Domain: the domain of the service belongs to
// This is a responsibility partitioning
type ServiceIdentity struct {
	Name        string
	Application string
	Namespace   string
	Domain      string
}

func Identity(conf *Service) *ServiceIdentity {
	return &ServiceIdentity{
		Name:        conf.Name,
		Application: conf.Application,
		Namespace:   conf.Namespace,
		Domain:      conf.Domain,
	}
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

func ServiceIdentityFromProto(proto *basev0.ServiceIdentity) *ServiceIdentity {
	return &ServiceIdentity{
		Name:        proto.Name,
		Application: proto.Application,
		Namespace:   proto.Namespace,
		Domain:      proto.Domain,
	}
}

func (s *Service) Reference() (*ServiceReference, error) {
	entry := &ServiceReference{
		Name:         s.Name,
		PathOverride: s.PathOverride,
	}
	return entry, nil
}

func (s *Service) Endpoint() string {
	return fmt.Sprintf("%s.%s", s.Name, s.Namespace)
}

func (s *Service) Dir() string {
	return s.dir
}

// LoadServiceFromDirUnsafe loads a service from a directory
func LoadServiceFromDirUnsafe(ctx context.Context, dir string) (*Service, error) {
	w := wool.Get(ctx).In("LoadServiceFromDirUnsafe", wool.DirField(dir))
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
	return LoadServiceFromDirUnsafe(ctx, *dir)
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
	return LoadServiceFromDirUnsafe(ctx, service.Dir())
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
	}
	return nil
}

func (s *Service) preSave(_ context.Context) error {
	for _, ref := range s.ServiceDependencies {
		if ref.Application == s.Application {
			ref.Application = ""
		}
	}
	for _, endpoint := range s.Endpoints {
		endpoint.Application = ""
		endpoint.Service = ""
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
			return nil, fmt.Errorf("unknown endpoint: %s", endpoint)
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
			return fmt.Errorf("endpoint already exists: %s", endpoint.Name)
		}
		w.Debug("adding endpoint %s", wool.NameField(endpoint.Name))
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
		if err := standards.SupportedAPI(api); err != nil {
			return err
		}
	}
	return nil
}
