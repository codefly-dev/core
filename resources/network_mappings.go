package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/standards"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func NewNativeNetworkAccess() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{
		Kind: NetworkAccessNative,
	}
}

func NewContainerNetworkAccess() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{
		Kind: NetworkAccessContainer,
	}
}

func NewPublicNetworkAccess() *basev0.NetworkAccess {
	return &basev0.NetworkAccess{
		Kind: NetworkAccessPublic,
	}
}

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

func FilterNetworkInstance(_ context.Context, instances []*basev0.NetworkInstance, networkAccess *basev0.NetworkAccess) *basev0.NetworkInstance {
	for _, instance := range instances {
		if instance.Access.Kind == networkAccess.Kind {
			return instance
		}
	}
	return nil
}

func FindNetworkInstanceInNetworkMappings(ctx context.Context, mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint, networkAccess *basev0.NetworkAccess) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("FindNetworkInstanceInNetworkMappings")
	if endpoint == nil {
		return nil, w.NewError("can't find network instance for a nil endpoint")
	}
	for _, mapping := range mappings {
		if mapping.Endpoint.Module == endpoint.Module &&
			mapping.Endpoint.Service == endpoint.Service &&
			mapping.Endpoint.Api == endpoint.Api &&
			mapping.Endpoint.Name == endpoint.Name {
			for _, instance := range mapping.Instances {
				if instance.Access.Kind == networkAccess.Kind {
					return instance, nil
				}
			}
		}
	}
	return nil, w.NewError("no network instance for endpoint: %s", EndpointFromProto(endpoint).Unique())
}

func FindNetworkMapping(ctx context.Context, mappings []*basev0.NetworkMapping, endpoint *basev0.Endpoint) (*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("FindNetworkMapping")
	if endpoint == nil {
		return nil, w.NewError("can't find network instance for a nil endpoint")
	}
	for _, mapping := range mappings {
		if mapping.Endpoint.Module == endpoint.Module &&
			mapping.Endpoint.Service == endpoint.Service &&
			mapping.Endpoint.Api == endpoint.Api &&
			mapping.Endpoint.Name == endpoint.Name {
			return mapping, nil

		}
	}
	return nil, w.NewError("no network mapping for endpoint: %s", EndpointFromProto(endpoint).Unique())
}

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
		summaries = append(summaries, NetworkInstanceSummary(instance))
	}
	return fmt.Sprintf("%s:%s", EndpointDestination(mapping.Endpoint), strings.Join(summaries, ", "))
}

func NetworkInstanceSummary(value *basev0.NetworkInstance) string {
	return fmt.Sprintf("%s:%d (%s)", value.Hostname, value.Port, value.Access.Kind)
}

func networkMappingHash(n *basev0.NetworkMapping) string {
	return HashString(n.String())
}

func NetworkMappingHash(networkMappings ...*basev0.NetworkMapping) string {
	hasher := NewHasher()
	for _, networkMapping := range networkMappings {
		hasher.Add(networkMappingHash(networkMapping))
	}
	return hasher.Hash()
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
				Access:   instance.Access,
			})
		}
		results = append(results, &basev0.NetworkMapping{
			Endpoint:  mapping.Endpoint,
			Instances: instances,
		})
	}
	return results

}

func SplitPublicNetworkMappings(ctx context.Context, mappings []*basev0.NetworkMapping) ([]*basev0.NetworkMapping, []*basev0.NetworkMapping, error) {
	var public []*basev0.NetworkMapping
	var nonPublic []*basev0.NetworkMapping
	for _, mapping := range mappings {
		if mapping.Endpoint.Visibility == VisibilityPublic || mapping.Endpoint.Visibility == VisibilityExternal {
			public = append(public, mapping)
		} else {
			nonPublic = append(nonPublic, mapping)
		}
	}
	return public, nonPublic, nil
}
