package configurations

import (
	"context"
	"fmt"
	"slices"
	"strings"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	servicesv1 "github.com/codefly-dev/core/proto/v1/go/services"

	"github.com/codefly-dev/core/shared"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

const ServiceConfigurationName = "service.codefly.yaml"

const ServiceAgent = AgentKind("codefly:service")

func init() {
	RegisterAgent(ServiceAgent, basev1.Agent_SERVICE)
}

const RuntimeServiceAgent = "codefly:service:runtime"
const FactoryServiceAgent = "codefly:service:factory"

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

	Agent        *Agent               `yaml:"agent"`
	Dependencies []*ServiceDependency `yaml:"dependencies"`
	Endpoints    []*Endpoint          `yaml:"endpoints"`
	Spec         map[string]any       `yaml:"spec"`

	// internal
	kind string
	dir  string
}

func (s *Service) Proto() *basev1.Service {
	return &basev1.Service{
		Name:        s.Name,
		Description: s.Description,
		Application: s.Application,
	}
}

// Unique identifies a service within a project
// We use a REST like convention rather then a subdomain one
func (s *Service) Unique() string {
	if shared.IsDebug() && s.Application == "" {
		panic(fmt.Sprintf("application is empty in unique %s", s.Name))
	}
	return fmt.Sprintf("%s/%s", s.Application, s.Name)
}

// Identity is the proto version of Unique
func (s *Service) Identity() *servicesv1.ServiceIdentity {
	return &servicesv1.ServiceIdentity{
		Name:        s.Name,
		Application: s.Application,
		Domain:      s.Domain,
		Namespace:   s.Namespace,
	}
}

// NewService creates a service in an application
func (app *Application) NewService(ctx context.Context, action *v1actions.AddService) (*Service, error) {
	logger := shared.GetLogger(ctx).With("NewService<%s>", action.Name)
	if app.ExistsService(action.Name) && !action.Override {
		return nil, logger.Errorf("service already exists: %s", action.Name)
	}
	agent, err := LoadAgent(ctx, action.Agent)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load agent")
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

	ref := &ServiceReference{Name: action.Name, PathOverride: OverridePath(action.Name, action.Path)}
	dir := app.ServicePath(ctx, ref)

	service.dir = dir

	err = shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot create service directory")
	}
	err = service.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save service configuration")
	}
	err = app.AddService(ctx, service)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot add service to application")
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project configuration")
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
	if shared.IsDebug() && ref.Application == "" {
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
	logger := shared.GetLogger(ctx).With("LoadServiceFromDirUnsafe<%s>", dir)
	service, err := LoadFromDir[Service](ctx, dir)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	service.dir = dir
	if err != nil {
		return nil, err
	}
	err = service.postLoad(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot post load service")
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
	logger := shared.GetLogger(ctx).With("Save<%s>", s.Name)
	err := s.preSave(ctx)
	if err != nil {
		return logger.Wrapf(err, "cannot pre-save")
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
	logger := shared.GetLogger(ctx).With("AddDependency<%s>", requirement.Name)
	dep, ok := s.ExistsDependency(requirement)
	if !ok {
		dep = &ServiceDependency{
			Name:        requirement.Name,
			Application: requirement.Application,
		}
		s.Dependencies = append(s.Dependencies, dep)
	}
	err := dep.UpdateEndpoints(ctx, requiredEndpoints)
	if err != nil {
		return logger.Wrapf(err, "cannot update endpoints")
	}
	return nil
}

// ReloadService from directory
func ReloadService(ctx context.Context, service *Service) (*Service, error) {
	return LoadServiceFromDirUnsafe(ctx, service.Dir())
}

func (s *Service) postLoad(ctx context.Context) error {
	for _, ref := range s.Dependencies {
		if ref.Application == "" {
			ref.Application = s.Application
		}
	}
	return nil
}

func (s *Service) preSave(ctx context.Context) error {
	for _, ref := range s.Dependencies {
		if ref.Application == s.Application {
			ref.Application = ""
		}
	}
	return nil
}

func (s *Service) HasEndpoints(ctx context.Context, endpoints []string) ([]string, error) {
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
	for _, dep := range s.Dependencies {
		if dep.Name == requirement.Name {
			return dep, true
		}
	}
	return nil, false
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
	logger := shared.GetLogger(ctx).With("UpdateEndpoints<%s>", s.Name)
	known := map[string]*EndpointReference{}
	for _, endpoint := range s.Endpoints {
		known[endpoint.Name] = endpoint
	}
	for _, endpoint := range endpoints {
		if _, exists := known[endpoint.Name]; exists {
			return fmt.Errorf("endpoint already exists: %s", endpoint.Name)
		}
		logger.DebugMe("adding endpoint %s", endpoint.Name)
		s.Endpoints = append(s.Endpoints, endpoint.AsReference())
	}
	return nil
}

const (
	Unknown = "unknown"
	Grpc    = "grpc"
	Rest    = "rest"
	TCP     = "tcp"
)

var supportedAPI []string

func init() {
	supportedAPI = []string{Grpc, Rest, TCP}
}

func SupportedAPI(kind string) error {
	if slices.Contains(supportedAPI, kind) {
		return nil
	}
	return fmt.Errorf("unsupported api: %s", kind)
}

type ClientEntry struct {
	Name string   `yaml:"name"`
	APIs []string `yaml:"apis"`
}

func (c *ClientEntry) Validate() error {
	for _, api := range c.APIs {
		if err := SupportedAPI(api); err != nil {
			return err
		}
	}
	return nil
}
