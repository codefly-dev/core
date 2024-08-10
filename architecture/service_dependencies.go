package architecture

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

/*
Overview builds a dependency graph of the module and its services.
*/

type ServiceDependencies struct {
	Workspace *resources.Workspace

	graph           *DAG
	uniqueToService map[string]*resources.Service
	options         *DependencyOptions
}

type DependencyOptions struct {
	SkipDependencyFor map[string]bool
}

type DependencyOption func(*DependencyOptions) error

func SkipDependencyFor(services ...string) DependencyOption {
	return func(opt *DependencyOptions) error {
		for _, svc := range services {
			opt.SkipDependencyFor[svc] = true
		}
		return nil
	}
}

func NewServiceDependencies(ctx context.Context, workspace *resources.Workspace, opts ...DependencyOption) (*ServiceDependencies, error) {
	w := wool.Get(ctx).In("NewServiceDependencies")
	opt := &DependencyOptions{
		SkipDependencyFor: make(map[string]bool),
	}
	for _, o := range opts {
		err := o(opt)
		if err != nil {
			return nil, w.Wrapf(err, "cannot apply option")
		}

	}
	dep := &ServiceDependencies{
		Workspace:       workspace,
		options:         opt,
		uniqueToService: make(map[string]*resources.Service),
	}
	err := dep.loadServiceGraph(ctx, workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service graph")
	}
	dep.graph.verb = "required by"
	return dep, nil
}

func (d *ServiceDependencies) ServiceFromUnique(unique string) (*resources.Service, error) {
	if svc, ok := d.uniqueToService[unique]; ok {
		return svc, nil
	}
	return nil, shared.NewErrorResourceNotFound("service", unique)
}

// DependsOn returns true if the service identified by unique depends on the service identified by other
func (d *ServiceDependencies) DependsOn(unique string, other string) (bool, error) {
	w := wool.Get(context.Background()).In("ServiceDependencies.DependsOn")
	if !d.graph.HasNode(unique) {
		return false, w.NewError("service <%s> does not exist", unique)
	}
	if !d.graph.HasNode(other) {
		return false, w.NewError("service <%s> does not exist", other)
	}
	// A depends on B is represented by an path B-> ...->  A
	return d.graph.ReachableFrom(other, unique), nil
}

type Service struct {
	Unique string
}

type ServiceDependency struct {
	From Service
	To   Service
}

func (d *ServiceDependencies) Print() string {
	return d.graph.Print()
}

func (d *ServiceDependencies) Services() []Service {
	var out []Service
	for _, node := range d.graph.Nodes() {
		if node.Type == resources.SERVICE {
			out = append(out, Service{
				Unique: node.ID,
			})
		}
	}
	return out
}

func (d *ServiceDependencies) Dependencies() []ServiceDependency {
	var out []ServiceDependency
	for _, edge := range d.graph.Edges() {
		out = append(out, ServiceDependency{
			From: Service{
				Unique: edge.From,
			},
			To: Service{
				Unique: edge.To,
			},
		})
	}
	return out
}

// OrderTo returns the list of services "required" to end up with the service identified by unique.
func (d *ServiceDependencies) OrderTo(ctx context.Context, unique string) ([]Service, error) {
	w := wool.Get(ctx).In("OrderTo")
	sub, err := d.graph.SubGraphTo(unique)
	if err != nil {
		return nil, fmt.Errorf("cannot topologically sort to <%s>: %w", unique, err)
	}
	w.Trace("service dependencies", wool.Field("graph", d.graph.PrintAsDot()))
	w.Trace("service dependencies", wool.Field("subgraph", sub.PrintAsDot()))
	order, err := sub.TopologicalSortTo(unique)
	if err != nil {
		return nil, fmt.Errorf("cannot topologically sort to <%s>: %w", unique, err)
	}
	w.Trace("service dependencies", wool.Field("order", order))
	var out []Service
	for _, u := range order {
		if u.Type != resources.SERVICE {
			continue
		}
		out = append(out, Service{
			Unique: u.ID,
		})
	}
	return out, nil
}

// DirectRequires returns the list of services that are directly required by the service identified by unique
// Result is sorted by topological order
func (d *ServiceDependencies) DirectRequires(ctx context.Context, unique string) ([]Service, error) {
	w := wool.Get(ctx).In("DirectRequires")
	children, err := d.graph.SortedParents(unique)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get sorted parents to <%s>", unique)
	}
	var out []Service
	for _, child := range children {
		if child.Type != resources.SERVICE {
			continue
		}
		out = append(out, Service{
			Unique: child.ID,
		})
	}
	return out, nil
}

// DirectDependents returns the list of services that are directly dependent on the service identified by unique
// Result is sorted by topological order
func (d *ServiceDependencies) DirectDependents(ctx context.Context, unique string) ([]Service, error) {
	w := wool.Get(ctx).In("DirectDependents")
	children, err := d.graph.SortedChildren(unique)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get sorted children from <%s>", unique)
	}
	var out []Service
	for _, child := range children {
		if child.Type != resources.SERVICE {
			continue
		}
		out = append(out, Service{
			Unique: child.ID,
		})
	}
	return out, nil
}

// Restrict restricts the dependencies to the services required by the service identified by unique
func (d *ServiceDependencies) Restrict(_ context.Context, unique string) (*ServiceDependencies, error) {
	// B is required by A if A <- ... <- B
	sub, err := d.graph.SubGraphTo(unique)
	if err != nil {
		return nil, fmt.Errorf("cannot restrict to <%s>: %w", unique, err)
	}
	return &ServiceDependencies{
		Workspace: d.Workspace,
		graph:     sub,
	}, nil
}

// X depends on Y means an edge X <- Y
func (d *ServiceDependencies) loadServiceGraph(ctx context.Context, workspace *resources.Workspace) error {
	w := wool.Get(ctx).In("loadServiceGraph")
	w.Debug("analyzing workspace", wool.Field("workspace", workspace.Name), wool.Field("#modules", len(workspace.Modules)))
	graph := NewDAG(workspace.Name)
	for _, modRef := range workspace.Modules {
		mod, err := workspace.LoadModuleFromReference(ctx, modRef)
		if err != nil {
			return w.Wrapf(err, "cannot load module <%s>", modRef.Name)
		}
		for _, serviceRef := range mod.ServiceReferences {
			svc, err := mod.LoadServiceFromReference(ctx, serviceRef)
			if err != nil {
				return w.Wrapf(err, "cannot load svc <%s>", serviceRef.Name)
			}
			identity, err := svc.Identity()
			if err != nil {
				return w.Wrapf(err, "cannot get service identity")
			}
			d.uniqueToService[identity.Unique()] = svc
			graph.AddNode(identity.Unique()).WithType(resources.SERVICE)
			if _, skipDependency := d.options.SkipDependencyFor[identity.Unique()]; skipDependency {
				continue
			}
			for _, dep := range svc.ServiceDependencies {
				graph.AddNode(dep.Unique()).WithType(resources.SERVICE)
				graph.AddEdge(dep.Unique(), identity.Unique())
			}
		}
	}
	d.graph = graph
	return nil
}

// EntryPoints returns the list of services that are not required by any other service
func (d *ServiceDependencies) EntryPoints(_ context.Context) ([]Service, error) {
	var out []Service
	for _, node := range d.graph.Nodes() {
		if node.Type != resources.SERVICE {
			continue
		}
		if len(d.graph.Children(node.ID)) == 0 {
			out = append(out, Service{
				Unique: node.ID,
			})
		}
	}
	return out, nil
}
