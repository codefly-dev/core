package resources

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/codefly-dev/core/wool"
)

// The workspace's module list answers "where does a module named X come from" —
// source, version, checkout location. It is a pin set. Which modules take part
// in a given run or build is a different question, and restating it as a second
// list is what lets the two drift: a module can be pinned and never run, and a
// run can need a module nobody remembered to list.
//
// ResolveModuleClosure answers the second question from the first plus the
// dependencies each service already declares, so there is exactly one statement
// of each fact and nothing to keep in sync.

// ModuleEdge is one derived participation edge: a service in From declared a
// dependency that To produces, which is what pulls To into the closure.
type ModuleEdge struct {
	From        string
	FromService string
	To          string
}

// ModuleClosure is the module set one stage needs: the seeds and everything
// their declarations transitively reach, in breadth-first order from the seeds.
type ModuleClosure struct {
	Modules []*Module
	Edges   []ModuleEdge
}

// Names returns the closure's module names, in closure order.
func (closure *ModuleClosure) Names() []string {
	names := make([]string, 0, len(closure.Modules))
	for _, mod := range closure.Modules {
		names = append(names, mod.Name)
	}
	return names
}

// Contains reports whether the module takes part in the closure.
func (closure *ModuleClosure) Contains(name string) bool {
	for _, mod := range closure.Modules {
		if mod.Name == name {
			return true
		}
	}
	return false
}

// moduleDemand is one entry of the walk frontier: the module to pull in, and
// who asked for it. The asker is what turns an uncovered pin set into an
// actionable error instead of a bare "not found".
type moduleDemand struct {
	module string
	// consumer and service name the declaring service; both empty for a seed.
	consumer string
	service  string
}

func (demand moduleDemand) unpinned(workspace *Workspace) error {
	pinned := workspace.ModulesNames()
	slices.Sort(pinned)
	if demand.consumer == "" {
		return fmt.Errorf("module %q is not pinned by workspace %q; pinned modules: %s",
			demand.module, workspace.Name, strings.Join(pinned, ", "))
	}
	return fmt.Errorf("service %s/%s depends on module %q, which workspace %q does not pin; pinned modules: %s",
		demand.consumer, demand.service, demand.module, workspace.Name, strings.Join(pinned, ", "))
}

// ResolveModuleClosure derives the module set one stage needs from what is
// being run or built and what that transitively declares it consumes. Seeds are
// the module names the stage starts from; each is resolved through the
// workspace's pin set, and every module a loaded service declares a
// stage-constraining dependency on joins the frontier.
//
// Only dependencies whose kind constrains the stage pull a module in. Building
// a service needs its codegen inputs and not the endpoints it will later
// consume; running it needs the reverse. Collapsing the two would make a stage
// demand pins for modules it never touches — and a dependency of kind external
// names a capability provided outside the workspace, so it constrains no stage
// and pulls in nothing.
//
// A pinned module nothing reaches is not in the closure: the pin set is a
// superset of any one stage. A module a declaration reaches but the pin set
// does not cover is an error naming the declaring service — that is the drift
// the hand-maintained list used to hide until the run came up with no
// endpoints.
func (workspace *Workspace) ResolveModuleClosure(ctx context.Context, stage Stage, seeds []string) (*ModuleClosure, error) {
	w := wool.Get(ctx).In("Workspace::ResolveModuleClosure", wool.NameField(workspace.Name))
	if err := stage.Validate(); err != nil {
		return nil, w.Wrap(err)
	}
	closure := &ModuleClosure{}
	seen := make(map[string]bool, len(seeds))
	// An edge records that a service pulls a module in, so several dependencies
	// from one service into one module are a single participation edge — the
	// producing service is not part of the identity.
	recorded := make(map[ModuleEdge]bool)
	var frontier []moduleDemand
	for _, seed := range seeds {
		if seen[seed] {
			continue
		}
		seen[seed] = true
		frontier = append(frontier, moduleDemand{module: seed})
	}
	for len(frontier) > 0 {
		demand := frontier[0]
		frontier = frontier[1:]
		ref := workspace.moduleReference(demand.module)
		if ref == nil {
			return nil, w.Wrap(demand.unpinned(workspace))
		}
		mod, err := workspace.LoadModuleFromReference(ctx, ref)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load module <%s>", demand.module)
		}
		closure.Modules = append(closure.Modules, mod)
		services, err := mod.LoadServices(ctx)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load services of module <%s>", mod.Name)
		}
		for _, svc := range services {
			for _, dep := range svc.ServiceDependencies {
				if !dep.Kind.Participates(stage) {
					continue
				}
				producer := dep.Module
				if producer == "" {
					producer = mod.Name
				}
				if producer == mod.Name {
					continue
				}
				edge := ModuleEdge{From: mod.Name, FromService: svc.Name, To: producer}
				if !recorded[edge] {
					recorded[edge] = true
					closure.Edges = append(closure.Edges, edge)
				}
				if seen[producer] {
					continue
				}
				seen[producer] = true
				frontier = append(frontier, moduleDemand{module: producer, consumer: mod.Name, service: svc.Name})
			}
		}
	}
	return closure, nil
}

// ValidateServiceDependencies applies the workspace's endpoint-visibility rules
// to exactly the modules that take part in the closure. It shares its
// implementation with the workspace-wide pass, so the check a run performs and
// the check validation performs cannot diverge — only their scope differs.
func (closure *ModuleClosure) ValidateServiceDependencies(ctx context.Context) error {
	w := wool.Get(ctx).In("ModuleClosure::ValidateServiceDependencies")
	if err := validateModuleDependencyVisibility(ctx, closure.Modules); err != nil {
		return w.Wrap(err)
	}
	return nil
}

// moduleReference returns the pin for a module name, or nil when the workspace
// pins no such module.
func (workspace *Workspace) moduleReference(name string) *ModuleReference {
	for _, ref := range workspace.Modules {
		if ReferenceMatch(ref.Name, name) {
			return ref
		}
	}
	return nil
}
