package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/standards"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func NewNativeNetworkAccess() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{
		Kind: NetworkAccessNative,
	}
}

func NewContainerNetworkAccess() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{
		Kind: NetworkAccessContainer,
	}
}

func NewPublicNetworkAccess() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{
		Kind: NetworkAccessPublic,
	}
}

type NetworkInstance struct {
	Port     uint16
	Hostname string
	Host     string
	Address  string
}

func DefaultNetworkInstance(api string) *NetworkInstance {
	instance := &NetworkInstance{
		Port:     standards.Port(api),
		Hostname: "localhost",
		Host:     fmt.Sprintf("localhost:%d", standards.Port(api)),
		Address:  fmt.Sprintf("localhost:%d", standards.Port(api)),
	}
	if api == standards.REST || api == standards.HTTP {
		instance.Address = fmt.Sprintf("http://localhost:%d", standards.Port(api))
	}
	return instance
}

func NewNetworkInstance(hostname string, port uint16) *basev0.NetworkInstance {
	return &basev0.NetworkInstance{
		Hostname: hostname,
		Host:     fmt.Sprintf("%s:%d", hostname, port),
		Port:     uint32(port),
		Address:  fmt.Sprintf("%s:%d", hostname, port),
	}
}
func NewHTTPNetworkInstance(hostname string, port uint16, secured bool) *basev0.NetworkInstance {
	instance := &basev0.NetworkInstance{
		Hostname: hostname,
		Port:     uint32(port),
		Host:     fmt.Sprintf("%s:%d", hostname, port),
	}
	if secured {
		instance.Address = fmt.Sprintf("https://%s:%d", hostname, port)
	} else {
		instance.Address = fmt.Sprintf("http://%s:%d", hostname, port)
	}
	return instance
}

// endpointMatches compares two endpoints by identity. Nil-safe: these protos come
// from another process, so a nil endpoint (at any level) must never panic — it just
// doesn't match. (An agent must never panic on the shape of its inputs.)
func endpointMatches(a, b *basev0.Endpoint) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Module == b.Module && a.Service == b.Service && a.Api == b.Api && a.Name == b.Name
}

// accessKindMatches reports whether an instance's access kind equals the requested
// one, tolerating nil at every level (nil instance / nil Access / nil networkAccess).
func accessKindMatches(instance *basev0.NetworkInstance, networkAccess *basev0.NetworkAccess) bool {
	if instance == nil || instance.Access == nil || networkAccess == nil {
		return false
	}
	return instance.Access.Kind == networkAccess.Kind
}

func FilterNetworkInstance(_ context.Context, instances []*basev0.NetworkInstance, networkAccess *basev0.NetworkAccess) *basev0.NetworkInstance {
	for _, instance := range instances {
		if accessKindMatches(instance, networkAccess) {
			return instance
		}
	}
	return nil
}

func FindNetworkInstanceInNetworkMappings(ctx context.Context, mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint, networkAccess *basev0.NetworkAccess) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("FindNetworkInstanceInNetworkMappings")
	if endpoint == nil {
		return nil, w.NewError("can't find network instance for a nil endpoint")
	}
	var matchedButNoAccess bool
	for _, mapping := range mappings {
		if mapping == nil || !endpointMatches(mapping.Endpoint, endpoint) {
			continue
		}
		matchedButNoAccess = true
		for _, instance := range mapping.Instances {
			if accessKindMatches(instance, networkAccess) {
				return instance, nil
			}
		}
	}
	// Diagnostic: show WHAT was available so a "no network instance" failure is
	// debuggable — which endpoints the mappings DID carry, and (if the endpoint
	// matched but no instance did) which access kinds were present vs requested.
	available := make([]string, 0, len(mappings))
	for _, m := range mappings {
		if m != nil && m.Endpoint != nil {
			available = append(available, EndpointFromProto(m.Endpoint).Unique())
		}
	}
	wanted := "none"
	if networkAccess != nil {
		wanted = networkAccess.Kind
	}
	if matchedButNoAccess {
		return nil, w.NewError("no network instance for endpoint %s: endpoint matched but no instance for access=%s; available mappings: %v",
			EndpointFromProto(endpoint).Unique(), wanted, available)
	}
	return nil, w.NewError("no network instance for endpoint %s (access=%s): endpoint not in the %d available mappings: %v",
		EndpointFromProto(endpoint).Unique(), wanted, len(available), available)
}

func FindNetworkMapping(ctx context.Context, mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint) (*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("FindNetworkMapping")
	if endpoint == nil {
		return nil, w.NewError("can't find network instance for a nil endpoint")
	}
	for _, mapping := range mappings {
		if mapping == nil || !endpointMatches(mapping.Endpoint, endpoint) {
			continue
		}
		return mapping, nil
	}
	return nil, w.NewError("no network mapping for endpoint: %s", EndpointFromProto(endpoint).Unique())
}

// ResolveDependencyNetworkMappings hands a consumer the addresses of what its
// dependencies consume, judged: the provenance is the composition that carries
// the consumer (the module of the service, job, runnable or application these
// mappings are for), and the verdict on each edge is the one every reader
// consults — the edge by the provenance of its two ends, then each endpoint by
// visibility — so what this returns is what gets injected as the consumer's
// environment, and therefore what its SDK exposes, and the permitted set and
// the exposed set cannot drift. Every HOLDER of a consumer's dependency
// addresses resolves through here — the run and the deploy the CLI drives,
// with the workspace in hand; the builder agent, with request provenance or,
// for older callers, the workspace above its service; the SDK session, with
// the one that composed its module. There is no selecting from what another holder judged: a holder
// that can find no composition refuses the addresses as unjudged
// (ErrUnjudgedProvenance) rather than wiring them.
func ResolveDependencyNetworkMappings(provenance Provenance, consumerModule string, dependencies []*ServiceDependency, mappings []*basev0.NetworkMapping) ([]*basev0.NetworkMapping, error) {
	endpoints := make([]*basev0.Endpoint, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			return nil, fmt.Errorf("dependency network mapping is missing its endpoint")
		}
		endpoints = append(endpoints, mapping.Endpoint)
	}

	selected := make(map[string]struct{})
	for _, dependency := range dependencies {
		// A network mapping is a runtime address. A dependency that does not
		// constrain the run phase has none to consume: injecting the producer's
		// connection anyway hands a consumer credentials for a service it only
		// builds against, and does so only when some *other* service happens to
		// be running it — a value that silently appears and disappears.
		if !dependency.Kind.Participates(StageRun) {
			continue
		}
		selectedEndpoints, err := ConsumedDependencyEndpoints(provenance, consumerModule, dependency, endpoints)
		if err != nil {
			return nil, err
		}
		for _, endpoint := range selectedEndpoints {
			selected[EndpointDestination(endpoint)] = struct{}{}
		}
	}
	var resolved []*basev0.NetworkMapping
	for _, mapping := range mappings {
		if _, ok := selected[EndpointDestination(mapping.Endpoint)]; ok {
			resolved = append(resolved, mapping)
		}
	}
	return resolved, nil
}

func MakeManyNetworkMappingSummary(mappings []*basev0.NetworkMapping) string {
	var results []string
	for _, mapping := range mappings {
		results = append(results, MakeNetworkMappingSummary(mapping))
	}
	return strings.Join(results, ", ")
}

func MakeNetworkMappingSummary(mapping *basev0.NetworkMapping) string {
	var summaries []string
	for _, instance := range mapping.Instances {
		summaries = append(summaries, NetworkInstanceSummary(instance))
	}
	return fmt.Sprintf("%s:%s", EndpointDestination(mapping.Endpoint), strings.Join(summaries, ", "))
}

func NetworkInstanceSummary(value *basev0.NetworkInstance) string {
	return fmt.Sprintf("%s:%d (%s)", value.Hostname, value.Port, value.Access.Kind)
}

func networkMappingHash(n *basev0.NetworkMapping) string {
	return HashString(n.String())
}

func NetworkMappingHash(networkMappings ...*basev0.NetworkMapping) string {
	hasher := NewHasher()
	for _, networkMapping := range networkMappings {
		hasher.Add(networkMappingHash(networkMapping))
	}
	return hasher.Hash()
}

func LocalizeNetworkMapping(mappings []*basev0.NetworkMapping, hostname string) []*basev0.NetworkMapping {
	var results []*basev0.NetworkMapping
	for _, mapping := range mappings {
		var instances []*basev0.NetworkInstance
		for _, instance := range mapping.Instances {
			instances = append(instances, &basev0.NetworkInstance{
				Hostname: hostname,
				Host:     fmt.Sprintf("%s:%d", hostname, instance.Port),
				Port:     instance.Port,
				Address:  fmt.Sprintf("%s:%d", hostname, instance.Port),
				Access:   instance.Access,
			})
		}
		results = append(results, &basev0.NetworkMapping{
			Endpoint:  mapping.Endpoint,
			Instances: instances,
		})
	}
	return results

}

// SplitPublicNetworkMappings separates the mappings addressed from outside the
// workspace — an exposed endpoint, or an external one, which lives there — from
// the rest. It is an ADDRESSING split, keyed on exposure and location; a
// visibility says who may call and never where an address is.
func SplitPublicNetworkMappings(ctx context.Context, mappings []*basev0.NetworkMapping) ([]*basev0.NetworkMapping, []*basev0.NetworkMapping, error) {
	var public []*basev0.NetworkMapping
	var nonPublic []*basev0.NetworkMapping
	for _, mapping := range mappings {
		// A mapping is handed out by its declaration, so the declaration is
		// judged whole here as on every proto ingress.
		if err := ValidateEndpointDeclaration(EndpointDeclarationOf(mapping.GetEndpoint())); err != nil {
			return nil, nil, err
		}
		if IsExposedEndpoint(mapping.Endpoint) || IsExternalEndpoint(mapping.Endpoint) {
			public = append(public, mapping)
		} else {
			nonPublic = append(nonPublic, mapping)
		}
	}
	return public, nonPublic, nil
}
