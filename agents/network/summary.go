package network

import (
	"fmt"

	"github.com/codefly-dev/core/configurations"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
)

type MappingSummary struct {
	Count    int
	Mappings []string
}

func MappingAsString(mapping *runtimev1.NetworkMapping) string {
	return fmt.Sprintf("%s -> %s", configurations.EndpointDestination(mapping.Endpoint), mapping.Addresses)
}

func MakeNetworkMappingSummary(mappings []*runtimev1.NetworkMapping) MappingSummary {
	sum := MappingSummary{}
	sum.Count = len(mappings)
	for _, mapping := range mappings {
		sum.Mappings = append(sum.Mappings, MappingAsString(mapping))
	}
	return sum
}
