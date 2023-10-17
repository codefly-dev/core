package configurations

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
	"os"
	"path"
	"slices"
	"strings"
)

const ServiceConfigurationName = "service.codefly.yaml"

/*

Convention: Relative NewDir from Application

*/

type Service struct {
	Kind         string               `yaml:"kind"`
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	Application  string               `yaml:"applications"`
	RelativePath string               `yaml:"relative-path"`
	Namespace    string               `yaml:"namespace"`
	Domain       string               `yaml:"domain"`
	Plugin       *Plugin              `yaml:"plugin"`
	Dependencies []*ServiceDependency `yaml:"dependencies"`
	Endpoints    []*EndpointEntry     `yaml:"endpoints"`
	Spec         map[string]any       `yaml:"spec"`
}

func (s *Service) Dir() string {
	return path.Join(MustCurrentApplication().Dir(), s.RelativePath)
}

func ValidateServiceName(name string) error {
	return nil
}

func (s *Service) Unique() string {
	return fmt.Sprintf("%s.%s", s.Application, s.Name)
}

func NewService(name string, namespace string, plugin *Plugin) (*Service, error) {
	logger := shared.NewLogger("configurations.NewService")
	svc := Service{
		Kind:         "service",
		Name:         name,
		Application:  MustCurrentApplication().Name,
		RelativePath: name,
		Domain:       path.Join(MustCurrentApplication().Domain, name),
		Namespace:    namespace,
		Plugin:       plugin,
		Spec:         make(map[string]any),
	}
	logger.Debugf("Creating service <%s> at relative path to applications <%s>", svc.Name, name)
	return &svc, nil
}

type ServiceIdentity struct {
	Name      string
	Namespace string
	Domain    string
}

func Identity(conf *Service) *ServiceIdentity {
	return &ServiceIdentity{
		Name:      conf.Name,
		Namespace: conf.Namespace,
		Domain:    conf.Domain,
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
	scope := WithScope(opts...).WithApplication(MustCurrentApplication())
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
	conf.Application = MustCurrentApplication().Name
	conf.RelativePath = scope.Application.Relative(dir, opts...)
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

func FindServiceFromName(name string) (*Service, error) {
	logger := shared.NewLogger("configurations.FindServiceFromName<%s>", name)
	ref, err := MustCurrentApplication().GetServiceReferences(name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service configuration")
	}
	if ref == nil {
		return nil, logger.Errorf("service does not exist")
	}
	config, err := LoadServiceFromReference(ref)
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
		err := os.Mkdir(destination, 0755)
		if err != nil {
			return logger.Errorf("cannot create directory <%s>: %v", destination, err)
		}
	}
	p := path.Join(destination, ServiceConfigurationName)
	content, err := yaml.Marshal(*s)
	if err != nil {
		return logger.Errorf("cannot marshal service configuration: %s", err)
	}
	err = os.WriteFile(p, content, 0644)
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
	//s.Dependencies =
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
	}
}

type ServiceDependency struct {
	Name                string `yaml:"name"`
	RelativePath        string `yaml:"relative-path"`
	ApplicationOverride string `yaml:"applications,omitempty"`

	Uses []*EndpointEntry `yaml:"uses,omitempty"`
}

func (s *ServiceDependency) Validate() error {
	if s.RelativePath == "" {
		s.RelativePath = s.Name
	}
	return nil
}

const Unknown = "unknown"

const Grpc = "grpc"
const Http = "http"
const Tcp = "tcp"

const RestFramework = "rest"

var supportedApi []string

func init() {
	supportedApi = []string{Grpc}
}

func SupportedApi(kind string) error {
	if slices.Contains(supportedApi, kind) {
		return nil
	}
	return fmt.Errorf("unsupported api: %s", kind)
}

func WithApplication(app *Application) Option {
	return func(scope *Scope) {
		scope.Application = app
	}
}

func (ref *ServiceReference) Dir(opts ...Option) (string, error) {
	scope := WithScope()
	scope.Application = MustCurrentApplication()
	for _, opt := range opts {
		opt(scope)
	}
	// if no relative path is specified, we used the Name
	relativePath := ref.RelativePath
	if relativePath == "" {
		relativePath = ref.Name
	}
	if ref.ApplicationOverride == "" {
		return path.Join(scope.Application.Dir(), relativePath), nil
	}
	panic("TOTO")
	//app, err := LoadApplicationConfigurationFromName(s.ApplicationOverride)
	//if err != nil {
	//	return "", logger.Wrapf(err, "cannot load applications configuration: %s", s.ApplicationOverride)
	//}
	//return path.Join(app.NewDir(), relativePath), nil
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
