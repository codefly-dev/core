package network

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

const Localhost = "localhost"

// RuntimeManager tracks per-port allocation across the entire
// service graph. The CLI fan-allocates ports for parallel services,
// so concurrent GenerateNetworkMappings / GetFreePort calls share
// allocatedPorts. mu serializes all mutations and reads — Go panics
// on concurrent map writes, and package test processes may ask for
// multiple temporary endpoints concurrently.
type RuntimeManager struct {
	mu             sync.Mutex
	allocatedPorts map[uint16]string
	dnsManager     DNSManager

	// For testing and ephemeral environments
	withTemporaryPorts bool
}

func Container(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := "host.docker.internal"
	instance := resources.NewNetworkInstance(host, port)
	if standards.IsHTTPBasedAPI(endpoint.Api) {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	}
	instance.Access = resources.NewContainerNetworkAccess()
	return instance
}

func Native(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := Localhost
	var instance *basev0.NetworkInstance
	if standards.IsHTTPBasedAPI(endpoint.Api) {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = resources.NewNetworkInstance(host, port)
	}
	instance.Access = resources.NewNativeNetworkAccess()
	return instance
}

// NativeFor computes the deterministic native (localhost) NetworkInstance an
// endpoint binds to under a normal `codefly run` — WITHOUT a running flow.
//
// It mirrors RuntimeManager.GenerateNetworkMappings' Native() path exactly
// (same ToNamedPort hash, same naming-scope folding, same Native() address
// formatting), so the value matches what a successfully-running service
// actually serves: collisions abort startup (so a running service never
// deviates from the hash) and temporary ports are test-only.
//
// It does NOT apply to external-visibility endpoints, whose address comes from
// a DNS record resolved at runtime rather than the port hash — callers must
// special-case resources.VisibilityExternal.
func NativeFor(ctx context.Context, workspace, module, service, namingScope string, endpoint *basev0.Endpoint) *basev0.NetworkInstance {
	name := endpoint.Name
	if namingScope != "" {
		name = fmt.Sprintf("%s-%s", endpoint.Name, namingScope)
	}
	port := ToNamedPort(ctx, workspace, module, service, name, endpoint.Api, PortModeHost)
	return Native(endpoint, port)
}

func PublicDefault(endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := Localhost
	var instance *basev0.NetworkInstance
	if standards.IsHTTPBasedAPI(endpoint.Api) {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = resources.NewNetworkInstance(host, port)
	}
	instance.Access = resources.NewPublicNetworkAccess()
	return instance
}

func DNS(_ *resources.ServiceIdentity, endpoint *basev0.Endpoint, dns *basev0.DNS) *basev0.NetworkInstance {
	var instance *basev0.NetworkInstance
	if standards.IsHTTPBasedAPI(endpoint.Api) {
		instance = resources.NewHTTPNetworkInstance(dns.Host, uint16(dns.Port), dns.Secured)
	} else {
		instance = resources.NewNetworkInstance(dns.Host, uint16(dns.Port))
	}
	instance.Access = resources.NewPublicNetworkAccess()
	return instance
}

// ContainerInstance stamps an instance with Container access.
//
// Used when an instance is built from a DNS record (which unconditionally
// tags Access=Public) but the mapping needs a Container-accessible variant
// so agents running inside Docker can resolve it. Mutates and returns the
// input — callers pass a freshly-constructed instance per wrap.
func ContainerInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Access = resources.NewContainerNetworkAccess()
	return instance
}

// NativeInstance stamps an instance with Native access.
//
// Same rationale as ContainerInstance: covers the case where an instance
// comes from DNS (Access=Public) but the agent runs natively on the host
// and looks up by Access=Native when calling FindNetworkInstanceInNetworkMappings.
func NativeInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Access = resources.NewNativeNetworkAccess()
	return instance
}

// PublicInstance stamps an instance with Public access.
//
// DNS instances are already Public, so this is often a no-op — but
// keeping it explicit makes the semantics at the call-site clear and
// defends against future changes to DNS().
func PublicInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Access = resources.NewPublicNetworkAccess()
	return instance
}

// ExternalInstance marks the instance as externally routable (via DNS).
//
// Externally-exposed endpoints are reached through their DNS entry from
// outside the cluster, which is Public access in the network model.
func ExternalInstance(instance *basev0.NetworkInstance) *basev0.NetworkInstance {
	instance.Access = resources.NewPublicNetworkAccess()
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *RuntimeManager) GenerateNetworkMappings(ctx context.Context,
	env *resources.Environment,
	workspace *resources.Workspace,
	service *resources.ServiceIdentity,
	endpoints []*basev0.Endpoint,
	runtimeContext *basev0.RuntimeContext) ([]*basev0.NetworkMapping, error) {
	if m == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	mode := PortModeFor(runtimeContext.GetKind())
	var out []*basev0.NetworkMapping
	for _, endpoint := range endpoints {
		nm := &basev0.NetworkMapping{
			Endpoint: endpoint,
		}
		// External endpoints
		if resources.IsExternalEndpoint(endpoint) {
			var dns *basev0.DNS
			var err error
			if m.dnsManager == nil {
				w.Warn("no DNS manager found for external endpoint: will use the `public` version if possible")
			} else {
				dns, err = m.dnsManager.GetDNS(ctx, service, endpoint.Name)
				if err != nil {
					w.Warn("no DNS found for external endpoint: will use the `public` version if possible")
				}
			}
			if dns != nil {
				nm.Instances = append(nm.Instances,
					ContainerInstance(DNS(service, endpoint, dns)),
					NativeInstance(DNS(service, endpoint, dns)),
				)
				out = append(out, nm)
				continue
			}
		}
		// Generate Port
		var port uint16
		name := endpoint.Name
		if env.NamingScope != "" {
			name = fmt.Sprintf("%s-%s", endpoint.Name, env.NamingScope)
		}
		if m.withTemporaryPorts {
			port = m.GetFreePort()
		} else {
			port = ToNamedPort(ctx, workspace.Name, service.Module, service.Name, name, endpoint.Api, mode)

		}
		w.Debug("allocating port", wool.Field("port", port), wool.Field("service", service.Unique()))
		// Lock around the read-then-write — without this, two
		// concurrent GenerateNetworkMappings calls can both check
		// "free" and both insert, racing the map and (worse) double-
		// allocating the same port.
		m.mu.Lock()
		// GetFreePort marks the port it hands out with the placeholder owner
		// randomPortOwner so a concurrent GetFreePort can't re-hand it. That is
		// NOT a real cross-service conflict — the caller is about to claim it
		// below — so only a port owned by a DIFFERENT real service collides.
		if unique, found := m.allocatedPorts[port]; found && unique != randomPortOwner && unique != service.Unique() {
			m.mu.Unlock()
			return nil, w.NewError("port %d already allocated for service %s", port, service.Unique())
		}
		m.allocatedPorts[port] = service.Unique()
		m.mu.Unlock()
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

// WithTemporaryPorts asks the host kernel for ephemeral ports instead of using
// deterministic "named" ports. This is intended for short-lived test flows
// owned by independent processes.
func (m *RuntimeManager) WithTemporaryPorts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.withTemporaryPorts = true
}

// randomPortOwner is the placeholder owner GetFreePort writes into
// allocatedPorts for a port it has handed out but that no service has formally
// claimed yet. The named-port conflict check treats it as "claimable", not a
// cross-service conflict.
const randomPortOwner = "random"

// GetFreePort asks the kernel to bind an ephemeral IPv4 loopback port, records
// it in this manager, then releases the probe listener so the service runtime
// can bind it. Kernel allocation avoids the low-entropy, process-local starting
// points that caused independent test CLIs to select the same sequential port.
//
// The listener remains open until after the in-process reservation is recorded,
// so concurrent callers on this manager cannot receive the same port.
func (m *RuntimeManager) GetFreePort() uint16 {
	for {
		listener, port, err := listenTemporaryPort()
		if err != nil {
			continue
		}

		m.mu.Lock()
		if _, alreadyAllocated := m.allocatedPorts[port]; alreadyAllocated {
			m.mu.Unlock()
			_ = listener.Close()
			continue
		}
		m.allocatedPorts[port] = randomPortOwner
		m.mu.Unlock()

		if err := listener.Close(); err != nil {
			m.mu.Lock()
			delete(m.allocatedPorts, port)
			m.mu.Unlock()
			continue
		}
		return port
	}
}

func listenTemporaryPort() (*net.TCPListener, uint16, error) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	})
	if err != nil {
		return nil, 0, err
	}
	return listener, uint16(listener.Addr().(*net.TCPAddr).Port), nil
}

func NewRuntimeManager(_ context.Context, dnsManager DNSManager) (*RuntimeManager, error) {
	return &RuntimeManager{
		dnsManager:     dnsManager,
		allocatedPorts: make(map[uint16]string),
	}, nil
}
