package network_test

import (
	"context"
	"fmt"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/standards"
	"github.com/stretchr/testify/require"
)

func deployedEndpoint(module, service, name, api string) *basev0.Endpoint {
	return &basev0.Endpoint{Module: module, Service: service, Name: name, Api: api}
}

// The golden case: a service exposing a named gRPC endpoint beside its
// conventional gRPC, Connect and REST endpoints. The named endpoint's port is a
// hash of the composed module name, so the same service composed under another
// name moves it — which is why a hand-declared port cannot stay correct.
func TestDeployedEndpointPortsGolden(t *testing.T) {
	ctx := context.Background()
	endpoints := func(module string) []*basev0.Endpoint {
		return []*basev0.Endpoint{
			deployedEndpoint(module, "accounts", "authority", standards.GRPC),
			deployedEndpoint(module, "accounts", "connect", standards.CONNECT),
			deployedEndpoint(module, "accounts", "grpc", standards.GRPC),
			deployedEndpoint(module, "accounts", "rest", standards.REST),
		}
	}

	ports, err := network.DeployedEndpointPorts(ctx, "saas", "accounts", endpoints("saas"))
	require.NoError(t, err)
	require.Equal(t, map[string]uint16{
		"authority": 52893,
		"connect":   8081,
		"grpc":      9090,
		"rest":      8080,
	}, ports)

	ports, err = network.DeployedEndpointPorts(ctx, "saas-starter", "accounts", endpoints("saas-starter"))
	require.NoError(t, err)
	require.Equal(t, uint16(6003), ports["authority"])
	require.Equal(t, uint16(9090), ports["grpc"])
}

func TestDeployedEndpointPortsCanonicalOwnerPerAPI(t *testing.T) {
	ctx := context.Background()
	for _, api := range []string{standards.GRPC, standards.REST, standards.HTTP, standards.CONNECT, standards.MCP, standards.TCP} {
		t.Run(api, func(t *testing.T) {
			// The only endpoint of an API owns the canonical port whatever it is called.
			ports, err := network.DeployedEndpointPorts(ctx, "mod", "svc", []*basev0.Endpoint{
				deployedEndpoint("mod", "svc", "anything", api),
			})
			require.NoError(t, err)
			require.Equal(t, standards.Port(api), ports["anything"])

			// With siblings of the same API, the endpoint named after the API owns it.
			ports, err = network.DeployedEndpointPorts(ctx, "mod", "svc", []*basev0.Endpoint{
				deployedEndpoint("mod", "svc", "sibling", api),
				deployedEndpoint("mod", "svc", api, api),
			})
			require.NoError(t, err)
			require.Equal(t, standards.Port(api), ports[api])
			require.Equal(t, network.ToNamedPort(ctx, "", "mod", "svc", "sibling", api, network.PortModeHost), ports["sibling"])
		})
	}
}

func TestDeployedEndpointPortsNamedSiblingsWithoutConventional(t *testing.T) {
	ctx := context.Background()
	// Two gRPC endpoints, neither named "grpc": neither is conventional, so
	// both get named ports and nobody holds 9090.
	ports, err := network.DeployedEndpointPorts(ctx, "mod", "svc", []*basev0.Endpoint{
		deployedEndpoint("mod", "svc", "alpha", standards.GRPC),
		deployedEndpoint("mod", "svc", "beta", standards.GRPC),
	})
	require.NoError(t, err)
	require.Equal(t, network.ToNamedPort(ctx, "", "mod", "svc", "alpha", standards.GRPC, network.PortModeHost), ports["alpha"])
	require.Equal(t, network.ToNamedPort(ctx, "", "mod", "svc", "beta", standards.GRPC, network.PortModeHost), ports["beta"])
	require.NotEqual(t, uint16(9090), ports["alpha"])
}

func TestDeployedEndpointPortsPriority(t *testing.T) {
	ctx := context.Background()
	// REST and HTTP share 8080; REST comes first in standards.APIS(), so it
	// keeps 8080 and HTTP falls back to a named port — in either input order.
	for _, order := range [][]string{{standards.REST, standards.HTTP}, {standards.HTTP, standards.REST}} {
		var endpoints []*basev0.Endpoint
		for _, api := range order {
			endpoints = append(endpoints, deployedEndpoint("mod", "svc", api, api))
		}
		ports, err := network.DeployedEndpointPorts(ctx, "mod", "svc", endpoints)
		require.NoError(t, err)
		require.Equal(t, uint16(8080), ports[standards.REST])
		require.Equal(t, network.ToNamedPort(ctx, "", "mod", "svc", standards.HTTP, standards.HTTP, network.PortModeHost), ports[standards.HTTP])
	}
}

func TestDeployedEndpointPortsTieBreak(t *testing.T) {
	ctx := context.Background()
	// Two unrecognised APIs share the fallback port 80 at the same (default)
	// priority; the lower EndpointDestination wins it, whatever the order.
	first := deployedEndpoint("mod", "svc", "aaa", "custom-a")
	second := deployedEndpoint("mod", "svc", "zzz", "custom-z")
	for _, endpoints := range [][]*basev0.Endpoint{{first, second}, {second, first}} {
		ports, err := network.DeployedEndpointPorts(ctx, "mod", "svc", endpoints)
		require.NoError(t, err)
		require.Equal(t, uint16(80), ports["aaa"])
		require.Equal(t, network.ToNamedPort(ctx, "", "mod", "svc", "zzz", "custom-z", network.PortModeHost), ports["zzz"])
	}
}

func TestDeployedEndpointPortsCollision(t *testing.T) {
	ctx := context.Background()
	// Find a named sibling whose hashed port lands exactly on another named
	// sibling's: a genuine collision the allocation must refuse, not paper over.
	seen := map[uint16]string{}
	var a, b string
	for i := 0; a == ""; i++ {
		name := fmt.Sprintf("endpoint-%d", i)
		port := network.ToNamedPort(ctx, "", "mod", "svc", name, standards.GRPC, network.PortModeHost)
		if prior, ok := seen[port]; ok {
			a, b = prior, name
		}
		seen[port] = name
		require.Less(t, i, 100000, "no colliding pair found")
	}
	_, err := network.DeployedEndpointPorts(ctx, "mod", "svc", []*basev0.Endpoint{
		deployedEndpoint("mod", "svc", a, standards.GRPC),
		deployedEndpoint("mod", "svc", b, standards.GRPC),
	})
	require.ErrorContains(t, err, "resolve to the same port")
	require.ErrorContains(t, err, a)
	require.ErrorContains(t, err, b)
}

func TestDeployedEndpointPortsRejectsNilAndDuplicates(t *testing.T) {
	ctx := context.Background()
	_, err := network.DeployedEndpointPorts(ctx, "mod", "svc", []*basev0.Endpoint{nil})
	require.ErrorContains(t, err, "nil endpoint")

	_, err = network.DeployedEndpointPorts(ctx, "mod", "svc", []*basev0.Endpoint{
		deployedEndpoint("mod", "svc", "grpc", standards.GRPC),
		deployedEndpoint("mod", "svc", "grpc", standards.GRPC),
	})
	require.ErrorContains(t, err, "declared twice")
}

func TestDeployedEndpointPortsEmpty(t *testing.T) {
	ports, err := network.DeployedEndpointPorts(context.Background(), "mod", "svc", nil)
	require.NoError(t, err)
	require.Empty(t, ports)
}
