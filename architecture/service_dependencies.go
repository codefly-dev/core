package architecture

import (
	"context"
	"fmt"
	"maps"
	"slices"
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

	// referenceDependencies holds, per consumer unique, the runtime dependencies
	// its workspace configuration references amount to. See
	// ConfigurationReferenceDependencies.
	referenceDependencies map[string][]*resources.ServiceDependency

	// stage is the stage this view was restricted to, empty when it was not.
	// It is what keeps a restricted view self-consistent: the same predicate
	// that decided which edges order the stage decides which are judged.
	stage resources.Stage
}

type DependencyOptions struct {
	SkipDependencyFor map[string]bool
	ExcludeService    map[string]bool
	// ConfigurationProducers holds, per workspace configuration group, the
	// <module>/<service>/<endpoint> references it carries. See
	// WithConfigurationReferences.
	ConfigurationProducers map[string][]string
}

func (opt *DependencyOptions) clone() *DependencyOptions {
	out := &DependencyOptions{
		SkipDependencyFor: make(map[string]bool, len(opt.SkipDependencyFor)),
		ExcludeService:    make(map[string]bool, len(opt.ExcludeService)),
	}
	maps.Copy(out.SkipDependencyFor, opt.SkipDependencyFor)
	maps.Copy(out.ExcludeService, opt.ExcludeService)
	if opt.ConfigurationProducers != nil {
		out.ConfigurationProducers = make(map[string][]string, len(opt.ConfigurationProducers))
		for group, producers := range opt.ConfigurationProducers {
			out.ConfigurationProducers[group] = slices.Clone(producers)
		}
	}
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

// WithConfigurationReferences orders every service after the producers its
// workspace configurations reference. A workspace configuration is the
// composition root's: it names endpoints the consuming module cannot know (the
// host, by the composition's name for it), so the consumer declares the group,
// not the dependency, and the root's ${endpoint:…} reference is what binds the
// two. Each such reference to a service of the workspace becomes a runtime edge
// from the producer to every service declaring the group, so a run includes,
// starts and orders the producer as for a declared dependency. A service's own
// endpoint, an excluded producer, and an edge that would close a cycle are
// skipped; a declared dependency that already orders the run is never
// duplicated.
//
// A declaration that orders nothing — `kind: external`, which names the producer
// for configuration but never starts or waits for it — does not stand in for the
// reference: the root's reference binds a producer of this workspace, so the edge
// gains the runtime kind and the producer is started first. That OVERRIDES what
// the consumer's manifest says about the edge, which is documented the other way
// round (docs/dependency-kinds.md), so it is logged rather than done quietly: the
// author's `external` is what Codefly would have obeyed had the composition root
// not bound the two. The superseded kind is taken off the edge, so the pair does
// not end up carrying `external` and `runtime` at once.
//
// Ordering is not the whole of a runtime dependency: the consumer must also wait
// for the endpoint it will call. ConfigurationReferenceDependencies is the other
// half — the referenced endpoints as the dependencies they are, for whoever plans
// readiness — which is why references keep the endpoint they name and not only
// the producer.
//
// referencesByGroup maps a group to the <module>/<service>/<endpoint> references
// it carries (configurations.EndpointProducers). A bare <module>/<service> still
// orders the producer, and contributes no endpoint to wait for.
func WithConfigurationReferences(referencesByGroup map[string][]string) DependencyOption {
	return func(opt *DependencyOptions) error {
		opt.ConfigurationProducers = referencesByGroup
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
	restricted := d.withGraph(g)
	restricted.stage = stage
	return restricted, nil
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
	references := make(map[string][]*resources.ServiceDependency, len(d.referenceDependencies))
	for consumer, dependencies := range d.referenceDependencies {
		if g.HasNode(consumer) {
			references[consumer] = slices.Clone(dependencies)
		}
	}
	return &ServiceDependencies{
		Workspace:             d.Workspace,
		graph:                 g,
		uniqueToService:       services,
		options:               d.options.clone(),
		stage:                 d.stage,
		referenceDependencies: references,
	}
}

// ConfigurationReferenceDependencies returns the dependencies a consumer's
// workspace configuration references amount to: one runtime dependency per
// referenced endpoint of a producer this workspace provides, naming that
// endpoint. They are not in the consumer's manifest — the composition root wrote
// the reference, which is the whole point of the mechanism — so a caller that
// plans what the consumer must wait for reads them from here and plans them
// exactly like declared ones:
//
//	resources.PlanReadiness(append(svc.ServiceDependencies, dep.ConfigurationReferenceDependencies(unique)...), endpoints)
//
// Without this, a reference would order the producer and gate nothing, and the
// consumer would be started before the endpoint it is about to call can serve —
// the failure ordering was introduced to prevent. A reference whose producer is
// outside the workspace, or that names no endpoint the producer declares,
// contributes nothing: it orders nothing either, and the composition fault is
// reported by configurations.CheckEndpointReferences.
// The result is a deep copy, like DAG.EdgeKinds: a caller that plans readiness
// from it composes it with the consumer's own declarations, and must not be able
// to reach into the graph's state by editing what it was handed.
func (d *ServiceDependencies) ConfigurationReferenceDependencies(consumer string) []*resources.ServiceDependency {
	out := make([]*resources.ServiceDependency, 0, len(d.referenceDependencies[consumer]))
	for _, dependency := range d.referenceDependencies[consumer] {
		copied := *dependency
		copied.Endpoints = make([]*resources.EndpointReference, 0, len(dependency.Endpoints))
		for _, endpoint := range dependency.Endpoints {
			reference := *endpoint
			copied.Endpoints = append(copied.Endpoints, &reference)
		}
		out = append(out, &copied)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
				//
				// `kind: external` describes this edge — the declaring service
				// never builds, starts or waits for the producer — not the
				// producer itself. So it only types a node nothing else has
				// named yet: a node already typed SERVICE (the workspace loaded
				// it, or another service consumes it normally) is never
				// downgraded, and a later SERVICE typing always wins. The
				// result is independent of the order services are listed in.
				if dep.Kind == resources.DependencyKindExternal {
					if !graph.HasNode(dep.Unique()) {
						graph.AddNode(dep.Unique()).WithType(resources.EXTERNAL)
					}
				} else {
					graph.AddNode(dep.Unique()).WithType(resources.SERVICE)
				}
				graph.AddKindedEdge(dep.Unique(), identity.Unique(), dep.Kind)
			}
		}
	}
	d.addConfigurationReferenceEdges(ctx, graph)
	d.graph = graph
	return nil
}

// addConfigurationReferenceEdges applies WithConfigurationReferences once every
// service of the workspace is in the graph.
func (d *ServiceDependencies) addConfigurationReferenceEdges(ctx context.Context, graph *DAG) {
	if len(d.options.ConfigurationProducers) == 0 {
		return
	}
	w := wool.Get(ctx).In("addConfigurationReferenceEdges")
	consumers := slices.Sorted(maps.Keys(d.uniqueToService))
	for _, consumer := range consumers {
		if d.options.SkipDependencyFor[consumer] {
			continue
		}
		for _, group := range d.uniqueToService[consumer].WorkspaceConfigurationDependencies {
			for _, reference := range d.options.ConfigurationProducers[group] {
				producer, endpoint := d.resolveReference(reference)
				if producer == "" || producer == consumer || d.options.ExcludeService[producer] {
					continue
				}
				if _, inWorkspace := d.uniqueToService[producer]; !inWorkspace {
					continue
				}
				if graph.edgeConstrains(producer, consumer, resources.StageRun) {
					// A declared dependency already orders the run. It may name
					// other endpoints of the producer than the referenced one, so
					// the endpoint is still recorded: what the consumer waits for
					// is every endpoint it consumes, however it came to consume it.
					d.recordReferenceDependency(consumer, producer, endpoint)
					continue
				}
				if graph.reachesInStage(consumer, producer, resources.StageRun) {
					w.Debug("skipping a configuration reference that would close a cycle",
						wool.Field("consumer", consumer), wool.Field("producer", producer), wool.Field("group", group))
					// Nothing is recorded either: the consumer cannot wait for a
					// producer that is already waiting for it, and a readiness
					// requirement here would be the deadlock this edge was dropped
					// to avoid.
					continue
				}
				d.recordReferenceDependency(consumer, producer, endpoint)
				graph.AddKindedEdge(producer, consumer, resources.DependencyKindRuntime)
				// An `external` declaration of the same pair said this edge orders
				// nothing; the reference says it orders the run. Say so, at a level
				// an operator sees without asking for debug output: it is the one
				// case where a manifest's own `kind` is not what Codefly does.
				if slices.Contains(graph.EdgeKinds(producer, consumer), resources.DependencyKindExternal) {
					w.Info("a workspace configuration reference orders a producer its consumer declares external",
						wool.Field("consumer", consumer), wool.Field("producer", producer), wool.Field("group", group))
					graph.dropEdgeKind(producer, consumer, resources.DependencyKindExternal)
				}
			}
		}
	}
}

// resolveReference splits a <module>/<service>/<endpoint> reference into the
// producer it names and the producer's own name for the endpoint. The endpoint is
// resolved against what the producer declares, by the same rule the value's
// resolution applies, so what is recorded is an endpoint that exists rather than
// the token the reference spelled it with (`${endpoint:m/s/grpc}` may name an
// endpoint called `rpc` that serves the grpc API). It is empty when the reference
// names no endpoint, or none the producer declares.
func (d *ServiceDependencies) resolveReference(reference string) (string, string) {
	info, err := resources.ParseEndpoint(reference)
	if err != nil || info.Module == "" || info.Service == "" {
		return "", ""
	}
	producer := info.Module + "/" + info.Service
	service, ok := d.uniqueToService[producer]
	if !ok || (info.Name == "" && info.API == "") {
		return producer, ""
	}
	for _, endpoint := range service.Endpoints {
		if resources.EndpointMatchesReferenceInfo(endpoint, info) {
			return producer, endpoint.Name
		}
	}
	return producer, ""
}

// recordReferenceDependency records the referenced endpoint as the runtime
// dependency it is, merging endpoints of the same producer into one dependency so
// a consumer waits for each endpoint it references exactly once.
func (d *ServiceDependencies) recordReferenceDependency(consumer, producer, endpoint string) {
	if endpoint == "" {
		return
	}
	module, name, found := strings.Cut(producer, "/")
	if !found {
		return
	}
	if d.referenceDependencies == nil {
		d.referenceDependencies = make(map[string][]*resources.ServiceDependency)
	}
	for _, existing := range d.referenceDependencies[consumer] {
		if existing.Unique() != producer {
			continue
		}
		for _, reference := range existing.Endpoints {
			if reference.Name == endpoint {
				return
			}
		}
		existing.Endpoints = append(existing.Endpoints, &resources.EndpointReference{Name: endpoint})
		return
	}
	d.referenceDependencies[consumer] = append(d.referenceDependencies[consumer], &resources.ServiceDependency{
		Module:    module,
		Name:      name,
		Kind:      resources.DependencyKindRuntime,
		Endpoints: []*resources.EndpointReference{{Name: endpoint}},
	})
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
