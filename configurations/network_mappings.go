package configurations

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

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
