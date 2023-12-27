package network

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/wool"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
)

type RandomStrategy struct{}

func (r RandomStrategy) Reserve(ctx context.Context, host string, endpoints []ApplicationEndpoint) (*ApplicationEndpointInstances, error) {
	w := wool.Get(ctx).In("network.RandomStrategy.Reserve")
	ports, err := GetFreePorts(len(endpoints))
	if err != nil {
		return nil, w.Wrapf(err, "cannot get free ports")
	}
	m := &ApplicationEndpointInstances{}
	for i, port := range ports {
		m.ApplicationEndpointInstances = append(m.ApplicationEndpointInstances, &ApplicationEndpointInstance{
			ApplicationEndpoint: endpoints[i],
			Port:                port,
			Host:                host,
		})
	}
	return m, nil
}

type FixedStrategy struct{}

func toHundreds(s string) int {
	// Add a new SHA-256 hash.
	hasher := sha256.New()

	// Write the string to the hash.
	hasher.Write([]byte(s))

	// Get the hash sum.
	hash := hasher.Sum(nil)

	// Convert the first 4 bytes of the hash to an integer.
	num := binary.BigEndian.Uint32(hash[:4])

	// Map the number to the range [0, 999].
	return int(num % 1000)
}

func (r FixedStrategy) Reserve(host string, endpoints []ApplicationEndpoint) (*ApplicationEndpointInstances, error) {
	m := &ApplicationEndpointInstances{}
	for _, endpoint := range endpoints {
		m.ApplicationEndpointInstances = append(m.ApplicationEndpointInstances, &ApplicationEndpointInstance{
			ApplicationEndpoint: endpoint,
			Port:                11000 + toHundreds(endpoint.Unique()),
			Host:                host,
		})
	}
	return m, nil
}

// GetFreePorts returns a slice of n free ports
func GetFreePorts(n int) ([]int, error) {
	var ports []int
	for i := 0; i < n; i++ {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return nil, err
		}
		defer l.Close()

		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}

func NewServicePortManager(_ context.Context, identity *configurations.ServiceIdentity, endpoints ...*basev1.Endpoint) (*ServiceManager, error) {
	return &ServiceManager{
		service:   identity,
		endpoints: endpoints,
		strategy:  &FixedStrategy{},
		ids:       make(map[string]int),
		host:      "localhost",
	}, nil
}
