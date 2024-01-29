package network

import (
	"fmt"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type MappingSummary struct {
	Count    int
	Mappings []string
}

func MappingAsString(mapping *basev0.NetworkMapping) string {
	return fmt.Sprintf("%s -> %s", configurations.EndpointDestination(mapping.Endpoint), mapping.Addresses)
}

func MakeNetworkMappingSummary(mappings []*basev0.NetworkMapping) MappingSummary {
	sum := MappingSummary{}
	sum.Count = len(mappings)
	for _, mapping := range mappings {
		sum.Mappings = append(sum.Mappings, MappingAsString(mapping))
	}
	return sum
}
