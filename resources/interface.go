package resources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// Interface declaration constants.
const (
	InterfaceConfigurationName = "interface.codefly.yaml"
	InterfaceKind              = "interface"
)

// InterfaceType is how core validates and diffs an interface. Core knows the
// types; it never knows an instance. The endpoint types are spelled as the
// endpoint API that implements them, so an endpoint implements an interface
// only when the two names are equal.
type InterfaceType string

const (
	InterfaceTypeGRPC    InterfaceType = "grpc"
	InterfaceTypeConnect InterfaceType = "connect"
	InterfaceTypeREST    InterfaceType = "rest"
	InterfaceTypeHTTP    InterfaceType = "http"
	InterfaceTypeMCP     InterfaceType = "mcp"
	// InterfaceTypeCapability is a configuration-group schema: what a consumer
	// reads to reach a provider it talks to through a library driver rather
	// than an endpoint it calls.
	InterfaceTypeCapability InterfaceType = "capability"
)

// InterfaceTypes are the interface types this core understands. TCP is a
// transport and carries no interface.
func InterfaceTypes() []InterfaceType {
	return []InterfaceType{InterfaceTypeGRPC, InterfaceTypeConnect, InterfaceTypeREST, InterfaceTypeHTTP, InterfaceTypeMCP, InterfaceTypeCapability}
}

// ImplementedByEndpoint reports whether an endpoint, rather than a
// configuration group, implements interfaces of this type.
func (t InterfaceType) ImplementedByEndpoint() bool {
	return t != InterfaceTypeCapability
}

var (
	interfacePublisherPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	interfaceNamePattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	interfaceHTTPMethods      = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
)

// InterfaceIdentity names one published version of an interface:
// <publisher>/<name>@<version>, e.g. codefly.dev/cache@0.3.0. The version is
// the interface's own, never the version of anything implementing it: a
// provider can release as often as it likes without changing what it
// implements.
type InterfaceIdentity struct {
	Publisher string
	Name      string
	Version   string
}

// ParseInterfaceIdentity reads <publisher>/<name>@<version>. The version must
// be a strict semantic version: an identity names exactly one definition.
func ParseInterfaceIdentity(value string) (*InterfaceIdentity, error) {
	key, version, err := splitInterfaceReference(value)
	if err != nil {
		return nil, err
	}
	if _, err := semver.StrictNewVersion(version); err != nil {
		return nil, fmt.Errorf("interface %q version %q is not a strict semantic version", value, version)
	}
	return &InterfaceIdentity{Publisher: key.Publisher, Name: key.Name, Version: version}, nil
}

// Key returns <publisher>/<name>, the interface independent of its version.
func (id *InterfaceIdentity) Key() string {
	return id.Publisher + "/" + id.Name
}

func (id *InterfaceIdentity) String() string {
	return id.Key() + "@" + id.Version
}

// InterfaceRequirement is what a consumer needs: an interface and the range of
// its versions the consumer works with, e.g. codefly.dev/cache@^0.3.
type InterfaceRequirement struct {
	Publisher string
	Name      string
	Range     string

	constraint *semver.Constraints
}

// ParseInterfaceRequirement reads <publisher>/<name>@<range>. The range is
// required: a requirement that admits every version would bind to a provider
// whatever breaking change it later ships, which is the one thing versioning
// the interface exists to prevent.
func ParseInterfaceRequirement(value string) (*InterfaceRequirement, error) {
	key, rng, err := splitInterfaceReference(value)
	if err != nil {
		return nil, err
	}
	constraint, err := semver.NewConstraint(rng)
	if err != nil {
		return nil, fmt.Errorf("interface requirement %q range %q is invalid: %w", value, rng, err)
	}
	return &InterfaceRequirement{Publisher: key.Publisher, Name: key.Name, Range: rng, constraint: constraint}, nil
}

// Key returns <publisher>/<name>.
func (r *InterfaceRequirement) Key() string {
	return r.Publisher + "/" + r.Name
}

func (r *InterfaceRequirement) String() string {
	return r.Key() + "@" + r.Range
}

// Satisfies reports whether the identity is this interface at a version the
// range admits.
func (r *InterfaceRequirement) Satisfies(id *InterfaceIdentity) bool {
	if id == nil || id.Key() != r.Key() {
		return false
	}
	version, err := semver.StrictNewVersion(id.Version)
	if err != nil {
		return false
	}
	return r.constraint.Check(version)
}

type interfaceKey struct {
	Publisher string
	Name      string
}

func splitInterfaceReference(value string) (interfaceKey, string, error) {
	name, suffix, found := strings.Cut(value, "@")
	if !found || suffix == "" {
		return interfaceKey{}, "", fmt.Errorf("interface %q must be <publisher>/<name>@<version>", value)
	}
	publisher, bare, found := strings.Cut(name, "/")
	if !found {
		return interfaceKey{}, "", fmt.Errorf("interface %q must be <publisher>/<name>@<version>", value)
	}
	if err := validateInterfaceKey(publisher, bare); err != nil {
		return interfaceKey{}, "", fmt.Errorf("interface %q: %w", value, err)
	}
	return interfaceKey{Publisher: publisher, Name: bare}, suffix, nil
}

func validateInterfaceKey(publisher, name string) error {
	if !interfacePublisherPattern.MatchString(publisher) {
		return fmt.Errorf("publisher %q must be a lowercase domain-like name", publisher)
	}
	if !interfaceNamePattern.MatchString(name) {
		return fmt.Errorf("name %q must be lowercase letters, digits and dashes", name)
	}
	return nil
}

// Interface is the published definition of a named, versioned contract. The
// repository that owns an interface publishes it; core never defines one. A
// module declares which interfaces it implements in its module interface, and
// a consumer requires an interface in a service dependency.
//
// The definition carries the surface core can check structurally, in the one
// section its type selects.
type Interface struct {
	Kind        string        `yaml:"kind"`
	Publisher   string        `yaml:"publisher"`
	Name        string        `yaml:"name"`
	Description string        `yaml:"description,omitempty"`
	Version     string        `yaml:"version"`
	Type        InterfaceType `yaml:"type"`

	Protobuf   *InterfaceProtobuf   `yaml:"protobuf,omitempty"`
	OpenAPI    *InterfaceOpenAPI    `yaml:"openapi,omitempty"`
	MCP        *InterfaceMCP        `yaml:"mcp,omitempty"`
	Capability *InterfaceCapability `yaml:"capability,omitempty"`
}

// InterfaceProtobuf is the surface of a grpc or connect interface.
type InterfaceProtobuf struct {
	Package  string                      `yaml:"package"`
	Services []*InterfaceProtobufService `yaml:"services"`
}

// InterfaceProtobufService is one protobuf service and its methods.
type InterfaceProtobufService struct {
	Name    string   `yaml:"name"`
	Methods []string `yaml:"methods"`
}

// InterfaceOpenAPI is the surface of a rest or http interface.
type InterfaceOpenAPI struct {
	Routes []*InterfaceRoute `yaml:"routes"`
}

// InterfaceRoute is one operation of an OpenAPI surface.
type InterfaceRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

// InterfaceMCP is the surface of an MCP interface.
type InterfaceMCP struct {
	Tools     []string `yaml:"tools,omitempty"`
	Resources []string `yaml:"resources,omitempty"`
}

// InterfaceCapability is a configuration-group schema: the group a provider
// emits and the keys in it. It turns the key names a consumer reads, which were
// an informal agreement, into a checked one.
type InterfaceCapability struct {
	Configuration string                    `yaml:"configuration"`
	Keys          []*InterfaceCapabilityKey `yaml:"keys"`
}

// InterfaceCapabilityKey is one key of a capability's configuration group.
type InterfaceCapabilityKey struct {
	Name     string `yaml:"name"`
	Secret   bool   `yaml:"secret,omitempty"`
	Optional bool   `yaml:"optional,omitempty"`
}

// Identity returns the definition's identity.
func (i *Interface) Identity() *InterfaceIdentity {
	return &InterfaceIdentity{Publisher: i.Publisher, Name: i.Name, Version: i.Version}
}

// Validate rejects an incomplete or ambiguous definition.
func (i *Interface) Validate() error {
	if i.Kind != InterfaceKind {
		return fmt.Errorf("interface %q has kind %q, expected %q", i.Name, i.Kind, InterfaceKind)
	}
	if err := validateInterfaceKey(i.Publisher, i.Name); err != nil {
		return fmt.Errorf("interface %s/%s: %w", i.Publisher, i.Name, err)
	}
	label := i.Publisher + "/" + i.Name
	if _, err := semver.StrictNewVersion(i.Version); err != nil {
		return fmt.Errorf("interface %s version %q is not a strict semantic version", label, i.Version)
	}
	if !slices.Contains(InterfaceTypes(), i.Type) {
		return fmt.Errorf("interface %s type %q is not one of %v", label, i.Type, InterfaceTypes())
	}
	present := map[string]bool{
		"protobuf":   i.Protobuf != nil,
		"openapi":    i.OpenAPI != nil,
		"mcp":        i.MCP != nil,
		"capability": i.Capability != nil,
	}
	want := i.Type.section()
	for section, declared := range present {
		if declared && section != want {
			return fmt.Errorf("interface %s of type %q declares a %s section; its surface belongs in %s", label, i.Type, section, want)
		}
	}
	if !present[want] {
		return fmt.Errorf("interface %s of type %q must declare its %s surface", label, i.Type, want)
	}
	var err error
	switch want {
	case "protobuf":
		err = i.Protobuf.validate()
	case "openapi":
		err = i.OpenAPI.validate()
	case "mcp":
		err = i.MCP.validate()
	case "capability":
		err = i.Capability.validate()
	}
	if err != nil {
		return fmt.Errorf("interface %s: %w", label, err)
	}
	return nil
}

func (t InterfaceType) section() string {
	switch t {
	case InterfaceTypeGRPC, InterfaceTypeConnect:
		return "protobuf"
	case InterfaceTypeREST, InterfaceTypeHTTP:
		return "openapi"
	case InterfaceTypeMCP:
		return "mcp"
	}
	return "capability"
}

func (p *InterfaceProtobuf) validate() error {
	if strings.TrimSpace(p.Package) == "" {
		return fmt.Errorf("protobuf package is required")
	}
	if len(p.Services) == 0 {
		return fmt.Errorf("protobuf surface must declare at least one service")
	}
	seen := make(map[string]struct{})
	for _, service := range p.Services {
		if service == nil || strings.TrimSpace(service.Name) == "" {
			return fmt.Errorf("protobuf service requires a name")
		}
		if len(service.Methods) == 0 {
			return fmt.Errorf("protobuf service %s must declare at least one method", service.Name)
		}
		for _, method := range service.Methods {
			if strings.TrimSpace(method) == "" || strings.Contains(method, "/") {
				return fmt.Errorf("protobuf service %s method %q is invalid", service.Name, method)
			}
			procedure := "/" + p.Package + "." + service.Name + "/" + method
			if _, exists := seen[procedure]; exists {
				return fmt.Errorf("protobuf procedure %s is declared twice", procedure)
			}
			seen[procedure] = struct{}{}
		}
	}
	return nil
}

func (p *InterfaceProtobuf) procedures() map[string]struct{} {
	procedures := make(map[string]struct{})
	for _, service := range p.Services {
		for _, method := range service.Methods {
			procedures["/"+p.Package+"."+service.Name+"/"+method] = struct{}{}
		}
	}
	return procedures
}

func (o *InterfaceOpenAPI) validate() error {
	if len(o.Routes) == 0 {
		return fmt.Errorf("openapi surface must declare at least one route")
	}
	seen := make(map[string]struct{})
	for _, route := range o.Routes {
		if route == nil || !slices.Contains(interfaceHTTPMethods, route.Method) {
			return fmt.Errorf("openapi route method must be one of %v", interfaceHTTPMethods)
		}
		if !strings.HasPrefix(route.Path, "/") {
			return fmt.Errorf("openapi route path %q must start with /", route.Path)
		}
		key := route.Method + " " + route.Path
		if _, exists := seen[key]; exists {
			return fmt.Errorf("openapi route %s is declared twice", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (m *InterfaceMCP) validate() error {
	if len(m.Tools) == 0 && len(m.Resources) == 0 {
		return fmt.Errorf("mcp surface must declare at least one tool or resource")
	}
	for label, names := range map[string][]string{"tool": m.Tools, "resource": m.Resources} {
		seen := make(map[string]struct{})
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("mcp %s name cannot be empty", label)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("mcp %s %q is declared twice", label, name)
			}
			seen[name] = struct{}{}
		}
	}
	return nil
}

func (c *InterfaceCapability) validate() error {
	if strings.TrimSpace(c.Configuration) == "" {
		return fmt.Errorf("capability configuration group is required")
	}
	if len(c.Keys) == 0 {
		return fmt.Errorf("capability must declare at least one key")
	}
	seen := make(map[string]struct{})
	for _, key := range c.Keys {
		if key == nil || strings.TrimSpace(key.Name) == "" {
			return fmt.Errorf("capability key requires a name")
		}
		if _, exists := seen[key.Name]; exists {
			return fmt.Errorf("capability key %q is declared twice", key.Name)
		}
		seen[key.Name] = struct{}{}
	}
	return nil
}

// ValidateConfiguration checks that a provider's configuration conforms to a
// capability interface: the group is present, every required key is in it,
// each key is secret exactly when the interface says so, and no key outside
// the interface appears. An undeclared key is refused rather than tolerated:
// consumers would come to read it, and the informal interface the definition
// replaces would grow back beside it.
func (i *Interface) ValidateConfiguration(configuration *basev0.Configuration) error {
	label := i.Identity().String()
	if i.Type != InterfaceTypeCapability {
		return fmt.Errorf("interface %s is of type %q; only a capability interface constrains a configuration", label, i.Type)
	}
	if configuration == nil {
		return fmt.Errorf("interface %s: no configuration to validate", label)
	}
	var group *basev0.ConfigurationInformation
	for _, info := range configuration.Infos {
		if info.GetName() == i.Capability.Configuration {
			group = info
			break
		}
	}
	if group == nil {
		return fmt.Errorf("interface %s: configuration from %q has no %q group", label, configuration.Origin, i.Capability.Configuration)
	}
	declared := make(map[string]*InterfaceCapabilityKey, len(i.Capability.Keys))
	for _, key := range i.Capability.Keys {
		declared[key.Name] = key
	}
	emitted := make(map[string]struct{}, len(group.ConfigurationValues))
	var problems []string
	for _, value := range group.ConfigurationValues {
		key, ok := declared[value.Key]
		if !ok {
			problems = append(problems, fmt.Sprintf("key %q is not part of the interface", value.Key))
			continue
		}
		emitted[value.Key] = struct{}{}
		if key.Secret != value.Secret {
			problems = append(problems, fmt.Sprintf("key %q must have secret=%t", value.Key, key.Secret))
		}
	}
	for _, key := range i.Capability.Keys {
		if _, ok := emitted[key.Name]; !ok && !key.Optional {
			problems = append(problems, fmt.Sprintf("required key %q is missing", key.Name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("interface %s: configuration group %q from %q does not conform: %s", label, i.Capability.Configuration, configuration.Origin, strings.Join(problems, "; "))
	}
	return nil
}

// LoadInterfaceFromDir loads and validates a definition. It is decoded
// strictly: a misspelled "optionnal" would otherwise load as its opposite and
// be checked as a contract nobody wrote.
func LoadInterfaceFromDir(ctx context.Context, dir string) (*Interface, error) {
	w := wool.Get(ctx).In("LoadInterfaceFromDir", wool.DirField(dir))
	p, err := Path[Interface](ctx, dir)
	if err != nil {
		return nil, w.Wrap(err)
	}
	content, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, w.Wrap(shared.NewErrorResourceNotFound(TypeName[Interface](), p))
	}
	if err != nil {
		return nil, w.Wrapf(err, "cannot read %s", p)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	definition := &Interface{}
	if err := decoder.Decode(definition); err != nil {
		return nil, w.Wrapf(err, "cannot decode %s", p)
	}
	if err := definition.Validate(); err != nil {
		return nil, w.Wrap(err)
	}
	return definition, nil
}

// SaveToDir validates the definition and writes it to dir.
func (i *Interface) SaveToDir(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("Interface.SaveToDir", wool.NameField(i.Name))
	if err := i.Validate(); err != nil {
		return w.Wrap(err)
	}
	return SaveToDir[Interface](ctx, i, dir)
}

// InterfaceResolver returns the published definition of exactly one interface
// version. Core never fetches or vendors a definition: the host acquires and
// pins it, the way it acquires composed workspaces.
type InterfaceResolver func(ctx context.Context, identity *InterfaceIdentity) (*Interface, error)

type interfaceResolverKey struct{}

// WithInterfaceResolver attaches a resolver to the calls made with ctx.
func WithInterfaceResolver(ctx context.Context, resolver InterfaceResolver) context.Context {
	return context.WithValue(ctx, interfaceResolverKey{}, resolver)
}

// ResolveInterface returns the definition of identity through the resolver
// attached to ctx. A definition whose own identity differs from the one asked
// for is refused, so a resolver cannot substitute another version.
func ResolveInterface(ctx context.Context, identity *InterfaceIdentity) (*Interface, error) {
	resolver, _ := ctx.Value(interfaceResolverKey{}).(InterfaceResolver)
	if resolver == nil {
		return nil, fmt.Errorf("interface %s: no interface resolver is configured", identity)
	}
	definition, err := resolver(ctx, identity)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", identity, err)
	}
	if definition == nil {
		return nil, fmt.Errorf("interface %s: resolver returned no definition", identity)
	}
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	if got := definition.Identity().String(); got != identity.String() {
		return nil, fmt.Errorf("interface %s: resolver returned the definition of %s", identity, got)
	}
	return definition, nil
}
