package configurations

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

func LocalizeMappings(nm []*basev0.NetworkMapping, local string) []*basev0.NetworkMapping {
	var localized []*basev0.NetworkMapping
	for _, mapping := range nm {
		localized = append(localized, LocalizeMapping(mapping, local))
	}
	return localized
}

func LocalizeMapping(mapping *basev0.NetworkMapping, local string) *basev0.NetworkMapping {
	var addresses []string
	for _, addr := range mapping.Addresses {
		addresses = append(addresses, strings.Replace(addr, "localhost", local, 1))
	}
	return &basev0.NetworkMapping{
		Application: mapping.Application,
		Service:     mapping.Service,
		Endpoint:    mapping.Endpoint,
		Addresses:   addresses,
	}
}

type MappingInstance struct {
	Address string
	Port    int
}

// GetMappingInstance returns the network mapping instance when there is only one
// Really a convenience function for Agent
func GetMappingInstance(mappings []*basev0.NetworkMapping) (*MappingInstance, error) {
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no network mappings")
	}
	m := mappings[0]
	if len(m.Addresses) == 0 {
		return nil, fmt.Errorf("no network addresses")
	}
	address := m.Addresses[0]
	tokens := strings.Split(address, ":")
	if len(tokens) != 2 {
		return nil, fmt.Errorf("invalid network address")
	}
	port, err := strconv.Atoi(tokens[1])
	if err != nil {
		return nil, fmt.Errorf("invalid network port")
	}
	return &MappingInstance{
		Address: address,
		Port:    port,
	}, nil
}

func MakeNetworkMappingSummary(mappings []*basev0.NetworkMapping) string {
	var results []string
	for _, mapping := range mappings {
		results = append(results, NetworkMappingInfo(mapping))
	}
	return strings.Join(results, ", ")
}

func NetworkMappingInfo(mapping *basev0.NetworkMapping) string {
	return fmt.Sprintf("%s:%s", EndpointDestination(mapping.Endpoint), mapping.Addresses)
}

func networkMappingHash(n *basev0.NetworkMapping) string {
	return HashString(n.String())
}

func NetworkMappingHash(networkMappings ...*basev0.NetworkMapping) (string, error) {
	hasher := NewHasher()
	for _, networkMapping := range networkMappings {
		hasher.Add(networkMappingHash(networkMapping))
	}
	return hasher.Hash(), nil
}

// ExtractEndpointEnvironmentVariables converts NetworkMapping endpoint data to environment variables
func ExtractEndpointEnvironmentVariables(ctx context.Context, nets []*basev0.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		e := EndpointFromProto(net.Endpoint)
		endpoint := AsEndpointEnvironmentVariable(ctx, e, net.Addresses)
		envs = append(envs, endpoint)
	}
	return envs, nil
}

// ExtractRestRoutesEnvironmentVariables converts NetworkMapping endpoint REST data to environment variables
func ExtractRestRoutesEnvironmentVariables(ctx context.Context, nets []*basev0.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		envs = append(envs, AsRestRouteEnvironmentVariable(ctx, net.Endpoint)...)
	}
	return envs, nil
}
