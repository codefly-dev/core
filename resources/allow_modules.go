package resources

import (
	"fmt"
	"slices"
)

// DerivedAllowModules is the allow-list of every endpoint a composition
// declares, DERIVED: for each endpoint, the modules whose services declare a
// run-stage service dependency that consumes it. It is the one implementation
// of the join the owner ruled on — "the ask lives with the asker, never with
// the target" — and the composition that renders an environment calls it for
// the allow-list it writes and the platform enforces, instead of reading one
// off the endpoint, where none may be written (ValidateEndpointDeclaration).
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
//   - The producer's own module appears when one of its services declares
//     the dependency, like any other ask. What a module may reach inside
//     itself never depends on this list.
//   - Only run-stage edges reach anything: a build or schema dependency
//     reads the producer's contract and never calls it, and a completion
//     prerequisite consumes no endpoint.
//   - A dependency the export boundary refuses — a cross-module ask for a
//     private endpoint — is the same refusal the static validation makes
//     (ConsumedDependencyEndpoints): nothing is derived for a composition
//     that does not validate.
type DerivedAllowModules struct {
	byEndpoint map[string][]string
}

// DeriveAllowModules computes the derived allow-list of every endpoint the
// given services declare, from the declared service dependencies of every
// service given. The services are the composition: a dependency naming a
// producer outside the set contributes nothing, as it reaches no endpoint
// here, and the workspace's own validation is what reports a producer that
// does not exist.
func DeriveAllowModules(services []*Service) (DerivedAllowModules, error) {
	derived := DerivedAllowModules{byEndpoint: map[string][]string{}}
	producers := make(map[string]*Service, len(services))
	for _, service := range services {
		if service == nil {
			continue
		}
		identity, err := service.Identity()
		if err != nil {
			return DerivedAllowModules{}, err
		}
		producers[identity.Unique()] = service
		for _, endpoint := range service.Endpoints {
			if endpoint == nil {
				continue
			}
			// The whole declaration is judged here as the loader judges it, so a
			// service built in memory with an authored list is refused like one
			// read from disk: the wire model the join reads carries no list, and
			// the derivation never runs over a declaration the model refuses.
			declaration := endpoint.Declaration()
			declaration.Service, declaration.Name = service.Name, endpoint.Name
			if err := ValidateEndpointDeclaration(declaration); err != nil {
				return DerivedAllowModules{}, err
			}
			key := allowModulesKey(identity.Module, service.Name, endpoint.Name)
			if _, declared := derived.byEndpoint[key]; declared {
				return DerivedAllowModules{}, fmt.Errorf("endpoint %s is declared twice", key)
			}
			derived.byEndpoint[key] = nil
		}
	}
	for _, consumer := range services {
		if consumer == nil {
			continue
		}
		identity, err := consumer.Identity()
		if err != nil {
			return DerivedAllowModules{}, err
		}
		for _, dependency := range consumer.ServiceDependencies {
			if dependency == nil || !dependency.Participates(StageRun) {
				continue
			}
			producer, declared := producers[dependency.Unique()]
			if !declared {
				continue
			}
			endpoints, err := producer.DependencyEndpoints()
			if err != nil {
				return DerivedAllowModules{}, err
			}
			consumed, err := ConsumedDependencyEndpoints(identity.Module, dependency, endpoints)
			if err != nil {
				return DerivedAllowModules{}, fmt.Errorf("service %s: %w", identity.Unique(), err)
			}
			for _, endpoint := range consumed {
				key := allowModulesKey(endpoint.GetModule(), endpoint.GetService(), endpoint.GetName())
				if !slices.Contains(derived.byEndpoint[key], identity.Module) {
					derived.byEndpoint[key] = append(derived.byEndpoint[key], identity.Module)
				}
			}
		}
	}
	for key := range derived.byEndpoint {
		slices.Sort(derived.byEndpoint[key])
	}
	return derived, nil
}

// Of returns the derived allow-list of the endpoint <module>/<service>/<name>,
// sorted, and whether the composition declares that endpoint at all: an
// endpoint nobody asks for has an empty list, which is a different fact from
// an endpoint the composition does not declare.
func (derived DerivedAllowModules) Of(module, service, name string) ([]string, bool) {
	modules, declared := derived.byEndpoint[allowModulesKey(module, service, name)]
	return slices.Clone(modules), declared
}

// allowModulesKey identifies an endpoint by name alone — the API is a property
// of the endpoint, not part of what a dependency asks for.
func allowModulesKey(module, service, name string) string {
	return ServiceUnique(module, service) + "/" + name
}
