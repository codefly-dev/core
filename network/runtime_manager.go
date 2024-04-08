package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/configurations/standards"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

const Localhost = "localhost"

type RuntimeManager struct {
	allocatedPorts map[uint16]bool
	dnsManager     DNSManager
}

func (m *RuntimeManager) SetLoadBalancer(string) {
	panic("N/A")
}

func (m *RuntimeManager) GetNamespace(context.Context, *configurations.Service, *configurations.Environment) (string, error) {
	return "", fmt.Errorf("namespace don't make sense locally. something went wrong")
}

func Container(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := "host.docker.internal"
	instance := configurations.NewNetworkInstance(host, port)
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, port, false)
	}
	instance.Scope = basev0.NetworkScope_Container
	return instance
}

func Native(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := Localhost
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = configurations.NewNetworkInstance(host, port)
	}
	instance.Scope = basev0.NetworkScope_Native
	return instance
}

func PublicDefault(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := Localhost
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = configurations.NewNetworkInstance(host, port)
	}
	instance.Scope = basev0.NetworkScope_Public
	return instance
}

func DNS(_ *configurations.Service, endpoint *basev0.Endpoint, dns *basev0.DNS) *basev0.NetworkInstance {
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(dns.Host, uint16(dns.Port), dns.Secured)
	} else {
		instance = configurations.NewNetworkInstance(dns.Host, uint16(dns.Port))
	}
	return instance
}

func ContainerInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Scope = basev0.NetworkScope_Container
	return instance
}

func NativeInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Scope = basev0.NetworkScope_Native
	return instance
}

func PublicInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Scope = basev0.NetworkScope_Public
	return instance
}

func ExternalInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Scope = basev0.NetworkScope_External
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *RuntimeManager) GenerateNetworkMappings(ctx context.Context, service *configurations.Service, endpoints []*basev0.Endpoint, _ *configurations.Environment) ([]*basev0.NetworkMapping, error) {
	if m == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	var out []*basev0.NetworkMapping
	for _, endpoint := range endpoints {
		nm := &basev0.NetworkMapping{
			Endpoint: endpoint,
		}
		// External endpoints
		if endpoint.Visibility == configurations.VisibilityExternal {
			dns, err := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
			if err != nil {
				w.Warn("no DNS found for external endpoint: will use the `public` version if possible")
			}
			if dns != nil {
				nm.Instances = append(nm.Instances,
					ContainerInstance(DNS(service, endpoint, dns)),
					NativeInstance(DNS(service, endpoint, dns)),
				)
				continue
			}
		}
		// Generate Port
		port := ToNamedPort(ctx, service.Project, service.Application, service.Name, endpoint.Name, endpoint.Api)
		if _, ok := m.allocatedPorts[port]; ok {
			// Port already allocated
			return nil, w.NewError("port %d already allocated for service %s (TODO: randomize? force override?)", port, service.Unique())
		}
		m.allocatedPorts[port] = true
		nm.Instances = []*basev0.NetworkInstance{
			Container(endpoint, port),
			Native(endpoint, port),
		}
		if endpoint.Visibility == configurations.VisibilityPublic {
			nm.Instances = append(nm.Instances, PublicDefault(endpoint, port))
		}
		out = append(out, nm)
	}
	return out, nil
}

func NewRuntimeManager(_ context.Context, dnsManager DNSManager) (*RuntimeManager, error) {
	return &RuntimeManager{
		dnsManager:     dnsManager,
		allocatedPorts: make(map[uint16]bool),
	}, nil
}
