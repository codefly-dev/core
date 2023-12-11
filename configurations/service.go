package configurations

import (
	"context"
	"fmt"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	"slices"
	"strings"

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

// Unique identifies a service within a project
// We use a REST like convention rather then a subdomain one
func (s *Service) Unique() string {
	if shared.IsDebug() && s.Application == "" {
		panic(fmt.Sprintf("application is empty in unique %s", s.Name))
	}
	return fmt.Sprintf("%s/%s", s.Application, s.Name)
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

// AddDependencyReference adds a dependency to the service
func (s *Service) AddDependencyReference(requirement *Service) error {
	logger := shared.NewLogger().With("configurations.Unique.AddDependencyReference<%s> <- %s", s.Name, requirement.Name)
	logger.Debugf("endpoints from the requirements: %v", requirement.Endpoints)
	for _, d := range requirement.Endpoints {
		logger.Debugf("JERE DEP: %v", d)
	}
	logger.Debugf("adding dependency <%s > to requirement <%s>", requirement.Name, s.Name)
	// s.Dependencies =
	return nil
}

// Reload from directory
func (s *Service) Reload(ctx context.Context, service *Service) (*Service, error) {
	return LoadServiceFromDirUnsafe(ctx, service.Dir())
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
