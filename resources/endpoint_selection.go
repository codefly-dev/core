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
//  1. An endpoint whose NAME is the reference's token wins. Core's matcher is
//     `name == token || api == token`, so a producer declaring `grpc` (api grpc)
//     and `admin` (api grpc) has two endpoints satisfying ${…/grpc}. The one
//     actually called `grpc` is what the reference says, and nothing else may
//     answer it — not an API sibling bound earlier, and not an API sibling that
//     happens to have an address for this consumer's access when the named one
//     does not.
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
// declared is the producer's manifest. Passing the endpoints of published
// mappings instead answers rules 1 and 3 and cannot answer rule 2 — a mapping
// does not carry visibility — which is why the resolution prefers the manifest
// when its caller supplies one.
func SelectEndpointForReference(consumerModule string, info *EndpointInformation, declared []*Endpoint) (*EndpointSelection, error) {
	if info == nil {
		return nil, fmt.Errorf("%w: no reference", ErrNoSuchEndpoint)
	}
	var candidates []*Endpoint
	for _, endpoint := range declared {
		if endpoint == nil {
			continue
		}
		// Module and service are compared when the endpoint carries them: a
		// candidate from another producer that happens to share the token is
		// not a candidate at all. EndpointMatchesReferenceInfo deliberately
		// ignores them because its callers identify the producer first, so a
		// caller that forgot to would let one producer's endpoint answer
		// another producer's reference. Scoping it here means no caller has to
		// remember.
		if endpoint.Module != "" && info.Module != "" && endpoint.Module != info.Module {
			continue
		}
		if endpoint.Service != "" && info.Service != "" && endpoint.Service != info.Service {
			continue
		}
		if !EndpointMatchesReferenceInfo(endpoint, info) {
			continue
		}
		candidates = append(candidates, endpoint)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w (declared: %s)", ErrNoSuchEndpoint, declaredNames(declared))
	}

	// (1) and (2): the endpoint the reference names, reachable or refused.
	for _, endpoint := range candidates {
		if info.Name == "" || endpoint.Name != info.Name {
			continue
		}
		if err := visibilityOf(consumerModule, info, endpoint); err != nil {
			return nil, err
		}
		return &EndpointSelection{Endpoint: endpoint, ExactName: true}, nil
	}

	// (3): the API matches, which must come to exactly one.
	var reachable []*Endpoint
	var refusals []error
	for _, endpoint := range candidates {
		if err := visibilityOf(consumerModule, info, endpoint); err != nil {
			refusals = append(refusals, err)
			continue
		}
		reachable = append(reachable, endpoint)
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

func visibilityOf(consumerModule string, info *EndpointInformation, endpoint *Endpoint) error {
	// An unidentified consumer is not judged. A caller that has not said which
	// module is receiving the address has not given this function the one input
	// the export boundary is a statement about, and refusing on an empty module
	// would make every private endpoint unreachable to every caller that cannot
	// name its consumer — a refusal with no evidence behind it. The callers that
	// CAN name it (the plan-time check, a render resolving for one service) pass
	// it and are judged.
	if consumerModule == "" {
		return nil
	}
	module, service := endpoint.Module, endpoint.Service
	if module == "" {
		module = info.Module
	}
	if service == "" {
		service = info.Service
	}
	return ValidateEndpointVisibility(consumerModule, module, service, endpoint.Name, Visibility(endpoint.Visibility), endpoint.AllowModules)
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

// EndpointSelectionContext is what a resolution knows about its consumer.
//
// A zero value selects from the endpoints the published mappings carry and
// judges no visibility, which is what a caller that has identified neither the
// consumer nor the producer's manifest can honestly ask for.
type EndpointSelectionContext struct {
	// ConsumerModule is the module receiving the address, for the export
	// boundary. Empty means unknown, and nothing is refused on visibility.
	ConsumerModule string
	// Declared returns a producer's manifest endpoints by <module>/<service>.
	// Nil falls back to the endpoints of the published mappings.
	Declared func(unique string) []*Endpoint
}

func (selection EndpointSelectionContext) declaredFor(unique string) []*Endpoint {
	if selection.Declared == nil {
		return nil
	}
	return selection.Declared(unique)
}
