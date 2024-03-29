package configurations

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

func NewNetworkInstance(host string, port uint16) *basev0.NetworkInstance {
	return &basev0.NetworkInstance{
		Host: host,
		Port: uint32(port),
	}
}

func FindNetworkInstance(mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint, scope basev0.RuntimeScope) (*basev0.NetworkInstance, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("can't find network instance for a nil endpoint")
	}
	for _, mapping := range mappings {
		if mapping.Endpoint.Application == endpoint.Application &&
			mapping.Endpoint.Service == endpoint.Service &&
			mapping.Endpoint.Api == endpoint.Api &&
			mapping.Endpoint.Name == endpoint.Name {
			for _, instance := range mapping.Instances {
				if instance.Scope == scope {
					return instance, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no network endpoint for name: %s", EndpointFromProto(endpoint).Unique())
}

func FindNetworkMapping(mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint) (*basev0.NetworkMapping, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("can't find network instance for a nil endpoint")
	}
	for _, mapping := range mappings {
		if mapping.Endpoint.Application == endpoint.Application &&
			mapping.Endpoint.Service == endpoint.Service &&
			mapping.Endpoint.Api == endpoint.Api &&
			mapping.Endpoint.Name == endpoint.Name {
			return mapping, nil

		}
	}
	return nil, fmt.Errorf("no network mapping for endpoint: %s", EndpointFromProto(endpoint).Unique())
}

//	func BuildMappingInstance(mapping *basev0.NetworkMapping) (*MappingInstance, error) {
//		address := mapping.Address
//		port, err := PortFromAddress(address)
//		if err != nil {
//			return nil, fmt.Errorf("invalid network port")
//		}
//		return &MappingInstance{
//			Address: address,
//			Port:    port,
//		}, nil
//	}
func MakeManyNetworkMappingSummary(mappings []*basev0.NetworkMapping) string {
	var results []string
	for _, mapping := range mappings {
		results = append(results, MakeNetworkMappingSummary(mapping))
	}
	return strings.Join(results, ", ")
}

func MakeNetworkMappingSummary(mapping *basev0.NetworkMapping) string {
	var summaries []string
	for _, instance := range mapping.Instances {
		summaries = append(summaries, NetworkInstanceInfo(instance))
	}
	return fmt.Sprintf("%s:%s", EndpointDestination(mapping.Endpoint), strings.Join(summaries, ", "))
}

func ScopeString(scope basev0.RuntimeScope) string {
	return basev0.RuntimeScope_name[int32(scope)]
}

func NetworkInstanceInfo(value *basev0.NetworkInstance) string {
	return fmt.Sprintf("%s:%d (%s)", value.Host, value.Port, ScopeString(value.Scope))
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
