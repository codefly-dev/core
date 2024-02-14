package configurations

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
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

func FindMapping(mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint) *basev0.NetworkMapping {
	for _, mapping := range mappings {
		if mapping.Endpoint.Name == endpoint.Name {
			return mapping
		}
	}
	return nil
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
	return BuildMappingInstance(mappings[0])
}

func BuildMappingInstance(mapping *basev0.NetworkMapping) (*MappingInstance, error) {
	if len(mapping.Addresses) == 0 {
		return nil, fmt.Errorf("no network addresses")
	}
	address := mapping.Addresses[0]
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

// GetMappingInstanceFor returns the network mapping instance when there is only one
// Really a convenience function for Agent
func GetMappingInstanceFor(mappings []*basev0.NetworkMapping, api string) (*MappingInstance, error) {
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no network mappings")
	}
	for _, m := range mappings {
		endpointAPI, err := APIAsStandard(m.Endpoint.Api)
		if err != nil {
			return nil, err
		}
		if endpointAPI != api {
			continue
		}
		return BuildMappingInstance(m)
	}
	return nil, fmt.Errorf("no network mappings for api: %s", api)
}

// GetMappingInstances returns the network mapping instances
func GetMappingInstancesFor(mappings []*basev0.NetworkMapping, api string) ([]*MappingInstance, error) {
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no network mappings")
	}
	var instances []*MappingInstance
	for _, m := range mappings {
		endpointAPI, err := APIAsStandard(m.Endpoint.Api)
		if err != nil {
			return nil, err
		}
		if endpointAPI != api {
			continue
		}
		instance, err := BuildMappingInstance(m)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

// GetMappingInstancesForName returns the network mapping instances
func GetMappingInstancesForName(mappings []*basev0.NetworkMapping, api string, name string) ([]*MappingInstance, error) {
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no network mappings")
	}
	var instances []*MappingInstance
	for _, m := range mappings {
		if m.Endpoint.Name != name {
			continue
		}
		endpointAPI, err := APIAsStandard(m.Endpoint.Api)
		if err != nil {
			return nil, err
		}
		if endpointAPI != api {
			continue
		}
		instance, err := BuildMappingInstance(m)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

func GetMappingInstanceForName(ctx context.Context, mappings []*basev0.NetworkMapping, api string, name string) (*MappingInstance, error) {
	w := wool.Get(ctx).In("configurations.GetMappingInstanceForName")
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no network mappings")
	}
	for _, m := range mappings {
		w.Focus("mapping", wool.Field("endpoint", m.Endpoint.Name))
		if m.Endpoint.Name != name {
			continue
		}
		endpointAPI, err := APIAsStandard(m.Endpoint.Api)
		if err != nil {
			return nil, err
		}
		if endpointAPI != api {
			continue
		}
		return BuildMappingInstance(m)
	}
	return nil, fmt.Errorf("no network mappings for api: %s", api)
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
