package configurations

import (
	"fmt"
	"slices"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// UnresolvedReference is one ${endpoint:…} reference, in a workspace
// configuration a consumer declares, that no run of that consumer can resolve.
type UnresolvedReference struct {
	// Consumer is the <module>/<service> declaring the group.
	Consumer string
	// Group and Key locate the value carrying the reference.
	Group string
	Key   string
	// Position is which reference of the value this is, 1-based, in order of
	// appearance across the value and its template literals. It is how the
	// reference is named: a reference is text from a value, a value may be a
	// secret, and nothing of it — not even coordinates whose every token
	// validated — is carried here. The reason names what the manifest says.
	Position int
	// Reason says why the reference cannot resolve.
	Reason string
}

func (r UnresolvedReference) String() string {
	return fmt.Sprintf("%s: %s/%s, reference %d: %s", r.Consumer, r.Group, r.Key, r.Position, r.Reason)
}

// UnresolvedReferencesError lists every unresolvable reference of a plan at
// once, so a composition fixes them in one pass rather than one run at a time.
type UnresolvedReferencesError struct {
	References []UnresolvedReference
}

func (e *UnresolvedReferencesError) Error() string {
	lines := make([]string, 0, len(e.References)+1)
	lines = append(lines, fmt.Sprintf("%d workspace configuration reference(s) cannot resolve:", len(e.References)))
	for _, reference := range e.References {
		lines = append(lines, "  - "+reference.String())
	}
	return strings.Join(lines, "\n")
}

// ProducerLookup returns the service a <module>/<service> unique names in the
// WORKSPACE, and false when the workspace has no such service.
//
// The workspace, not the run: a producer a run excludes or simply does not start
// is still a real service, and a reference naming it is a fact about the
// composition that holds whatever subset is being run. Looking references up in
// a run-scoped graph instead would refuse every partial run of a workspace whose
// root binds producers outside it. What a run does contain is the resolve-time
// question (resources.WithRunProducers), asked once addresses exist.
type ProducerLookup func(unique string) (*resources.Service, bool)

// CheckEndpointReferences validates, before anything is built or started, every
// ${endpoint:…} reference in the workspace configurations each consumer
// declares: the reference must be well formed, name a producer of the workspace,
// name an endpoint that producer's manifest declares, and name one the
// consumer's module may receive. It returns an *UnresolvedReferencesError
// listing all of them, or nil.
//
// provided is what the environment provides (ReadWorkspaceConfigurations); a
// group appears once per contributing information. profile is the run profile
// whose ExcludeWorkspaceConfigurations the consumer never receives — the only
// place group exclusions come from, so a caller passes what it resolved rather
// than assembling a set the check cannot verify; a zero profile excludes
// nothing. A group the environment does not provide at all is not reported here:
// resolving it fails on its own, naming the group. Only the groups a consumer
// declares are checked: the composition root's groups injected into every
// service never bind one service to another, and a value there that a service
// cannot resolve is not for it.
//
// This is the COMPOSITION half of the contract, and it is the half that can be
// checked before anything exists: a typo, an endpoint a producer does not
// publish, an endpoint a module may not receive — faults of the workspace, true
// of every run of it. It deliberately says nothing about what a given run
// contains or about addresses, which do not exist yet; those are
// resources.InterpolateConfigurationEndpoints with WithRunProducers, which fails
// when a producer the run DOES contain was not handed to a consumer. Neither
// half subsumes the other, and a reference is silently dropped by neither.
func CheckEndpointReferences(provided []*basev0.ConfigurationInformation, consumers []*resources.Service, profile resources.RunProfile, producer ProducerLookup) error {
	excluded := make(map[string]bool, len(profile.ExcludeWorkspaceConfigurations))
	for _, group := range profile.ExcludeWorkspaceConfigurations {
		excluded[group] = true
	}
	byGroup := make(map[string][]*basev0.ConfigurationInformation)
	for _, info := range provided {
		byGroup[info.GetName()] = append(byGroup[info.GetName()], info)
	}
	var unresolved []UnresolvedReference
	seen := make(map[UnresolvedReference]bool)
	for _, consumer := range consumers {
		if consumer == nil {
			continue
		}
		identity, err := consumer.Identity()
		if err != nil {
			return err
		}
		unique := identity.Unique()
		for _, group := range consumer.WorkspaceConfigurationDependencies {
			if excluded[group] {
				continue
			}
			for _, info := range byGroup[group] {
				for _, value := range info.GetConfigurationValues() {
					// A marker the grammar cannot read is a fault of the value,
					// reported here with the references: the render refuses
					// it, and a plan is the earlier place to say so.
					if resources.ConfigurationValueHasMalformedEndpointMarker(value) {
						problem := UnresolvedReference{Consumer: unique, Group: group, Key: value.GetKey(),
							Reason: "malformed reference: the reserved ${endpoint: prefix is not a well-formed reference"}
						if !seen[problem] {
							seen[problem] = true
							unresolved = append(unresolved, problem)
						}
					}
					// Not EndpointReferences(value.GetValue()): a value whose
					// producer declared an assembly holds its text in the
					// template's literals, so a ${endpoint:…} written there is
					// invisible to .Value. This check exists to fail a plan
					// before anything starts, and missing a reference is exactly
					// the silent pass it was added to remove.
					for position, reference := range resources.ConfigurationValueEndpointReferences(value) {
						problem := checkEndpointReference(reference, identity.Module, producer)
						if problem == nil {
							continue
						}
						problem.Consumer, problem.Group, problem.Key, problem.Position = unique, group, value.GetKey(), position+1
						if !seen[*problem] {
							seen[*problem] = true
							unresolved = append(unresolved, *problem)
						}
					}
				}
			}
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	slices.SortStableFunc(unresolved, func(a, b UnresolvedReference) int {
		return strings.Compare(a.Consumer+"\x00"+a.Group+"\x00"+a.Key, b.Consumer+"\x00"+b.Group+"\x00"+b.Key)
	})
	return &UnresolvedReferencesError{References: unresolved}
}

func checkEndpointReference(reference string, consumerModule string, producer ProducerLookup) *UnresolvedReference {
	// A marker body is input until it validates as coordinates, and the value
	// it came from may be a secret: a reference that did not validate is
	// reported without its text.
	out := &UnresolvedReference{}
	info, err := resources.ParseEndpointReference(reference)
	if err != nil {
		out.Reason = "malformed reference: " + err.Error()
		return out
	}
	service, ok := producer(info.Module + "/" + info.Service)
	if !ok || service == nil {
		out.Reason = "the producer is not a service of this workspace"
		return out
	}
	// One selection, shared with the resolution. This used to scan the manifest
	// itself and return on the FIRST endpoint matching the reference, which is
	// a different question from the one the resolution answers — so a reference
	// could be JUDGED against one endpoint and RESOLVED to another, and a
	// composition this check passed could still address an endpoint the
	// consumer was never permitted. resources.SelectEndpointForReference is now
	// the only answer to "which endpoint does this reference name", and this
	// check reports its refusal verbatim.
	if _, err := resources.SelectEndpointForReference(consumerModule, info, service.Endpoints); err != nil {
		out.Reason = err.Error()
		return out
	}
	return nil
}
