package configurations

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/codefly-dev/core/shared"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

const ServiceConfigurationName = "service.codefly.yaml"

/*
A Service

Convention: RelativePath from Application
*/
type Service struct {
	Kind         string               `yaml:"kind"`
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	Application  string               `yaml:"application"`
	RelativePath string               `yaml:"relative-path,omitempty"`
	Namespace    string               `yaml:"namespace"`
	Domain       string               `yaml:"domain"`
	Plugin       *Plugin              `yaml:"plugin"`
	Dependencies []*ServiceDependency `yaml:"dependencies"`
	Endpoints    []*Endpoint          `yaml:"endpoints"`
	Spec         map[string]any       `yaml:"spec"`
}

func (s *Service) Endpoint() string {
	return fmt.Sprintf("%s.%s", s.Name, s.Namespace)
}

func (s *Service) Dir(opts ...Option) string {
	scope := WithScope(opts...)
	return path.Join(scope.Application.Dir(), s.RelativePath)
}

func ValidateServiceName(name string) error {
	return nil
}

func (s *Service) Unique() string {
	return fmt.Sprintf("%s.%s", s.Name, s.Application)
}

func NewService(name string, namespace string, plugin *Plugin, ops ...Option) (*Service, error) {
	scope := WithScope(ops...)
	logger := shared.NewLogger("configurations.NewService<%s>", scope.Application.Name)
	svc := Service{
		Kind:         "service",
		Name:         name,
		Application:  scope.Application.Name,
		RelativePath: name,
		Domain:       scope.Application.ServiceDomain(name),
		Namespace:    namespace,
		Plugin:       plugin,
		Spec:         make(map[string]any),
	}
	logger.Debugf("new service configuration <%s> with relative path <%s>", svc.Name, svc.Name)
	return &svc, nil
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
	logger := shared.NewLogger("configurations.Unique<%s>.Reference", s.Name)
	entry := &ServiceReference{
		Name:         s.Name,
		RelativePath: s.RelativePath,
	}
	err := entry.Validate()
	if err != nil {
		return nil, logger.Wrapf(err, "invalid service entry")
	}
	return entry, nil
}

func LoadServiceFromDir(dir string, opts ...Option) (*Service, error) {
	logger := shared.NewLogger("configurations.LoadServiceFromPath<%s>", dir)
	conf, err := LoadFromDir[Service](dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service configuration")
	}
	// Normalize

	for _, entry := range conf.Dependencies {
		if entry.RelativePath == "" {
			entry.RelativePath = entry.Name
		}
	}
	conf.Plugin.Kind = PluginRuntimeService
	return conf, nil
}

/*
Derivatives
*/

func LoadServiceFromReference(ref *ServiceReference, opts ...Option) (*Service, error) {
	logger := shared.NewLogger("configurations.LoadServiceFromReference<%s>", ref.Name)
	p, err := ref.Dir(opts...)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get service path")
	}
	logger.Tracef("loading service <%s> at <%s>", ref.Name, p)
	return LoadServiceFromDir(p, opts...)
}

func FindServiceFromName(name string, opts ...Option) (*Service, error) {
	logger := shared.NewLogger("configurations.FindServiceFromName<%s>", name)
	scope := WithScope(opts...)
	ref, err := scope.Application.GetServiceReferences(name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service configuration")
	}
	if ref == nil {
		return nil, logger.Errorf("service does not exist")
	}
	config, err := LoadServiceFromReference(ref, opts...)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service configuration")
	}
	logger.Tracef("loading service <%s> from applications <%s> at <%s>", ref.Name, MustCurrentApplication().Name, ref.RelativePath)
	return config, nil
}

func (s *Service) Save() error {
	logger := shared.NewLogger("configurations.Unique<%s>.Save", s.Name)
	destination := s.Dir()
	logger.Tracef("saving service at <%s> from applications path <%s>", destination, MustCurrentApplication().Dir())
	return s.SaveAtDir(destination)
}

func (s *Service) SaveAtDir(destination string) error {
	logger := shared.NewLogger("configurations.Unique<%s>.Save", s.Name)
	if _, err := os.Stat(destination); os.IsNotExist(err) {
		err := os.Mkdir(destination, 0o755)
		if err != nil {
			return logger.Errorf("cannot create directory <%s>: %v", destination, err)
		}
	}
	p := path.Join(destination, ServiceConfigurationName)
	content, err := yaml.Marshal(*s)
	if err != nil {
		return logger.Errorf("cannot marshal service configuration: %s", err)
	}
	err = os.WriteFile(p, content, 0o644)
	if err != nil {
		return logger.Errorf("cannot save service configuration: %s", err)
	}
	return nil
}

func (s *Service) AddSpec(spec any) error {
	if s.Spec == nil {
		s.Spec = make(map[string]any)
	}
	if err := mapstructure.Decode(spec, &s.Spec); err != nil {
		return fmt.Errorf("cannot decode service spec: %s", err)
	}
	return nil
}

// AddDependencyReference adds a dependency to the service
func (s *Service) AddDependencyReference(requirement *Service) error {
	logger := shared.NewLogger("configurations.Unique.AddDependencyReference<%s> <- %s", s.Name, requirement.Name)
	logger.DebugMe("endpoints from the requirements: %v", requirement.Endpoints)
	for _, d := range requirement.Endpoints {
		logger.DebugMe("JERE DEP: %v", d)
	}
	logger.Debugf("adding dependency <%s > to requirement <%s>", requirement.Name, s.Name)
	// s.Dependencies =
	return nil
}

func (s *Service) Duplicate(name string) *Service {
	other := Service{
		Kind:         s.Kind,
		Name:         name,
		Version:      s.Version,
		Application:  s.Application,
		RelativePath: s.RelativePath,
		Namespace:    s.Namespace,
		Domain:       s.Domain,
		Plugin:       s.Plugin,
		Dependencies: s.Dependencies,
		Endpoints:    s.Endpoints,
		Spec:         s.Spec,
	}
	return &other
}

/*
LoadServicesFromInput from string inputs
*/
func LoadServicesFromInput(inputs ...string) ([]*Service, error) {
	logger := shared.NewLogger("configurations.LoadServicesFromInput")
	var services []*Service
	for _, input := range inputs {
		entry, err := LoadService(input)
		if err != nil {
			return nil, logger.Wrapf(err, "cannot load service entry")
		}
		services = append(services, entry)
	}
	return services, nil
}

func ParseServiceInput(input string) (string, string, error) {
	tokens := strings.Split(input, "@")
	if len(tokens) == 1 {
		return tokens[0], "", nil
	}
	if len(tokens) == 2 {
		return tokens[0], tokens[1], nil
	}
	return "", "", fmt.Errorf("invalid service entry: %s", input)
}

func LoadService(input string) (*Service, error) {
	logger := shared.NewLogger("configurations.LoadService")
	service, appOrNothing, err := ParseServiceInput(input)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot parse service entry")
	}
	if appOrNothing != "" {
		return nil, logger.Errorf("not implemented yet: with other applications")
	}
	return FindServiceFromName(service)
}

func (s *ServiceDependency) AsReference() *ServiceReference {
	return &ServiceReference{
		Name:         s.Name,
		RelativePath: s.RelativePath,
		Application:  s.Application,
	}
}

func (s *ServiceDependency) Unique() string {
	return fmt.Sprintf("%s.%s", s.Name, s.Application)
}

type ServiceDependency struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path,omitempty"`
	// Null application means self
	Application string `yaml:"application,omitempty"`

	Endpoints []*EndpointReference `yaml:"endpoints,omitempty"`
}

func (s *ServiceDependency) String() string {
	return fmt.Sprintf("ServiceDependency<%s.%s>", s.Name, s.Application)
}

func (s *ServiceDependency) Validate() error {
	if s.RelativePath == "" {
		s.RelativePath = s.Name
	}
	return nil
}

const Unknown = "unknown"

const (
	Grpc = "grpc"
	Rest = "rest"
	Tcp  = "tcp"
)

var supportedApi []string

func init() {
	supportedApi = []string{Grpc, Rest, Tcp}
}

func SupportedApi(kind string) error {
	if slices.Contains(supportedApi, kind) {
		return nil
	}
	return fmt.Errorf("unsupported api: %s", kind)
}

func (ref *ServiceReference) Dir(opts ...Option) (string, error) {
	logger := shared.NewLogger("configurations.ServiceReference.Dir<%s>", ref.Name)
	scope := WithScope(opts...)
	// if no relative path is specified, we used the Name
	relativePath := ref.RelativePath
	if relativePath == "" {
		relativePath = ref.Name
	}
	if ref.Application == "" {
		return path.Join(scope.Application.Dir(opts...), relativePath), nil
	}
	app, err := scope.Project.ApplicationByName(ref.Application)
	if err != nil {
		return "", logger.Errorf("cannot load applications configuration: %s", ref.Application)
	}
	return path.Join(app.Dir(), ref.RelativePath), nil
}

type ClientEntry struct {
	Name string   `yaml:"name"`
	Apis []string `yaml:"apis"`
}

func (c *ClientEntry) Validate() error {
	for _, api := range c.Apis {
		if err := SupportedApi(api); err != nil {
			return err
		}
	}
	return nil
}
