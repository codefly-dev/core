package architecture

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// Closure is the set of services one target actually needs, resolved by walking
// out from the target rather than by loading the whole workspace.
//
// NewServiceDependencies loads every module a workspace composes, so a single
// uncomposed, pinned or broken module fails planning for targets that never
// touch it. A Closure loads a module only when the walk reaches it; a module it
// does reach and cannot load becomes an unresolved node carrying an actionable
// reason, so the failure is reported instead of silently narrowing the closure.
type Closure struct {
	Workspace *resources.Workspace
	Target    string

	options     *DependencyOptions
	services    map[string]*resources.Service
	modules     map[string]*resources.Module
	resolutions map[string]*resources.ModuleResolution
	references  map[string]*resources.ModuleReference
	unresolved  map[string]string
	via         map[string][]string
	graph       *DAG
}

// SelectClosure resolves the closure required to operate target, given as
// "module/service". In a flat workspace the module may be omitted.
func SelectClosure(ctx context.Context, workspace *resources.Workspace, target string, opts ...DependencyOption) (*Closure, error) {
	w := wool.Get(ctx).In("architecture.SelectClosure", wool.NameField(target))
	options := &DependencyOptions{
		SkipDependencyFor: make(map[string]bool),
		ExcludeService:    make(map[string]bool),
	}
	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, w.Wrapf(err, "cannot apply option")
		}
	}
	unique, err := canonicalTarget(workspace, target)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if options.ExcludeService[unique] {
		return nil, w.NewError("target <%s> is excluded from the dependency graph", unique)
	}
	closure := &Closure{
		Workspace:   workspace,
		Target:      unique,
		options:     options,
		services:    make(map[string]*resources.Service),
		modules:     make(map[string]*resources.Module),
		resolutions: make(map[string]*resources.ModuleResolution),
		references:  make(map[string]*resources.ModuleReference),
		unresolved:  make(map[string]string),
		via:         make(map[string][]string),
	}
	if err := closure.walk(ctx); err != nil {
		return nil, w.Wrap(err)
	}
	return closure, nil
}

func canonicalTarget(workspace *resources.Workspace, target string) (string, error) {
	reference, err := resources.ParseServiceReference(strings.TrimSpace(target))
	if err != nil {
		return "", err
	}
	if reference.Module == "" {
		if workspace.Layout != resources.LayoutKindFlat {
			return "", fmt.Errorf("target <%s> must be given as module/service in workspace <%s>", target, workspace.Name)
		}
		reference.Module = workspace.Name
	}
	return resources.ServiceUnique(reference.Module, reference.Name), nil
}

func (closure *Closure) walk(ctx context.Context) error {
	graph := NewDAG(fmt.Sprintf("%s-closure-%s", closure.Workspace.Name, closure.Target))
	graph.WithRelationVerb("required by")
	graph.AddNode(closure.Target).WithType(resources.SERVICE)

	queue := []string{closure.Target}
	seen := map[string]bool{closure.Target: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		service, reason := closure.loadService(ctx, current)
		if reason != "" {
			closure.unresolved[current] = reason
			continue
		}
		closure.services[current] = service
		if closure.options.SkipDependencyFor[current] {
			continue
		}
		for _, dependency := range service.ServiceDependencies {
			unique := dependency.Unique()
			if closure.options.ExcludeService[unique] {
				continue
			}
			graph.AddNode(unique).WithType(resources.SERVICE)
			graph.AddEdge(unique, current)
			if seen[unique] {
				continue
			}
			seen[unique] = true
			closure.via[unique] = append(slices.Clone(closure.via[current]), current)
			queue = append(queue, unique)
		}
	}
	closure.graph = graph
	return nil
}

// loadService returns the service, or an actionable reason it cannot be loaded.
func (closure *Closure) loadService(ctx context.Context, unique string) (*resources.Service, string) {
	moduleName, name := resources.SplitUnique(unique)
	module, reason := closure.loadModule(ctx, moduleName)
	if reason != "" {
		return nil, reason
	}
	service, err := module.LoadServiceFromName(ctx, name)
	if err != nil {
		return nil, fmt.Sprintf("cannot load service <%s> from module <%s>: %s", name, moduleName, err)
	}
	return service, ""
}

func (closure *Closure) loadModule(ctx context.Context, name string) (*resources.Module, string) {
	if module, ok := closure.modules[name]; ok {
		return module, ""
	}
	reference := closure.moduleReference(name)
	if reference == nil {
		return nil, fmt.Sprintf("module <%s> is not composed in workspace <%s>; add it to %s",
			name, closure.Workspace.Name, resources.WorkspaceConfigurationName)
	}
	closure.references[name] = reference
	resolution, err := closure.Workspace.ResolveModule(ctx, reference)
	if err != nil {
		return nil, fmt.Sprintf("cannot resolve module <%s>: %s", name, err)
	}
	closure.resolutions[name] = resolution
	module, err := closure.Workspace.LoadModuleFromReference(ctx, reference)
	if err != nil {
		return nil, fmt.Sprintf("cannot load module <%s>: %s", name, err)
	}
	closure.modules[name] = module
	return module, ""
}

func (closure *Closure) moduleReference(name string) *resources.ModuleReference {
	for _, reference := range closure.Workspace.Modules {
		if resources.ReferenceMatch(reference.Name, name) {
			return reference
		}
	}
	return nil
}

// Verify rejects a closure that cannot be executed: a node the walk reached but
// could not resolve, a dependency cycle, or a dependency onto an endpoint the
// producer's visibility does not grant the consumer's module.
func (closure *Closure) Verify(ctx context.Context) error {
	w := wool.Get(ctx).In("architecture.Closure.Verify", wool.NameField(closure.Target))
	if err := closure.unresolvedError(ctx); err != nil {
		return err
	}
	if _, err := closure.graph.TopologicalSort(); err != nil {
		return w.Wrapf(err, "cannot order the closure of <%s>", closure.Target)
	}
	return verifyVisibility(ctx, closure.services)
}

func (closure *Closure) unresolvedError(ctx context.Context) error {
	if len(closure.unresolved) == 0 {
		return nil
	}
	unresolved := make([]string, 0, len(closure.unresolved))
	for unique, reason := range closure.unresolved {
		unresolved = append(unresolved, fmt.Sprintf("%s (%s): %s", unique, closure.requirement(unique), reason))
	}
	sort.Strings(unresolved)
	return wool.Get(ctx).In("architecture.Closure").NewError(
		"cannot resolve the closure of <%s>:\n  %s", closure.Target, strings.Join(unresolved, "\n  "))
}

func (closure *Closure) requirement(unique string) string {
	if unique == closure.Target {
		return "requested target"
	}
	via := closure.via[unique]
	if len(via) == 0 {
		return "required by " + closure.Target
	}
	return "required by " + via[len(via)-1]
}

// Order returns the closure in dependency order, target last. A closure holding
// an unresolved node has no runnable order, so it is refused rather than ordered
// around the gap.
func (closure *Closure) Order(ctx context.Context) ([]Service, error) {
	w := wool.Get(ctx).In("architecture.Closure.Order", wool.NameField(closure.Target))
	if err := closure.unresolvedError(ctx); err != nil {
		return nil, err
	}
	sorted, err := closure.graph.TopologicalSort()
	if err != nil {
		return nil, w.Wrapf(err, "cannot order the closure of <%s>", closure.Target)
	}
	out := make([]Service, 0, len(sorted))
	for _, node := range sorted {
		out = append(out, Service{Unique: node.ID})
	}
	return out, nil
}

// ServiceFromUnique returns a resolved member of the closure.
func (closure *Closure) ServiceFromUnique(unique string) (*resources.Service, error) {
	if service, ok := closure.services[unique]; ok {
		return service, nil
	}
	return nil, shared.NewErrorResourceNotFound("service", unique)
}
