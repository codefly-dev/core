package network

import (
	"strings"

	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
)

func LocalizeMappings(nm []*runtimev0.NetworkMapping, local string) []*runtimev0.NetworkMapping {
	var localized []*runtimev0.NetworkMapping
	for _, mapping := range nm {
		localized = append(localized, LocalizeMapping(mapping, local))
	}
	return localized
}

func LocalizeMapping(mapping *runtimev0.NetworkMapping, local string) *runtimev0.NetworkMapping {
	var addresses []string
	for _, addr := range mapping.Addresses {
		addresses = append(addresses, strings.Replace(addr, "localhost", local, 1))
	}
	return &runtimev0.NetworkMapping{
		Application: mapping.Application,
		Service:     mapping.Service,
		Endpoint:    mapping.Endpoint,
		Addresses:   addresses,
	}
}
