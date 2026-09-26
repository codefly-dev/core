package resources

import (
	"context"
	"fmt"
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

// interfaceBinding records what a dependency declared before binding resolved
// it, so a save writes the author's declaration back and never the provider
// this workspace happened to bind.
type interfaceBinding struct {
	name      string
	module    string
	endpoints []*EndpointReference
}

func (s *ServiceDependency) interfaceOnly() bool {
	return s.Interface != "" && s.Name == ""
}

func (s *Service) hasInterfaceDependencies() bool {
	for _, dep := range s.ServiceDependencies {
		if dep.Interface != "" && dep.binding == nil {
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
	if err := bindInterfaceDependencies(service, providers, workspace.InterfaceBindings); err != nil {
		return w.Wrap(err)
	}
	return nil
}

func bindInterfaceDependencies(service *Service, providers []*InterfaceProvider, bindings []*InterfaceBinding) error {
	chosen := make(map[string]*InterfaceBinding, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			return fmt.Errorf("interface bindings contain a nil entry")
		}
		chosen[binding.Interface] = binding
	}
	for _, dep := range service.ServiceDependencies {
		if dep.Interface == "" || dep.binding != nil {
			continue
		}
		requirement, err := ParseInterfaceRequirement(dep.Interface)
		if err != nil {
			return fmt.Errorf("service %s: %w", service.label(), err)
		}
		provider, err := selectInterfaceProvider(dep, requirement, providers, chosen[requirement.Key()])
		if err != nil {
			return fmt.Errorf("service %s requires %s: %w", service.label(), requirement, err)
		}
		dep.binding = &interfaceBinding{name: dep.Name, module: dep.Module, endpoints: dep.Endpoints}
		dep.Name = provider.Service
		dep.Module = provider.Module
		if provider.Endpoint != "" && len(dep.Endpoints) == 0 {
			dep.Endpoints = []*EndpointReference{{Name: provider.Endpoint}}
		}
	}
	// A bound interface can land on a service the consumer also names
	// directly; the two entries would then be one dependency declared twice.
	return validateServiceDependencyNames(service.ServiceDependencies)
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
// declaration over to a reload, which read the dependencies back unbound.
func (s *Service) adoptInterfaceBindings(previous *Service) {
	bound := make(map[string]*ServiceDependency)
	for _, dep := range previous.ServiceDependencies {
		if dep.binding != nil {
			bound[dep.Interface] = dep
		}
	}
	for _, dep := range s.ServiceDependencies {
		source, ok := bound[dep.Interface]
		if !ok || dep.Interface == "" || dep.binding != nil {
			continue
		}
		dep.binding = &interfaceBinding{name: dep.Name, module: dep.Module, endpoints: dep.Endpoints}
		dep.Name = source.Name
		dep.Module = source.Module
		dep.Endpoints = source.Endpoints
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
