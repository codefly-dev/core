package configurations

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

func LocalizeMappings(nm []*basev0.NetworkMapping, local string) {
	for _, mapping := range nm {
		LocalizeMapping(mapping, local)
	}
}

func LocalizeMapping(mapping *basev0.NetworkMapping, local string) {
	mapping.Address = strings.Replace(mapping.Address, "localhost", local, 1)
}

func FindNetworkMapping(endpoint *basev0.Endpoint, mappings []*basev0.NetworkMapping) (*basev0.NetworkMapping, error) {
	for _, mapping := range mappings {
		if mapping.Endpoint.Application == endpoint.Application &&
			mapping.Endpoint.Service == endpoint.Service &&
			mapping.Endpoint.Name == endpoint.Name {
			return mapping, nil
		}
	}
	return nil, fmt.Errorf("no network mapping for name: %s", EndpointFromProto(endpoint).Unique())
}

type MappingInstance struct {
	Address string
	Port    int
}

func BuildMappingInstance(mapping *basev0.NetworkMapping) (*MappingInstance, error) {
	address := mapping.Address
	port, err := PortFromAddress(address)
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
	return fmt.Sprintf("%s:%s", EndpointDestination(mapping.Endpoint), mapping.Address)
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

// ExtractEndpointEnvironmentVariables converts NetworkMapping info data to environment variables
func ExtractEndpointEnvironmentVariables(ctx context.Context, nets []*basev0.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		e := EndpointFromProto(net.Endpoint)
		endpoint := AsEndpointEnvironmentVariable(ctx, e, net.Address)
		envs = append(envs, endpoint)
	}
	return envs, nil
}

// ExtractRestRoutesEnvironmentVariables converts NetworkMapping info REST data to environment variables
func ExtractRestRoutesEnvironmentVariables(ctx context.Context, nets []*basev0.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		envs = append(envs, AsRestRouteEnvironmentVariable(ctx, net.Endpoint)...)
	}
	return envs, nil
}

func ExtractPublicNetworkMappings(mappings []*basev0.NetworkMapping) []*basev0.NetworkMapping {
	var publicMappings []*basev0.NetworkMapping
	for _, mapping := range mappings {
		if mapping.Endpoint.Visibility == VisibilityPublic {
			publicMappings = append(publicMappings, mapping)
		}
	}
	return publicMappings

}

// Split address in host and port
func SplitAddress(address string) (string, string, error) {
	tokens := strings.Split(address, ":")
	if len(tokens) != 2 {
		return "", "", fmt.Errorf("invalid address")
	}
	return tokens[0], tokens[1], nil
}
