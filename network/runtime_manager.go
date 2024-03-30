package network

import (
	"context"
	"fmt"
	"runtime"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

const Localhost = "localhost"

type RuntimeManager struct {
	allocatedPorts map[uint16]bool
	dnsManager     DNSManager
}

func (m *RuntimeManager) GetNamespace(context.Context, *configurations.Service, *configurations.Environment) (string, error) {
	return "", fmt.Errorf("namespace don't make sense locally. something went wrong")
}

func Container(port uint16) *basev0.NetworkInstance {
	host := "host.docker.internal"
	// Set network mode to "host" only for Linux builds
	if runtime.GOOS == "linux" {
		host = Localhost
	}
	instance := configurations.NewNetworkInstance(host, port)
	instance.Scope = basev0.NetworkScope_Container
	return instance
}

func Native(port uint16) *basev0.NetworkInstance {
	host := Localhost
	instance := configurations.NewNetworkInstance(host, port)
	instance.Scope = basev0.NetworkScope_Native
	return instance
}

func PublicDefault(port uint16) *basev0.NetworkInstance {
	host := Localhost
	instance := configurations.NewNetworkInstance(host, port)
	instance.Scope = basev0.NetworkScope_Public
	return instance
}

func DNS(_ *configurations.Service, dns *basev0.DNS) *basev0.NetworkInstance {
	instance := configurations.NewNetworkInstance(dns.Host, uint16(dns.Port))
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
func (m *RuntimeManager) GenerateNetworkMappings(ctx context.Context, service *configurations.Service, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
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
					ContainerInstance(DNS(service, dns)),
					NativeInstance(DNS(service, dns)),
				)
				continue
			}
		}
		// Generate Port
		port := ToNamedPort(ctx, service.Application, service.Name, endpoint.Name, endpoint.Api)
		if _, ok := m.allocatedPorts[port]; ok {
			// Port already allocated
			return nil, w.NewError("port %d already allocated for service %s (TODO: randomize? force override?)", port, service.Unique())
		}
		m.allocatedPorts[port] = true
		nm.Instances = []*basev0.NetworkInstance{
			Container(port),
			Native(port),
		}
		if endpoint.Visibility == configurations.VisibilityPublic {
			nm.Instances = append(nm.Instances, PublicDefault(port))
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
