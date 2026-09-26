package network

import (
	"context"
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

// DeployedEndpointPorts is the one allocation of in-cluster (Kubernetes)
// ports for a service's endpoints, keyed by endpoint name. The CLI's GitOps
// render calls it, and so does any other tool that has to agree with what the
// render emits — a module generator must never re-declare these by hand.
//
// module is the name the workspace composes the module under, which can differ
// from the module's own name: it is an input of the named-port hash.
//
// endpoints are the endpoints rendered in-cluster; the caller drops external
// endpoints that resolve to a public host, which never receive a cluster port.
//
// Allocation, which is pure and needs no runtime context:
//   - The conventional endpoint of each API — the endpoint named after its API,
//     or the only endpoint of that API — competes for standards.Port(api).
//     APIs earlier in standards.APIS() win a shared canonical port; a tie within
//     one priority breaks on resources.EndpointDestination.
//   - Every other endpoint gets ToNamedPort(ctx, "", module, service, name,
//     api, PortModeHost).
//   - Two endpoints resolving to the same port is an error, as is a nil
//     endpoint or an endpoint name appearing twice.
func DeployedEndpointPorts(ctx context.Context, module, service string, endpoints []*basev0.Endpoint) (map[string]uint16, error) {
	apiCounts := make(map[string]int)
	for _, endpoint := range endpoints {
		if endpoint == nil {
			return nil, fmt.Errorf("service %s/%s: cannot allocate a port for a nil endpoint", module, service)
		}
		apiCounts[endpoint.Api]++
	}
	apiPriority := make(map[string]int, len(standards.APIS()))
	for priority, api := range standards.APIS() {
		apiPriority[api] = priority
	}
	canonicalOwners := make(map[uint16]*basev0.Endpoint)
	for _, endpoint := range endpoints {
		if apiCounts[endpoint.Api] > 1 && endpoint.Name != endpoint.Api {
			continue
		}
		port := standards.Port(endpoint.Api)
		owner := canonicalOwners[port]
		if owner == nil || apiPriority[endpoint.Api] < apiPriority[owner.Api] ||
			(apiPriority[endpoint.Api] == apiPriority[owner.Api] && resources.EndpointDestination(endpoint) < resources.EndpointDestination(owner)) {
			canonicalOwners[port] = endpoint
		}
	}
	ports := make(map[string]uint16, len(endpoints))
	allocated := make(map[uint16]string, len(endpoints))
	for _, endpoint := range endpoints {
		if _, seen := ports[endpoint.Name]; seen {
			return nil, fmt.Errorf("service %s/%s: endpoint %q is declared twice", module, service, endpoint.Name)
		}
		port := standards.Port(endpoint.Api)
		if canonicalOwners[port] != endpoint {
			port = ToNamedPort(ctx, "", module, service, endpoint.Name, endpoint.Api, PortModeHost)
		}
		if owner, exists := allocated[port]; exists {
			return nil, fmt.Errorf("endpoints %q and %q resolve to the same port %d", owner, endpoint.Name, port)
		}
		allocated[port] = endpoint.Name
		ports[endpoint.Name] = port
	}
	return ports, nil
}
