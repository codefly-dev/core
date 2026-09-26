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
	// Reference is the <module>/<service>/<endpoint> the value names.
	Reference string
	// Producer is the <module>/<service> the reference names, empty when the
	// reference is malformed.
	Producer string
	// Reason says why the reference cannot resolve.
	Reason string
}

func (r UnresolvedReference) String() string {
	producer := r.Producer
	if producer == "" {
		producer = "-"
	}
	return fmt.Sprintf("%s: %s/%s = ${endpoint:%s} (producer %s): %s", r.Consumer, r.Group, r.Key, r.Reference, producer, r.Reason)
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
// plan being checked, and false when the plan has no such service.
type ProducerLookup func(unique string) (*resources.Service, bool)

// CheckEndpointReferences validates, before anything is built or started,
// every ${endpoint:…} reference in the workspace configurations each consumer
// declares: the reference must be well formed, name a producer the plan
// contains, and name an endpoint that producer's manifest declares. It returns
// an *UnresolvedReferencesError listing all of them, or nil.
//
// provided is what the environment provides (ReadWorkspaceConfigurations); a
// group appears once per contributing information. excluded names the groups a
// profile excludes, which the consumer never receives. A group the environment
// does not provide at all is not reported here: resolving it fails on its own,
// naming the group. Only the groups a consumer declares are checked: the
// composition root's groups injected into every service never bind one service
// to another, and a value there that a service cannot resolve is not for it.
//
// This is the plan-time half of a contract whose run-time half is
// resources.InterpolateConfigurationEndpoints, which fails on the same
// references when a value is resolved: a configuration error is reported as
// early as possible, and never omitted.
func CheckEndpointReferences(provided []*basev0.ConfigurationInformation, consumers []*resources.Service, excluded map[string]bool, producer ProducerLookup) error {
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
		unique := resources.WithUnique(consumer).Unique()
		for _, group := range consumer.WorkspaceConfigurationDependencies {
			if excluded[group] {
				continue
			}
			for _, info := range byGroup[group] {
				for _, value := range info.GetConfigurationValues() {
					for _, reference := range resources.EndpointReferences(value.GetValue()) {
						problem := checkEndpointReference(reference, producer)
						if problem == nil {
							continue
						}
						problem.Consumer, problem.Group, problem.Key = unique, group, value.GetKey()
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

func checkEndpointReference(reference string, producer ProducerLookup) *UnresolvedReference {
	out := &UnresolvedReference{Reference: reference}
	info, err := resources.ParseEndpoint(reference)
	if err != nil {
		out.Reason = fmt.Sprintf("malformed reference: %v", err)
		return out
	}
	if info.Module == "" || info.Service == "" || (info.Name == "" && info.API == "") {
		out.Reason = "malformed reference: it must name <module>/<service>/<endpoint>"
		return out
	}
	out.Producer = info.Module + "/" + info.Service
	service, ok := producer(out.Producer)
	if !ok || service == nil {
		out.Reason = "the producer is not a service of this plan"
		return out
	}
	for _, endpoint := range service.Endpoints {
		if endpoint == nil {
			continue
		}
		if declaresReferencedEndpoint(endpoint, info) {
			return nil
		}
	}
	declared := make([]string, 0, len(service.Endpoints))
	for _, endpoint := range service.Endpoints {
		if endpoint != nil {
			declared = append(declared, endpoint.Name)
		}
	}
	out.Reason = fmt.Sprintf("the producer declares no such endpoint (declared: %s)", strings.Join(declared, ", "))
	return out
}

// declaresReferencedEndpoint mirrors how a reference matches a mapping when it
// is resolved (resources.InterpolateEndpoints): by API when the reference names
// one, and by name, or by an API spelled as the name, otherwise.
func declaresReferencedEndpoint(endpoint *resources.Endpoint, info *resources.EndpointInformation) bool {
	if info.API != "" && endpoint.API != info.API {
		return false
	}
	if info.Name == "" {
		return endpoint.API == info.API
	}
	return endpoint.Name == info.Name || endpoint.API == info.Name
}
