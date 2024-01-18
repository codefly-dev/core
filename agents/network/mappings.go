package network

import (
	"fmt"
	"strconv"
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

type MappingInstance struct {
	Address string
	Port    int
}

func Instance(mappings []*runtimev0.NetworkMapping) (*MappingInstance, error) {
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no network mappings")
	}
	m := mappings[0]
	if len(m.Addresses) == 0 {
		return nil, fmt.Errorf("no network addresses")
	}
	address := m.Addresses[0]
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
