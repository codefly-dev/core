package network

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

const Localhost = "localhost"

type RuntimeManager struct {
	allocatedPorts map[uint16]string
	dnsManager     DNSManager

	// For testing and ephemeral environments
	withTemporaryPorts bool
	lastRandomPort     uint16
}

var _ Manager = &RuntimeManager{}

func (m *RuntimeManager) SetLoadBalancer(string) {
	panic("N/A")
}

func (m *RuntimeManager) GetNamespace(context.Context, *resources.Environment, *resources.Workspace, *resources.Service) (string, error) {
	return "", fmt.Errorf("namespaces don't make sense locally. something went wrong")
}

func Container(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := "host.docker.internal"
	instance := resources.NewNetworkInstance(host, port)
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	}
	instance.Access = AccessContainer()
	return instance
}

func Native(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := Localhost
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = resources.NewNetworkInstance(host, port)
	}
	instance.Access = AccessNative()
	return instance
}

func AccessContainer() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{Kind: basev0.NetworkAccess_FromContainer}
}

func AccessNative() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{Kind: basev0.NetworkAccess_FromNative}
}

func PublicDefault(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := Localhost
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = resources.NewNetworkInstance(host, port)
	}
	instance.Access = resources.PublicNetworkAccess()
	return instance
}

func DNS(_ *resources.Service, endpoint *basev0.Endpoint, dns *basev0.DNS) *basev0.NetworkInstance {
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = resources.NewHTTPNetworkInstance(dns.Host, uint16(dns.Port), dns.Secured)
	} else {
		instance = resources.NewNetworkInstance(dns.Host, uint16(dns.Port))
	}
	instance.Access = resources.PublicNetworkAccess()
	return instance
}

func ContainerInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	return instance
}

func NativeInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	return instance
}

func PublicInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	return instance
}

func ExternalInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *RuntimeManager) GenerateNetworkMappings(ctx context.Context, env *resources.Environment, workspace *resources.Workspace, service *resources.Service, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
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
		if endpoint.Visibility == resources.VisibilityExternal {
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
		var port uint16
		if m.withTemporaryPorts {
			port = m.GetFreePort()
		} else {
			port = ToNamedPort(ctx, workspace.Name, service.Module, service.Name, endpoint.Name, endpoint.Api)

		}
		if unique, found := m.allocatedPorts[port]; found && unique != service.Unique() {
			// Port already allocated
			return nil, w.NewError("port %d already allocated for service %s (TODO: randomize? force override?)", port, service.Unique())
		}
		m.allocatedPorts[port] = service.Unique()
		nm.Instances = []*basev0.NetworkInstance{
			Container(endpoint, port),
			Native(endpoint, port),
		}
		if endpoint.Visibility == resources.VisibilityPublic {
			nm.Instances = append(nm.Instances, PublicDefault(endpoint, port))
		}
		out = append(out, nm)
	}
	return out, nil
}

// WithTemporaryPorts will use random ports instead of "named" ports
func (m *RuntimeManager) WithTemporaryPorts() {
	m.withTemporaryPorts = true
	m.lastRandomPort = 20000
}

// GetFreePort returns the next free port after lastRandomPort
func (m *RuntimeManager) GetFreePort() uint16 {
	for {
		m.lastRandomPort++
		// Check if the port is already allocated
		if _, ok := m.allocatedPorts[m.lastRandomPort]; !ok {
			// Try to establish a listener on the port
			ln, err := net.Listen("tcp", ":"+strconv.Itoa(int(m.lastRandomPort)))
			if err != nil {
				// If an error occurs, the port is likely in use
				continue
			}
			// If the listener is established successfully, the port is free
			ln.Close()
			return m.lastRandomPort
		}
	}
}

func NewRuntimeManager(_ context.Context, dnsManager DNSManager) (*RuntimeManager, error) {
	return &RuntimeManager{
		dnsManager:     dnsManager,
		allocatedPorts: make(map[uint16]string),
	}, nil
}
