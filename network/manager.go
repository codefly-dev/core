package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type RuntimeManager struct {
	DNSManager     DNSManager
	allocatedPorts map[uint16]bool
}

func Container(port uint16) *basev0.NetworkInstance {
	instance := &basev0.NetworkInstance{
		Scope: basev0.RuntimeScope_Container,
		Port:  uint32(port),
		Host:  "host.docker.internal",
	}
	instance.Address = fmt.Sprintf("%s:%d", instance.Host, instance.Port)
	return instance
}

func Native(port uint16) *basev0.NetworkInstance {
	instance := &basev0.NetworkInstance{
		Scope: basev0.RuntimeScope_Native,
		Port:  uint32(port),
		Host:  "localhost",
	}
	instance.Address = fmt.Sprintf("%s:%d", instance.Host, instance.Port)
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m RuntimeManager) GenerateNetworkMappings(ctx context.Context, service *configurations.Service, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	var out []*basev0.NetworkMapping
	if m.DNSManager == nil {
		for _, endpoint := range endpoints {
			// Generate Port
			port := ToNamedPort(ctx, service.Application, service.Name, endpoint.Name, endpoint.Api)
			if _, ok := m.allocatedPorts[port]; ok {
				// Port already allocated
				return nil, w.NewError("port %d already allocated for service %s (TODO: randomize? force override?)", port, service.Unique())
			}
			m.allocatedPorts[port] = true
			out = append(out, &basev0.NetworkMapping{
				Endpoint: endpoint,
				Instances: []*basev0.NetworkInstance{
					Container(port),
					Native(port),
				},
			})
		}
	}
	return out, nil
}

func NewManager(_ context.Context) (*RuntimeManager, error) {
	return &RuntimeManager{
		allocatedPorts: make(map[uint16]bool),
	}, nil
}
