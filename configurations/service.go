package configurations

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
	"os"
	"path"
	"slices"
)

const ServiceConfigurationName = "service.codefly.yaml"

/*
A Service

Convention: RelativePath from Application
*/
type Service struct {
	Kind                 string               `yaml:"kind"`
	Name                 string               `yaml:"name"`
	Version              string               `yaml:"version"`
	Application          string               `yaml:"application"`
	RelativePathOverride *string              `yaml:"relative-path,omitempty"`
	Namespace            string               `yaml:"namespace"`
	Domain               string               `yaml:"domain"`
	Plugin               *Plugin              `yaml:"plugin"`
	Dependencies         []*ServiceDependency `yaml:"dependencies"`
	Endpoints            []*Endpoint          `yaml:"endpoints"`
	Spec                 map[string]any       `yaml:"spec"`
}

func (s *Service) Endpoint() string {
	return fmt.Sprintf("%s.%s", s.Name, s.Namespace)
}

func (s *Service) Dir(opts ...Option) string {
	scope := WithScope(opts...)
	return path.Join(scope.Application.Dir(), s.RelativePath())
}

func ValidateServiceName(name string) error {
	return nil
}

// Unique identifies a service within a project
// We use a REST like convention rather then a sub-domain one
func (s *Service) Unique() string {
	return fmt.Sprintf("%s/%s", s.Application, s.Name)
}

func NewService(name string, namespace string, plugin *Plugin, ops ...Option) (*Service, error) {
	scope := WithScope(ops...)
	logger := shared.NewLogger("configurations.NewService<%s>", scope.Application.Name)
	svc := Service{
		Kind:        "service",
		Name:        name,
		Application: scope.Application.Name,
		Domain:      scope.Application.ServiceDomain(name),
		Namespace:   namespace,
		Plugin:      plugin,
		Spec:        make(map[string]any),
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
	entry := &ServiceReference{
		Name:                 s.Name,
		RelativePathOverride: s.RelativePathOverride,
	}
	return entry, nil
}

func LoadServiceFromDir(dir string, opts ...Option) (*Service, error) {
	logger := shared.NewLogger("configurations.LoadServiceFromPath<%s>", dir)
	conf, err := LoadFromDir[Service](dir)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service configuration")
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

func FindServiceFromReference(ref *ServiceReference) (*Service, error) {
	logger := shared.NewLogger("configurations.FindServiceFromReference<%s/%s>", ref.Application, ref.Name)
	// Find the application
	app, err := MustCurrentProject().LoadApplicationFromReference(&ApplicationReference{Name: ref.Application})
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load application configuration")
	}

	config, err := app.LoadServiceFromName(ref.Name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service configuration")
	}
	logger.Tracef("loading service <%s> from applications <%s> at <%s>", ref.Name, app.Name, ref.RelativePath())
	return config, nil
}

func (s *Service) Save() error {
	logger := shared.NewLogger("configurations.Unique<%s>.Save", s.Name)
	destination := s.Dir()
	logger.Tracef("saving service at <%s> from applications path <%s>", destination, MustCurrentApplication().Dir())
	return s.SaveAtDir(destination)
}

func (s *Service) SaveAtDir(destination string) error {
	logger := shared.NewLogger("configurations.Unique<%s>.Save", s.Unique())
	if _, err := os.Stat(destination); os.IsNotExist(err) {
		err := os.Mkdir(destination, 0o755)
		if err != nil {
			return logger.Errorf("cannot create directory <%s>: %v", destination, err)
		}
	}
	p := path.Join(destination, ServiceConfigurationName)
	content, err := yaml.Marshal(s)
	if err != nil {
		return logger.Errorf("cannot marshal service configuration: %s", err)
	}
	err = os.WriteFile(p, content, 0o644)
	if err != nil {
		return logger.Errorf("cannot save service configuration: %s", err)
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
		Kind:                 s.Kind,
		Name:                 name,
		Version:              s.Version,
		Application:          s.Application,
		RelativePathOverride: s.RelativePathOverride,
		Namespace:            s.Namespace,
		Domain:               s.Domain,
		Plugin:               s.Plugin,
		Dependencies:         s.Dependencies,
		Endpoints:            s.Endpoints,
		Spec:                 s.Spec,
	}
	return &other
}

func (s *Service) RelativePath() string {
	if s.RelativePathOverride != nil {
		return *s.RelativePathOverride
	}
	return s.Name

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

func LoadService(input string) (*Service, error) {
	logger := shared.NewLogger("configurations.LoadService")
	ref, err := ParseServiceReference(input)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot parse service entry")
	}
	return FindServiceFromReference(ref)
}

func (s *ServiceDependency) AsReference() *ServiceReference {
	return &ServiceReference{
		Name:                 s.Name,
		RelativePathOverride: s.RelativePathOverride,
		Application:          s.Application,
	}
}

func (s *ServiceDependency) Unique() string {
	return fmt.Sprintf("%s.%s", s.Name, s.Application)
}

type ServiceDependency struct {
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path,omitempty"`
	// Null application means self
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
	Tcp     = "tcp"
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
