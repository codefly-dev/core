package resources

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/codefly-dev/core/wool"
)

// InterfaceProvider is one place in a workspace that implements an interface
// version: an exported endpoint, or a service's capability when Endpoint is
// empty.
type InterfaceProvider struct {
	Identity *InterfaceIdentity
	Module   string
	Service  string
	Endpoint string
}

func (p *InterfaceProvider) location() string {
	if p.Endpoint == "" {
		return p.Module + "/" + p.Service
	}
	return p.Module + "/" + p.Service + "/" + p.Endpoint
}

func (p *InterfaceProvider) String() string {
	return p.location() + " (" + p.Identity.String() + ")"
}

// InterfaceBinding chooses the provider of an interface when more than one is
// in scope. It names the interface without a version: the consumer's range
// still decides whether the chosen provider is acceptable.
type InterfaceBinding struct {
	Interface string `yaml:"interface"`
	Module    string `yaml:"module"`
	Service   string `yaml:"service"`
}

// interfaceBinding records what an interface-only dependency declared before
// binding resolved it to a provider, so a save writes the author's declaration
// back and never the provider this workspace happened to bind. A dependency
// that names its service is never rewritten by binding, only checked.
type interfaceBinding struct {
	module    string
	endpoints []*EndpointReference
}

func (s *ServiceDependency) interfaceOnly() bool {
	return s.Interface != "" && s.Name == ""
}

// boundByInterface reports whether Name and Module hold a provider binding
// chose rather than a service the author named. Such a dependency is the
// author's requirement of an interface, not of that service: an operation on
// the named service must neither match nor remove it.
func (s *ServiceDependency) boundByInterface() bool {
	return s.binding != nil
}

func (s *Service) hasInterfaceDependencies() bool {
	for _, dep := range s.ServiceDependencies {
		if dep.Interface != "" && dep.binding == nil && !dep.interfaceVerified {
			return true
		}
	}
	return false
}

// InterfaceProviders lists every interface implementation the workspace's
// modules declare.
func (workspace *Workspace) InterfaceProviders(ctx context.Context) ([]*InterfaceProvider, error) {
	w := wool.Get(ctx).In("Workspace::InterfaceProviders", wool.NameField(workspace.Name))
	modules, err := workspace.LoadModules(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	var providers []*InterfaceProvider
	for _, mod := range modules {
		declared, err := mod.InterfaceProviders()
		if err != nil {
			return nil, w.Wrap(err)
		}
		providers = append(providers, declared...)
	}
	return providers, nil
}

// BindInterfaceDependencies resolves each interface dependency of service to
// exactly one provider in the workspace. A dependency that also names a
// service is checked against it. Otherwise the provider is the one satisfying
// the required range, or the one the workspace's interface-bindings choose.
// No provider, or several without a binding, is an error: picking one
// implicitly would make which provider a consumer reaches depend on what else
// happens to be composed.
func (workspace *Workspace) BindInterfaceDependencies(ctx context.Context, service *Service) error {
	w := wool.Get(ctx).In("Workspace::BindInterfaceDependencies", wool.NameField(service.label()))
	if !service.hasInterfaceDependencies() {
		return nil
	}
	providers, err := workspace.InterfaceProviders(ctx)
	if err != nil {
		return w.Wrap(err)
	}
	bound, err := bindInterfaceDependencies(service, providers, workspace.InterfaceBindings)
	if err != nil {
		return w.Wrap(err)
	}
	// With a resolver attached, each implementation a consumer is bound to is
	// checked against its published definition, and only those: a definition
	// the host cannot resolve fails the consumers relying on it, not every
	// load of the module that declares it.
	if !hasInterfaceResolver(ctx) {
		return nil
	}
	checked := make(map[string]bool)
	for _, provider := range bound {
		key := provider.String()
		if checked[key] {
			continue
		}
		checked[key] = true
		mod, err := workspace.LoadModuleFromName(ctx, provider.Module)
		if err != nil {
			return w.Wrap(err)
		}
		if err := mod.validateImplementation(ctx, provider); err != nil {
			return w.Wrapf(err, "service %s is bound to %s", service.label(), provider)
		}
	}
	return nil
}

// bindInterfaceDependencies binds the service's interface dependencies and
// returns the implementations it bound them to.
func bindInterfaceDependencies(service *Service, providers []*InterfaceProvider, bindings []*InterfaceBinding) ([]*InterfaceProvider, error) {
	chosen, err := validateInterfaceBindings(providers, bindings)
	if err != nil {
		return nil, err
	}
	var bound []*InterfaceProvider
	for _, dep := range service.ServiceDependencies {
		if dep.Interface == "" || dep.binding != nil || dep.interfaceVerified {
			continue
		}
		requirement, err := ParseInterfaceRequirement(dep.Interface)
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", service.label(), err)
		}
		provider, err := selectInterfaceProvider(dep, requirement, providers, chosen[requirement.Key()])
		if err != nil {
			return nil, fmt.Errorf("service %s requires %s: %w", service.label(), requirement, err)
		}
		bound = append(bound, provider)
		if dep.Name != "" {
			dep.interfaceVerified = true
			continue
		}
		dep.binding = &interfaceBinding{module: dep.Module, endpoints: dep.Endpoints}
		dep.Name = provider.Service
		dep.Module = provider.Module
		// A completion edge waits for finished work and consumes no endpoint,
		// so it is never narrowed to the implementing one.
		if provider.Endpoint != "" && len(dep.Endpoints) == 0 && dep.Kind != DependencyKindCompletion {
			dep.Endpoints = []*EndpointReference{{Name: provider.Endpoint}}
		}
	}
	// Two requirements may bind to one provider — two interfaces one endpoint
	// serves, or an interface and a service the consumer also names. They are
	// separate declarations, not one written twice: the dependency graph merges
	// their edges and unions their kinds, and network mappings union what each
	// consumes.
	return bound, nil
}

// validateInterfaceBindings checks every binding the workspace declares, not
// only those a dependency happens to use: a misspelled interface would
// otherwise match nothing, and the consumer would be told to add the binding
// its author already wrote.
func validateInterfaceBindings(providers []*InterfaceProvider, bindings []*InterfaceBinding) (map[string]*InterfaceBinding, error) {
	chosen := make(map[string]*InterfaceBinding, len(bindings))
	for _, binding := range bindings {
		if err := binding.validate(); err != nil {
			return nil, err
		}
		if _, exists := chosen[binding.Interface]; exists {
			return nil, fmt.Errorf("interface binding for %s is declared twice", binding.Interface)
		}
		chosen[binding.Interface] = binding
		provides := false
		for _, provider := range providers {
			if provider.Identity.Key() == binding.Interface && provider.Module == binding.Module && provider.Service == binding.Service {
				provides = true
				break
			}
		}
		if !provides {
			var implementing []*InterfaceProvider
			for _, provider := range providers {
				if provider.Identity.Key() == binding.Interface {
					implementing = append(implementing, provider)
				}
			}
			return nil, fmt.Errorf("interface binding for %s chooses %s/%s, which implements no version of it; implementations in scope: %s",
				binding.Interface, binding.Module, binding.Service, describeProviders(implementing))
		}
	}
	return chosen, nil
}

func (b *InterfaceBinding) validate() error {
	if b == nil {
		return fmt.Errorf("interface bindings contain a nil entry")
	}
	publisher, name, found := strings.Cut(b.Interface, "/")
	if !found {
		return fmt.Errorf("interface binding %q must name an interface as <publisher>/<name>, without a version", b.Interface)
	}
	if err := validateInterfaceKey(publisher, name); err != nil {
		return fmt.Errorf("interface binding %q: %w", b.Interface, err)
	}
	if strings.TrimSpace(b.Module) == "" || strings.TrimSpace(b.Service) == "" {
		return fmt.Errorf("interface binding for %s must name a module and a service", b.Interface)
	}
	return nil
}

func selectInterfaceProvider(dep *ServiceDependency, requirement *InterfaceRequirement, providers []*InterfaceProvider, binding *InterfaceBinding) (*InterfaceProvider, error) {
	var implementing, satisfying []*InterfaceProvider
	for _, provider := range providers {
		if provider.Identity.Key() != requirement.Key() {
			continue
		}
		implementing = append(implementing, provider)
		if requirement.Satisfies(provider.Identity) {
			satisfying = append(satisfying, provider)
		}
	}
	module, name, source := "", "", ""
	switch {
	case dep.Name != "":
		module, name, source = dep.Module, dep.Name, "the named service"
	case binding != nil:
		module, name, source = binding.Module, binding.Service, "the workspace binding"
	}
	if name != "" {
		var matched []*InterfaceProvider
		for _, provider := range satisfying {
			if provider.Module == module && provider.Service == name {
				matched = append(matched, provider)
			}
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("%s %s/%s provides no version in range; implementations in scope: %s", source, module, name, describeProviders(implementing))
		}
		satisfying = matched
	}
	// Endpoints the dependency names select among endpoint implementations: a
	// consumer that names one is served by that endpoint, so it must be one
	// that implements the requirement. A capability is implemented by the
	// service, not an endpoint, and a consumer may name the endpoint it
	// connects to without narrowing which service provides the capability.
	if named := namedEndpoints(dep); len(named) > 0 {
		var matched []*InterfaceProvider
		for _, provider := range satisfying {
			if provider.Endpoint == "" || slices.Contains(named, provider.Endpoint) {
				matched = append(matched, provider)
			}
		}
		if len(matched) == 0 {
			return nil, fmt.Errorf("none of the endpoints it names (%s) implements a version in range; implementations in scope: %s", strings.Join(named, ", "), describeProviders(implementing))
		}
		satisfying = matched
	}
	switch len(satisfying) {
	case 0:
		return nil, fmt.Errorf("no provider in scope satisfies it; implementations in scope: %s", describeProviders(implementing))
	case 1:
		return satisfying[0], nil
	}
	if name != "" {
		return nil, fmt.Errorf("%s %s/%s implements it more than once (%s); name the endpoint in the dependency", source, module, name, describeProviders(satisfying))
	}
	return nil, fmt.Errorf("several providers satisfy it (%s); choose one in the workspace's interface-bindings", describeProviders(satisfying))
}

func namedEndpoints(dep *ServiceDependency) []string {
	var named []string
	for _, reference := range dep.Endpoints {
		if reference != nil && reference.Name != "" {
			named = append(named, reference.Name)
		}
	}
	return named
}

func describeProviders(providers []*InterfaceProvider) string {
	if len(providers) == 0 {
		return "none"
	}
	described := make([]string, 0, len(providers))
	for _, provider := range providers {
		described = append(described, provider.String())
	}
	sort.Strings(described)
	return strings.Join(described, ", ")
}

// ApplyInterfaceBindings binds the interface dependencies of a service loaded
// by directory, against the workspace at workspaceDir. It is the counterpart of
// ApplyModuleInterface for callers that cannot go through
// Module.LoadService*: an agent loading the service it serves. A service with
// no interface dependency is left untouched and the workspace is not loaded.
func ApplyInterfaceBindings(ctx context.Context, service *Service, workspaceDir string) error {
	w := wool.Get(ctx).In("resources.ApplyInterfaceBindings", wool.NameField(service.label()))
	if !service.hasInterfaceDependencies() {
		return nil
	}
	workspace, err := LoadWorkspaceFromDir(ctx, workspaceDir)
	if err != nil {
		return w.Wrapf(err, "cannot load the workspace that binds the service's interfaces")
	}
	return workspace.BindInterfaceDependencies(ctx, service)
}

// adoptInterfaceBindings carries the bindings of a previous load of the same
// declaration over to a reload, which read the dependencies back unbound. A
// dependency is matched by what its author declared — the requirement, and the
// service when one is named — never by the requirement alone: one service can
// require the same interface twice, once from whichever provider is bound and
// once from a service it names.
func (s *Service) adoptInterfaceBindings(previous *Service) {
	resolved := make(map[string]*ServiceDependency)
	verified := make(map[string]bool)
	for _, dep := range previous.ServiceDependencies {
		switch {
		case dep.binding != nil:
			resolved[dep.Interface] = dep
		case dep.interfaceVerified:
			verified[dep.Interface+"\x00"+dep.Unique()] = true
		}
	}
	for _, dep := range s.ServiceDependencies {
		switch {
		case dep.Interface == "" || dep.binding != nil || dep.interfaceVerified:
		case dep.interfaceOnly():
			source, ok := resolved[dep.Interface]
			if !ok {
				continue
			}
			dep.binding = &interfaceBinding{module: dep.Module, endpoints: dep.Endpoints}
			dep.Name = source.Name
			dep.Module = source.Module
			dep.Endpoints = source.Endpoints
		default:
			dep.interfaceVerified = verified[dep.Interface+"\x00"+dep.Unique()]
		}
	}
}

func (s *Service) label() string {
	if s.module == "" {
		return s.Name
	}
	return s.module + "/" + s.Name
}

// refuseInterfaceDependencies rejects interface dependencies on resources the
// workspace does not bind. Only services are bound; an unbound requirement on
// a job, runnable or application would reach its graph naming no producer.
func refuseInterfaceDependencies(kind, name string, dependencies []*ServiceDependency) error {
	for _, dep := range dependencies {
		if dep != nil && dep.Interface != "" {
			return fmt.Errorf("%s %q requires interface %s, but only a service's interface dependencies are bound; name the providing service instead", kind, name, dep.Interface)
		}
	}
	return nil
}

// bindUpFrom binds a service found by directory against the workspace above
// from. Without one the requirement stays unbound, and the runtime paths refuse
// it when it is used.
func bindUpFrom(ctx context.Context, service *Service, from string) error {
	if !service.hasInterfaceDependencies() {
		return nil
	}
	dir, err := FindUpFrom[Workspace](ctx, from)
	if err != nil || dir == nil {
		return err
	}
	return ApplyInterfaceBindings(ctx, service, *dir)
}
