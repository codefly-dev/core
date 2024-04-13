package configurations

import (
	"context"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type NetworkInstance struct {
	Port     uint16
	Hostname string
	Host     string
	Address  string
}

func DefaultNetworkInstance(api string) *NetworkInstance {
	instance := &NetworkInstance{
		Port:     standards.Port(api),
		Hostname: "localhost",
		Host:     fmt.Sprintf("localhost:%d", standards.Port(api)),
		Address:  fmt.Sprintf("localhost:%d", standards.Port(api)),
	}
	if api == standards.REST || api == standards.HTTP {
		instance.Address = fmt.Sprintf("http://localhost:%d", standards.Port(api))
	}
	return instance
}

func NewNetworkInstance(hostname string, port uint16) *basev0.NetworkInstance {
	return &basev0.NetworkInstance{
		Hostname: hostname,
		Host:     fmt.Sprintf("%s:%d", hostname, port),
		Port:     uint32(port),
		Address:  fmt.Sprintf("%s:%d", hostname, port),
	}
}
func NewHTTPNetworkInstance(hostname string, port uint16, secured bool) *basev0.NetworkInstance {
	instance := &basev0.NetworkInstance{
		Hostname: hostname,
		Port:     uint32(port),
		Host:     fmt.Sprintf("%s:%d", hostname, port),
	}
	if secured {
		instance.Address = fmt.Sprintf("https://%s:%d", hostname, port)
	} else {
		instance.Address = fmt.Sprintf("http://%s:%d", hostname, port)
	}
	return instance
}

func FindNetworkInstanceInNetworkMappings(_ context.Context, mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint, scope basev0.NetworkScope) (*basev0.NetworkInstance, error) {
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
	return nil, fmt.Errorf("no network instance for endpoint: %s", EndpointFromProto(endpoint).Unique())
}

func FindNetworkMapping(_ context.Context, mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint) (*basev0.NetworkMapping, error) {
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

func ScopeAsString(scope basev0.NetworkScope) string {
	return basev0.NetworkScope_name[int32(scope)]

}

func MakeNetworkMappingSummary(mapping *basev0.NetworkMapping) string {
	var summaries []string
	for _, instance := range mapping.Instances {
		summaries = append(summaries, NetworkInstanceSummary(instance))
	}
	return fmt.Sprintf("%s:%s", EndpointDestination(mapping.Endpoint), strings.Join(summaries, ", "))
}

func ScopeString(scope basev0.NetworkScope) string {
	return basev0.NetworkScope_name[int32(scope)]
}

func NetworkInstanceSummary(value *basev0.NetworkInstance) string {
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

func LocalizeNetworkMapping(mappings []*basev0.NetworkMapping, hostname string) []*basev0.NetworkMapping {
	var results []*basev0.NetworkMapping
	for _, mapping := range mappings {
		var instances []*basev0.NetworkInstance
		for _, instance := range mapping.Instances {
			instances = append(instances, &basev0.NetworkInstance{
				Hostname: hostname,
				Host:     fmt.Sprintf("%s:%d", hostname, instance.Port),
				Port:     instance.Port,
				Address:  fmt.Sprintf("%s:%d", hostname, instance.Port),
				Scope:    instance.Scope,
			})
		}
		results = append(results, &basev0.NetworkMapping{
			Endpoint:  mapping.Endpoint,
			Instances: instances,
		})
	}
	return results

}
