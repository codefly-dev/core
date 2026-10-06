package resources

import (
	"context"
	"slices"

	"github.com/codefly-dev/core/wool"
)

// AllowList is the derived allow-list of one declared endpoint. A list applies
// to an internal endpoint only — public is open and private is closed, and
// neither carries one — so Modules is nil unless Visibility is internal.
type AllowList struct {
	// Visibility is the endpoint's reach, as the composition exports it.
	Visibility Visibility
	// Modules is the derived list, sorted, each module once: the modules
	// whose services declare a run-stage dependency that reaches the
	// endpoint. Empty for an internal endpoint nobody asks for; nil for a
	// reach that carries no list.
	Modules []string
}

// DerivedAllowModules is the allow-list of every endpoint a composition
// declares, DERIVED by Workspace.DeriveAllowModules. It is the one
// implementation of the join the owner ruled on — "the ask lives with the
// asker, never with the target" — and the composition that renders an
// environment calls it for the allow-list it writes and the platform
// enforces, instead of reading one off the endpoint, where none may be written
// (ValidateEndpointDeclaration).
//
// What it is and is not:
//
//   - It is exactly the set of asks. A module reaching an endpoint it never
//     declared a dependency on produces no entry, and an entry cannot survive
//     the dependency being removed — a stronger property than a hand-written
//     list, which can go stale in either direction.
//   - It is REACHABILITY, a deployment-time fact for mesh policy and
//     NetworkPolicy, and never authorization. Whether a particular call is
//     permitted — on whose behalf, for which installation, within which
//     scopes — is per-call and runtime: WorkContextV1 carries the audience,
//     the authority scopes and the seal, and neither substitutes for the
//     other.
//   - Only an edge that REACHES the endpoint derives an entry
//     (DependencyKind.ReachesEndpoints): a build or schema dependency reads
//     the producer's contract and never calls it, a completion prerequisite
//     waits for the producer to finish and consumes no endpoint — its stage
//     participation is not consumption — and an external capability has no
//     producer here.
//   - The producer's own module appears when one of its services declares
//     the dependency, like any other ask. What a module may reach inside
//     itself never depends on this list.
//   - The join is over the COMPOSITION, never over bare services: each edge
//     is judged by the provenance of its two ends (Workspace.JudgeCompositionEdge)
//     before the export boundary judges the endpoint, so a solution's direct
//     route to a module's endpoints is refused here (ErrSolutionReachesThroughHost),
//     and so is a dependency the export boundary refuses — a cross-module ask
//     for a private endpoint — the same refusal the static validation makes
//     (ConsumedDependencyEndpoints). Nothing is derived for a composition that
//     does not validate.
type DerivedAllowModules struct {
	byEndpoint map[string]AllowList
}

// DeriveAllowModules computes the derived allow-list of every endpoint this
// composition declares, from the declared service dependencies of every
// service it carries, each module's endpoints as its interface exports them.
// A dependency naming a producer the composition does not carry contributes
// nothing, as it reaches no endpoint here; whether such a producer should
// exist is the workspace's own validation's question.
func (workspace *Workspace) DeriveAllowModules(ctx context.Context) (DerivedAllowModules, error) {
	w := wool.Get(ctx).In("Workspace::DeriveAllowModules", wool.NameField(workspace.Name))
	modules, err := workspace.LoadModules(ctx)
	if err != nil {
		return DerivedAllowModules{}, w.Wrap(err)
	}
	type consumer struct {
		module  *Module
		service *Service
	}
	derived := DerivedAllowModules{byEndpoint: map[string]AllowList{}}
	producers := map[string]*Service{}
	var consumers []consumer
	for _, mod := range modules {
		services, err := mod.LoadServices(ctx)
		if err != nil {
			return DerivedAllowModules{}, w.Wrap(err)
		}
		for _, service := range services {
			identity, err := service.Identity()
			if err != nil {
				return DerivedAllowModules{}, w.Wrap(err)
			}
			producers[identity.Unique()] = service
			consumers = append(consumers, consumer{module: mod, service: service})
			for _, endpoint := range service.Endpoints {
				if endpoint == nil {
					continue
				}
				// Judged whole here as the loader judges it, so a service
				// built in memory is held to the same declaration.
				if err := ValidateEndpointDeclaration(endpoint.Declaration()); err != nil {
					return DerivedAllowModules{}, w.Wrap(err)
				}
				key := allowModulesKey(identity.Module, service.Name, endpoint.Name)
				if _, declared := derived.byEndpoint[key]; declared {
					return DerivedAllowModules{}, w.NewError("endpoint %s is declared twice", key)
				}
				list := AllowList{Visibility: endpoint.Visibility}
				if endpoint.Visibility == VisibilityInternal {
					list.Modules = []string{}
				}
				derived.byEndpoint[key] = list
			}
		}
	}
	for _, c := range consumers {
		identity, err := c.service.Identity()
		if err != nil {
			return DerivedAllowModules{}, w.Wrap(err)
		}
		for _, dependency := range c.service.ServiceDependencies {
			if dependency == nil || !dependency.Kind.ReachesEndpoints() {
				continue
			}
			producerModule := dependency.Module
			if producerModule == "" {
				producerModule = identity.Module
			}
			producer, carried := producers[dependency.Unique()]
			if !carried {
				continue
			}
			if err := workspace.JudgeCompositionEdge(identity.Module, producerModule, dependency); err != nil {
				return DerivedAllowModules{}, w.Wrap(err)
			}
			endpoints, err := producer.DependencyEndpoints()
			if err != nil {
				return DerivedAllowModules{}, w.Wrap(err)
			}
			consumed, err := ConsumedDependencyEndpoints(identity.Module, dependency, endpoints)
			if err != nil {
				return DerivedAllowModules{}, w.Wrapf(err, "service %s", identity.Unique())
			}
			for _, endpoint := range consumed {
				key := allowModulesKey(endpoint.GetModule(), endpoint.GetService(), endpoint.GetName())
				list := derived.byEndpoint[key]
				if list.Visibility != VisibilityInternal || slices.Contains(list.Modules, identity.Module) {
					continue
				}
				list.Modules = append(list.Modules, identity.Module)
				derived.byEndpoint[key] = list
			}
		}
	}
	for key, list := range derived.byEndpoint {
		slices.Sort(list.Modules)
		derived.byEndpoint[key] = list
	}
	return derived, nil
}

// Of returns the derived allow-list of the endpoint <module>/<service>/<name>,
// and whether the composition declares that endpoint at all: an internal
// endpoint nobody asks for has an empty list, a public or private one has
// none, and an endpoint the composition does not declare is a different fact
// from both.
func (derived DerivedAllowModules) Of(module, service, name string) (AllowList, bool) {
	list, declared := derived.byEndpoint[allowModulesKey(module, service, name)]
	if list.Modules != nil {
		list.Modules = slices.Clone(list.Modules)
	}
	return list, declared
}

// allowModulesKey identifies an endpoint by name alone — the API is a property
// of the endpoint, not part of what a dependency asks for.
func allowModulesKey(module, service, name string) string {
	return ServiceUnique(module, service) + "/" + name
}
