package resources

import (
	"errors"
	"fmt"
	"strings"
)

// ErrAmbiguousEndpointReference is returned when a reference names no endpoint
// exactly and more than one endpoint the consumer may reach satisfies it. The
// reference is not resolved to one of them: which one would be decided by
// declaration order, and a value addressing an endpoint nobody chose is worse
// than a composition that is told to say which it means.
var ErrAmbiguousEndpointReference = errors.New("endpoint reference satisfied by more than one endpoint the consumer may reach")

// ErrNoSuchEndpoint is returned when the producer declares no endpoint the
// reference could name.
var ErrNoSuchEndpoint = errors.New("the producer declares no such endpoint")

// ErrConsumerNotIdentified is returned when a reference is resolved with no
// consumer module to judge the producer's export boundary against. A reference
// is an edge into the consumer's module; without the module there is nothing to
// decide visibility for, and this package does not resolve on the assumption
// that whoever asked may reach whatever they named.
var ErrConsumerNotIdentified = errors.New("the consumer of an endpoint reference is not identified: a reference is resolved for a module or refused")

// ErrNoDeclaredEndpoints is returned when a resolution has no way to read the
// producer's declared endpoints. The manifest is the only authority on which
// endpoint a token names and who may reach it; published mappings establish
// neither the complete declared set nor a declaration that was never published,
// so they are not a substitute.
var ErrNoDeclaredEndpoints = errors.New("the producer's declared endpoints are not available to this resolution")

// ErrUnknownProducer is returned when the reference names a producer the
// workspace does not declare. It is a composition fault, distinct from a
// producer that exists but is not part of this run.
var ErrUnknownProducer = errors.New("the reference names a producer this workspace does not declare")

// ErrEndpointNotReachable is wrapped around every refusal the export boundary
// makes, so a caller can tell "this consumer may not reach the endpoint it
// named" from every other failure. In a run-wide interpolation it is the one
// refusal that is a legitimate per-consumer omission: the value was not for
// that consumer.
var ErrEndpointNotReachable = errors.New("the consumer may not reach the endpoint the reference names")

// ErrInvalidEndpointDeclaration is returned when the producer's declaration of
// the endpoint cannot be judged at all — an unsupported visibility value, for
// one. It is a fault of the declaration, never a per-consumer omission: a
// run-wide interpolation that dropped the value would hide a typo in a
// manifest behind a key silently missing from every consumer.
var ErrInvalidEndpointDeclaration = errors.New("the endpoint's declaration is invalid")

// ErrEndpointAPIMismatch is returned when a reference qualifies the endpoint it
// names with an API the named endpoint does not serve. The qualifier is a
// statement about the endpoint named, not a request for any sibling serving
// that API.
var ErrEndpointAPIMismatch = errors.New("the reference qualifies the endpoint it names with an API that endpoint does not serve")

// EndpointSelection is the one endpoint a reference names for one consumer.
//
// It exists so that the plan-time check and the resolution cannot disagree.
// Before this, the check returned on the first manifest endpoint matching the
// reference while the resolution returned the first published MAPPING matching
// it — two different questions answered by two different scans, so a reference
// could be judged against one endpoint and resolved to another. Every caller
// now asks this, and the resolution binds the endpoint that was judged.
type EndpointSelection struct {
	// Endpoint is the endpoint the reference names, as the producer declares it.
	Endpoint *Endpoint
	// ExactName says the reference named the endpoint by its own name rather
	// than by its API.
	ExactName bool
}

// SelectEndpointForReference answers which of a producer's declared endpoints a
// reference names, for a consumer in consumerModule.
//
// The rules, in order, and each one is a decision the resolution used to make by
// accident:
//
//  1. An endpoint whose NAME is the reference's token is the one named, and it
//     is found BEFORE any API matching: a producer declaring `grpc` (api grpc)
//     and `admin` (api grpc) has two endpoints satisfying ${…/grpc} under the
//     API rule, but the one actually called `grpc` is what the reference says.
//     Nothing else may answer it — not an API sibling bound earlier, not an API
//     sibling that happens to have an address for this consumer's access when
//     the named one does not, and not an API sibling when the reference
//     qualifies the name with an API the named endpoint does not serve (that is
//     a refusal, ErrEndpointAPIMismatch, not a search for a sibling).
//  2. An exact name the consumer may NOT reach is a refusal, never a fallback.
//     Resolving to a permitted API sibling instead would answer a reference for
//     an endpoint the consumer was refused with a different endpoint's address,
//     which is both wrong and silent. The composition is told to name an
//     endpoint it may reach, or the producer to export the one it means.
//  3. With no exact name, the candidates are the API matches the consumer may
//     reach. Exactly one is the answer; several are ambiguous and refused;
//     none is a refusal carrying the visibility reason when an endpoint existed
//     but was not reachable, so a consumer is told it may not reach the endpoint
//     rather than that the producer has none.
//
// consumerModule must be given: a reference with no consumer is refused with
// ErrConsumerNotIdentified. declared is the producer's manifest, scoped here to
// the producer the reference names so that no caller has to remember to do it.
func SelectEndpointForReference(consumerModule string, info *EndpointInformation, declared []*Endpoint) (*EndpointSelection, error) {
	if info == nil {
		return nil, fmt.Errorf("%w: no reference", ErrNoSuchEndpoint)
	}
	if consumerModule == "" {
		return nil, ErrConsumerNotIdentified
	}
	scoped := make([]*Endpoint, 0, len(declared))
	for _, endpoint := range declared {
		if endpoint == nil {
			continue
		}
		// Module and service are compared when the endpoint carries them: a
		// candidate from another producer that happens to share the token is
		// not a candidate at all. The matcher deliberately ignores them because
		// its callers identify the producer first, so a caller that forgot to
		// would let one producer's endpoint answer another producer's reference.
		// Scoping it here means no caller has to remember.
		if endpoint.Module != "" && info.Module != "" && endpoint.Module != info.Module {
			continue
		}
		if endpoint.Service != "" && info.Service != "" && endpoint.Service != info.Service {
			continue
		}
		scoped = append(scoped, endpoint)
	}

	// (1) and (2): the endpoint the reference names, found by its name alone so
	// that an API qualifier can never erase it from consideration, then held to
	// the qualifier and to the export boundary.
	if info.Name != "" {
		for _, endpoint := range scoped {
			if endpoint.Name != info.Name {
				continue
			}
			if info.API != "" && endpoint.API != info.API {
				return nil, fmt.Errorf("%w: %s/%s serves %q, the reference asks for %q", ErrEndpointAPIMismatch, info.Service, endpoint.Name, endpoint.API, info.API)
			}
			if err := visibilityOf(consumerModule, info, endpoint); err != nil {
				return nil, err
			}
			return &EndpointSelection{Endpoint: endpoint, ExactName: true}, nil
		}
	}

	// (3): the API matches, which must come to exactly one.
	var reachable []*Endpoint
	var refusals []error
	matched := 0
	for _, endpoint := range scoped {
		if !endpointMatchesReferenceInfo(endpoint, info) {
			continue
		}
		matched++
		if err := visibilityOf(consumerModule, info, endpoint); err != nil {
			// Only a genuine denial narrows the candidates. A candidate whose
			// declaration cannot be judged is a fault of the manifest, and it
			// is reported before any sibling is selected or any omission is
			// allowed — otherwise a reachable sibling would hide it, and the
			// declaration order would decide whether it was ever seen.
			if !errors.Is(err, ErrEndpointNotReachable) {
				return nil, err
			}
			refusals = append(refusals, err)
			continue
		}
		reachable = append(reachable, endpoint)
	}
	if matched == 0 {
		return nil, fmt.Errorf("%w (declared: %s)", ErrNoSuchEndpoint, declaredNames(scoped))
	}
	switch {
	case len(reachable) == 1:
		return &EndpointSelection{Endpoint: reachable[0]}, nil
	case len(reachable) > 1:
		names := make([]string, 0, len(reachable))
		for _, endpoint := range reachable {
			names = append(names, endpoint.Name)
		}
		return nil, fmt.Errorf("%w: %s — name the one you mean", ErrAmbiguousEndpointReference, strings.Join(names, ", "))
	default:
		return nil, refusals[0]
	}
}

// visibilityOf judges the export boundary for one endpoint. A denial by a valid
// export policy is wrapped in ErrEndpointNotReachable, so a caller can recognise
// the one class of refusal that is about this consumer; a declaration that
// cannot be judged (ErrInvalidEndpointDeclaration) is passed through as the
// manifest fault it is.
func visibilityOf(consumerModule string, info *EndpointInformation, endpoint *Endpoint) error {
	module, service := endpoint.Module, endpoint.Service
	if module == "" {
		module = info.Module
	}
	if service == "" {
		service = info.Service
	}
	if err := ValidateEndpointVisibility(consumerModule, module, service, endpoint.Name, Visibility(endpoint.Visibility), endpoint.Location, endpoint.AllowModules); err != nil {
		if errors.Is(err, ErrInvalidEndpointDeclaration) {
			// Not a denial: the declaration cannot be judged. It must not read
			// as "this consumer may not reach it", which a run-wide
			// interpolation is entitled to drop.
			return err
		}
		return fmt.Errorf("%w: %w", ErrEndpointNotReachable, err)
	}
	return nil
}

func declaredNames(declared []*Endpoint) string {
	names := make([]string, 0, len(declared))
	for _, endpoint := range declared {
		if endpoint != nil {
			names = append(names, endpoint.Name)
		}
	}
	if len(names) == 0 {
		return "the producer declares no endpoints at all"
	}
	return strings.Join(names, ", ")
}

// DeclaredEndpoints returns a producer's declared endpoints by
// <module>/<service>. The second result says whether the workspace declares that
// producer at all: a producer that exists but declares no endpoints answers
// (nil, true), which is a different fact from (nil, false).
type DeclaredEndpoints func(unique string) ([]*Endpoint, bool)

// EndpointSelectionContext is what a resolution knows about its consumer, and
// both halves are required: the consumer module, for the export boundary, and
// the producers' manifests, for which endpoint a token names. A resolution with
// either half missing is refused — there is no honest answer a resolution can
// give about an endpoint without knowing who is asking and what was declared.
type EndpointSelectionContext struct {
	// ConsumerModule is the module receiving the address.
	ConsumerModule string
	// Declared returns a producer's manifest endpoints by <module>/<service>.
	Declared DeclaredEndpoints
}

// Complete reports why this context cannot resolve a reference, or nil.
func (selection EndpointSelectionContext) Complete() error {
	if selection.ConsumerModule == "" {
		return ErrConsumerNotIdentified
	}
	if selection.Declared == nil {
		return ErrNoDeclaredEndpoints
	}
	return nil
}

// DeclaredEndpointsOf answers DeclaredEndpoints from a workspace's services:
// the authoritative manifest, read the way the plan-time check reads it.
func DeclaredEndpointsOf(services []*Service) DeclaredEndpoints {
	byUnique := make(map[string][]*Endpoint, len(services))
	for _, service := range services {
		if service == nil {
			continue
		}
		identity, err := service.Identity()
		if err != nil || identity == nil {
			continue
		}
		byUnique[identity.Unique()] = service.Endpoints
	}
	return func(unique string) ([]*Endpoint, bool) {
		endpoints, ok := byUnique[unique]
		return endpoints, ok
	}
}

// WorseReferenceFailure returns the failure that must be reported when a value
// carries several: a composition or declaration fault over any omission, and a
// denial (ErrEndpointNotReachable) over a mere unavailability
// (errEndpointNotAvailable). The ranking is what lets each entry point apply its
// own omission policy to the one error it receives: the run-wide path may omit
// on either omission class, the strict path only on an unavailability for a
// producer outside the run — so a value that also carries a denial reports the
// denial, whichever reference came first.
func WorseReferenceFailure(a, b error) error {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case !ReferenceFailureIsAnOmission(a):
		return a
	case !ReferenceFailureIsAnOmission(b):
		return b
	case errors.Is(a, ErrEndpointNotReachable):
		return a
	case errors.Is(b, ErrEndpointNotReachable):
		return b
	default:
		return a
	}
}
