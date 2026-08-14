package resources_test

import (
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/stretchr/testify/require"
)

func TestParsingFromAddress(t *testing.T) {
	tcs := []struct {
		address  string
		hostname string
		port     uint16
	}{
		{"localhost:8080", "localhost", 8080},
		{"http://localhost:8080", "localhost", 8080},
	}
	for _, tc := range tcs {
		t.Run(tc.address, func(t *testing.T) {
			add, err := resources.ParseAddress(tc.address)
			require.NoError(t, err)
			require.Equal(t, tc.port, add.Port)
			require.Equal(t, tc.hostname, add.Hostname)
		})
	}
}

func TestResolveDependencyNetworkMappingsKeepsOnlyNamedEndpoint(t *testing.T) {
	dependency := &resources.ServiceDependency{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "usage"}},
	}
	mappings := []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "grpc", Api: standards.GRPC}},
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: standards.GRPC}},
	}

	resolved, err := resources.ResolveDependencyNetworkMappings([]*resources.ServiceDependency{dependency}, mappings)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, "usage", resolved[0].Endpoint.Name)
}

func TestResolveDependencyNetworkMappingsAllowsUnavailableNamedEndpoint(t *testing.T) {
	dependency := &resources.ServiceDependency{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "usage"}},
	}

	resolved, err := resources.ResolveDependencyNetworkMappings([]*resources.ServiceDependency{dependency}, nil)
	require.NoError(t, err)
	require.Empty(t, resolved)
}

func TestResolveDependencyNetworkMappingsSelectsUniqueAPIReference(t *testing.T) {
	dependency := &resources.ServiceDependency{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{API: standards.GRPC}},
	}
	mappings := []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: standards.GRPC}},
	}

	resolved, err := resources.ResolveDependencyNetworkMappings([]*resources.ServiceDependency{dependency}, mappings)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, "usage", resolved[0].Endpoint.Name)
}
