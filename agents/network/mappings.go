package network

import (
	"strings"

	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
)

func LocalizeMappings(nm []*runtimev1.NetworkMapping, local string) []*runtimev1.NetworkMapping {
	var localized []*runtimev1.NetworkMapping
	for _, mapping := range nm {
		localized = append(localized, LocalizeMapping(mapping, local))
	}
	return localized
}

func LocalizeMapping(mapping *runtimev1.NetworkMapping, local string) *runtimev1.NetworkMapping {
	var addresses []string
	for _, addr := range mapping.Addresses {
		addresses = append(addresses, strings.Replace(addr, "localhost", local, 1))
	}
	return &runtimev1.NetworkMapping{
		Application: mapping.Application,
		Service:     mapping.Service,
		Endpoint:    mapping.Endpoint,
		Addresses:   addresses,
	}
}
