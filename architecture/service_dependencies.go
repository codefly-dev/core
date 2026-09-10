package architecture

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/codefly-dev/core/graph"
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
	ExcludeService    map[string]bool
}

func (opt *DependencyOptions) clone() *DependencyOptions {
	out := &DependencyOptions{
		SkipDependencyFor: make(map[string]bool, len(opt.SkipDependencyFor)),
		ExcludeService:    make(map[string]bool, len(opt.ExcludeService)),
	}
	maps.Copy(out.SkipDependencyFor, opt.SkipDependencyFor)
	maps.Copy(out.ExcludeService, opt.ExcludeService)
	return out
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

// ExcludeServices removes services from the dependency graph entirely.
// Use this for optional infrastructure that the current local run does not
// need, for example disabling infra/temporal while testing code paths that only
// require Postgres and Neo4j.
func ExcludeServices(services ...string) DependencyOption {
	return func(opt *DependencyOptions) error {
		for _, svc := range services {
			opt.ExcludeService[svc] = true
		}
		return nil
	}
}

func NewServiceDependencies(ctx context.Context, workspace *resources.Workspace, opts ...DependencyOption) (*ServiceDependencies, error) {
	w := wool.Get(ctx).In("NewServiceDependencies")
	opt := &DependencyOptions{
		SkipDependencyFor: make(map[string]bool),
		ExcludeService:    make(map[string]bool),
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

// Restrict restricts the dependencies to the services required by the service identified by unique.
//
// The restricted view keeps the dependency options and the service lookup of the service nodes
// retained in the sub graph: a service that the receiver resolves and that the sub graph keeps
// resolves identically here. Services dropped by the restriction are no longer resolvable and
// ServiceFromUnique reports them as not found. The receiver is left untouched.
//
// The lookup is preserved, not repaired: a service node that the receiver cannot resolve stays
// unresolvable. Such a node is what loadServiceGraph records for a dependency on a service
// absent from the workspace that is not declared kind external — the external kind is typed
// EXTERNAL and stays out of Services(), an undeclared one is typed SERVICE with nothing behind it.
//
// Workspace is carried over as-is and is NOT restricted, so it still lists modules and services
// that the restricted graph dropped.
func (d *ServiceDependencies) Restrict(_ context.Context, unique string) (*ServiceDependencies, error) {
	// B is required by A if A <- ... <- B
	sub, err := d.graph.SubGraphTo(unique)
	if err != nil {
		return nil, fmt.Errorf("cannot restrict to <%s>: %w", unique, err)
	}
	return d.withGraph(sub), nil
}

// ForStage restricts the dependencies to the edges that constrain the given
// stage. Callers select the stage they are about to execute instead of
// threading booleans through the call chain, so a build-only edge never orders
// a run and a consumed endpoint never orders a build.
func (d *ServiceDependencies) ForStage(stage resources.Stage) (*ServiceDependencies, error) {
	g, err := d.graph.ForStage(stage)
	if err != nil {
		return nil, err
	}
	return d.withGraph(g), nil
}

// StageOrder is the execution order for one stage of a phase.
type StageOrder struct {
	Stage    resources.Stage
	Services []Service
}

// OrderFor returns what a caller must do, in order, to carry out an operation
// on a service: one entry per stage the phase decomposes into.
//
// This is the API build/run/test/deploy callers want. `test` and `deploy` build
// and then run, so they return a build order followed by a run order — two
// sortable graphs rather than one merged graph that would report a cycle
// between a build-time and a run-time edge pointing opposite ways.
func (d *ServiceDependencies) OrderFor(ctx context.Context, phase resources.Phase, unique string) ([]StageOrder, error) {
	w := wool.Get(ctx).In("architecture.OrderFor")
	if err := phase.Validate(); err != nil {
		return nil, w.Wrap(err)
	}
	var out []StageOrder
	for _, stage := range phase.Stages() {
		restricted, err := d.ForStage(stage)
		if err != nil {
			return nil, w.Wrap(err)
		}
		order, err := restricted.OrderTo(ctx, unique)
		if err != nil {
			return nil, w.Wrapf(err, "cannot order %s stage of %s", stage, phase)
		}
		out = append(out, StageOrder{Stage: stage, Services: order})
	}
	return out, nil
}

// withGraph carries the service lookup and options onto a derived graph,
// dropping the services the derived graph no longer holds so a removed node
// cannot be resolved through the restricted view.
func (d *ServiceDependencies) withGraph(g *DAG) *ServiceDependencies {
	services := make(map[string]*resources.Service, len(d.uniqueToService))
	for unique, svc := range d.uniqueToService {
		if g.HasNode(unique) {
			services[unique] = svc
		}
	}
	return &ServiceDependencies{
		Workspace:       d.Workspace,
		graph:           g,
		uniqueToService: services,
		options:         d.options.clone(),
	}
}

// VerifyAcyclic fails when the dependency graph of a stage deadlocks, naming
// the stage and the cycle. Cycles are a per-stage property: an API consuming a
// database at runtime while the database bootstrap consumes the API schema at
// build time is a cycle in neither stage, even though the untyped union of both
// edges is one.
func (d *ServiceDependencies) VerifyAcyclic(ctx context.Context) error {
	w := wool.Get(ctx).In("architecture.VerifyAcyclic")
	for _, stage := range resources.Stages() {
		g, err := d.graph.ForStage(stage)
		if err != nil {
			return w.Wrap(err)
		}
		if cycle := g.Cycle(); cycle != nil {
			return w.NewError("%s dependencies have a cycle: %s", stage, strings.Join(cycle, " -> "))
		}
	}
	return nil
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
			if _, excluded := d.options.ExcludeService[identity.Unique()]; excluded {
				continue
			}
			d.uniqueToService[identity.Unique()] = svc
			graph.AddNode(identity.Unique()).WithType(resources.SERVICE)
			if _, skipDependency := d.options.SkipDependencyFor[identity.Unique()]; skipDependency {
				continue
			}
			for _, dep := range svc.ServiceDependencies {
				if _, excluded := d.options.ExcludeService[dep.Unique()]; excluded {
					continue
				}
				// An external capability is not a workspace service: typing it
				// SERVICE put it in Services() while ServiceFromUnique could
				// never resolve it, so callers iterating Services() hit a
				// not-found on an entry the same object had just handed them.
				// The edge is still recorded, so the declaration stays visible.
				nodeType := resources.SERVICE
				if dep.Kind == resources.DependencyKindExternal {
					nodeType = resources.EXTERNAL
				}
				graph.AddNode(dep.Unique()).WithType(nodeType)
				graph.AddKindedEdge(dep.Unique(), identity.Unique(), dep.Kind)
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

// Graph returns the dependency graph as a generic graph.Graph.
func (d *ServiceDependencies) Graph() *graph.Graph {
	name := "services"
	if d.Workspace != nil {
		name = d.Workspace.Name + "-services"
	}
	return ToGraph(d.graph, name)
}
