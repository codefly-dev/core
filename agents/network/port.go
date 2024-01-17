package network

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"

	"github.com/codefly-dev/core/wool"
)

type RandomStrategy struct{}

func (r RandomStrategy) Reserve(ctx context.Context, host string, endpoints []*ApplicationEndpoint) (*ApplicationEndpointInstances, error) {
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

func toThousands(s string) int {
	// Add a new SHA-256 hash.
	hasher := sha256.New()

	// Write the string to the hash.
	hasher.Write([]byte(s))

	// Get the hash sum.
	hash := hasher.Sum(nil)

	num := binary.BigEndian.Uint32(hash)

	// Map the number to the range [0, 9999].
	return int(num % 10000)
}

func (r FixedStrategy) Reserve(ctx context.Context, host string, endpoints []*ApplicationEndpoint) (*ApplicationEndpointInstances, error) {
	m := &ApplicationEndpointInstances{}
	for _, endpoint := range endpoints {
		m.ApplicationEndpointInstances = append(m.ApplicationEndpointInstances, &ApplicationEndpointInstance{
			ApplicationEndpoint: endpoint,
			Port:                10000 + toThousands(endpoint.Unique()),
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

func NewServicePortManager(_ context.Context) (*ServiceManager, error) {
	return &ServiceManager{
		strategy: &FixedStrategy{},
		ids:      make(map[string]int),
		host:     "localhost",
	}, nil
}
