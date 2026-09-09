package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

const Localhost = "localhost"

// RuntimeManager tracks per-port allocation across the entire
// service graph. The CLI fan-allocates ports for parallel services,
// so concurrent GenerateNetworkMappings / AllocateTemporaryPort calls share
// allocatedPorts. mu serializes all mutations and reads — Go panics
// on concurrent map writes, and package test processes may ask for
// multiple temporary endpoints concurrently.
type RuntimeManager struct {
	mu             sync.Mutex
	allocatedPorts map[uint16]string
	dnsManager     DNSManager

	// For testing and ephemeral environments
	withTemporaryPorts bool

	// portOverrides pins specific endpoints to caller-chosen host ports,
	// keyed by EndpointDestination (module/service/endpoint). An override
	// wins over both the deterministic hash and temporary ports; the normal
	// cross-endpoint conflict check still applies, so two endpoints pinned to
	// the same port is reported rather than silently double-allocated.
	portOverrides map[string]uint16
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
// It does NOT apply to external endpoints, whose address comes from a DNS
// record resolved at runtime rather than the port hash — callers must
// special-case them with resources.IsExternalEndpoint (external is a location,
// expressible either as location: external or the deprecated visibility).
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
	// Ports this call reserved. The manager outlives any single call, so a
	// failure part-way through the endpoint list must hand back what it took —
	// otherwise a caller that retries or moves on leaves the manager believing
	// ports are in use by a service that never started.
	var reserved []uint16
	releaseReserved := func() {
		for _, port := range reserved {
			m.ReleasePort(port)
		}
	}
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
		owner := resources.EndpointDestination(endpoint)
		var port uint16
		if override, ok := m.portOverride(owner); ok {
			port = override
		} else {
			name := endpoint.Name
			if env.NamingScope != "" {
				name = fmt.Sprintf("%s-%s", endpoint.Name, env.NamingScope)
			}
			if m.withTemporaryPorts {
				var err error
				port, err = m.AllocateTemporaryPort(ctx)
				if err != nil {
					releaseReserved()
					return nil, w.Wrapf(err, "cannot allocate a temporary port for endpoint %s", owner)
				}
			} else {
				port = ToNamedPort(ctx, workspace.Name, service.Module, service.Name, name, endpoint.Api, mode)
			}
		}
		w.Debug("allocating port", wool.Field("port", port), wool.Field("service", service.Unique()))
		// Lock around the read-then-write — without this, two
		// concurrent GenerateNetworkMappings calls can both check
		// "free" and both insert, racing the map and (worse) double-
		// allocating the same port.
		m.mu.Lock()
		// AllocateTemporaryPort marks the port it hands out with the placeholder
		// owner randomPortOwner so a concurrent allocation can't re-hand it. That is
		// NOT a real cross-endpoint conflict — the caller is about to claim it
		// below — so only a port owned by a DIFFERENT real endpoint collides.
		allocatedTo, found := m.allocatedPorts[port]
		if found && allocatedTo != randomPortOwner && allocatedTo != owner {
			m.mu.Unlock()
			releaseReserved()
			return nil, w.NewError("port %d for endpoint %s is already allocated to %s", port, owner, allocatedTo)
		}
		// A port this owner already held before the call is not ours to hand
		// back; only reservations this call created are rolled back.
		heldBeforeCall := found && allocatedTo == owner
		m.allocatedPorts[port] = owner
		m.mu.Unlock()
		if !heldBeforeCall {
			reserved = append(reserved, port)
		}
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

// WithPortOverrides pins endpoints to caller-chosen host ports. Keys are
// EndpointDestination values (module/service/endpoint, e.g. "app/subject/rest");
// the value is the host port that endpoint must bind. Overrides merge across
// calls. An override takes precedence over both the deterministic hash and
// temporary ports, letting a caller anchor a known endpoint while the rest of
// the graph is still allocated normally.
func (m *RuntimeManager) WithPortOverrides(overrides map[string]uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.portOverrides == nil {
		m.portOverrides = make(map[string]uint16, len(overrides))
	}
	for destination, port := range overrides {
		m.portOverrides[destination] = port
	}
}

func (m *RuntimeManager) portOverride(destination string) (uint16, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	port, ok := m.portOverrides[destination]
	return port, ok
}

// randomPortOwner is the placeholder owner AllocateTemporaryPort writes into
// allocatedPorts for a port it has handed out but that no endpoint has formally
// claimed yet. The named-port conflict check treats it as "claimable", not a
// cross-endpoint conflict.
const randomPortOwner = "random"

// temporaryPortAttempts bounds AllocateTemporaryPort's retry loop. Retries
// cover a reservation collision, a probe-close failure, or a descriptor table
// that is momentarily full — all of which clear within a handful of attempts;
// a larger budget only turns a broken host into a long spin.
const temporaryPortAttempts = 16

// temporaryPortBackoff is the pause between allocation retries. It is short
// enough to stay invisible in a normal flow and long enough that a pathological
// host is not hammered.
const temporaryPortBackoff = 5 * time.Millisecond

// ErrTemporaryPortUnavailable marks an allocation failure the host may recover
// from on its own: a descriptor table that stayed full for the whole budget, or
// a manager whose reservations covered every port the kernel offered. Retrying
// the operation later can succeed.
var ErrTemporaryPortUnavailable = errors.New("no temporary port available")

// ErrTemporaryPortUnsupported marks an allocation failure that will not clear
// without operator action: no IPv4 loopback to bind, or a kernel that refuses
// the address family outright. Retrying is pointless — the host is misconfigured
// for the way codefly allocates ports.
var ErrTemporaryPortUnsupported = errors.New("temporary port allocation unsupported on this host")

// AllocateTemporaryPort asks the kernel to bind an ephemeral IPv4 loopback
// port, records it in this manager, then releases the probe listener so the
// service runtime can bind it. Kernel allocation avoids the low-entropy,
// process-local starting points that caused independent test CLIs to select the
// same sequential port.
//
// The listener remains open until after the in-process reservation is recorded,
// so concurrent callers on this manager cannot receive the same port. Closing
// the probe does NOT reserve the port against other processes — the reservation
// is in-process only, and another process may bind the port between this call
// and the service actually starting. Cross-process listener ownership is
// tracked separately (audit F05).
//
// Allocation is bounded. A host that cannot bind loopback at all fails on the
// first attempt with ErrTemporaryPortUnsupported. A full descriptor table, a
// reservation collision, or a probe-close failure retries at most
// temporaryPortAttempts times with a backoff, then fails with
// ErrTemporaryPortUnavailable wrapping the last cause. Cancellation is checked
// before every attempt, so the call returns within the caller's deadline or the
// attempt budget, whichever comes first.
func (m *RuntimeManager) AllocateTemporaryPort(ctx context.Context) (uint16, error) {
	w := wool.Get(ctx).In("network.Runtime.AllocateTemporaryPort")
	var lastErr error
	for attempt := 1; attempt <= temporaryPortAttempts; attempt++ {
		if attempt > 1 {
			waitTemporaryPortBackoff(ctx)
		}
		// ctx.Err() is the authority on cancellation, not the backoff's select:
		// when the timer and ctx.Done() are both ready, select picks at random,
		// so branching on which case fired would let an attempt run after the
		// caller gave up.
		if err := ctx.Err(); err != nil {
			return 0, w.Wrapf(err, "cancelled allocating a temporary port after %d/%d attempts (last error: %v)", attempt-1, temporaryPortAttempts, lastErr)
		}

		listener, port, err := listenTemporaryPort()
		if err != nil {
			if !isRetryableListenError(err) {
				return 0, w.Wrapf(fmt.Errorf("%w: %w", ErrTemporaryPortUnsupported, err), "cannot bind a kernel-assigned loopback port (attempt %d/%d)", attempt, temporaryPortAttempts)
			}
			lastErr = err
			continue
		}

		m.mu.Lock()
		_, alreadyAllocated := m.allocatedPorts[port]
		if !alreadyAllocated {
			m.allocatedPorts[port] = randomPortOwner
		}
		m.mu.Unlock()

		if alreadyAllocated {
			_ = listener.Close()
			lastErr = fmt.Errorf("kernel re-offered port %d, already reserved by this manager", port)
			continue
		}

		if err := listener.Close(); err != nil {
			m.ReleasePort(port)
			lastErr = err
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("cannot allocate a temporary port after %d attempts: %w: %w", temporaryPortAttempts, ErrTemporaryPortUnavailable, lastErr)
}

// ReleasePort drops this manager's reservation for a port so a later allocation
// can hand it out again. Without it the manager's view of the host only ever
// grows towards "everything is taken", and the bounded retry above turns that
// drift into a hard allocation failure.
func (m *RuntimeManager) ReleasePort(port uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allocatedPorts, port)
}

// isRetryableListenError separates a host that is momentarily out of
// descriptors — which clears on its own — from one that cannot serve loopback
// at all, which never will.
func isRetryableListenError(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

// waitTemporaryPortBackoff pauses between attempts, returning early when the
// caller cancels. It reports nothing: the caller re-reads ctx.Err() so a
// simultaneously-ready timer can never mask a cancellation.
func waitTemporaryPortBackoff(ctx context.Context) {
	timer := time.NewTimer(temporaryPortBackoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
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
