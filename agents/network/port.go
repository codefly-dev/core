package network

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/wool"
)

func GetAllLocalIPs() ([]string, error) {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}

	return ips, nil
}

type FixedStrategy struct{}

func HashInt(s string, low, high int) int {
	hasher := sha256.New()
	hasher.Write([]byte(s))
	hash := hasher.Sum(nil)
	num := binary.BigEndian.Uint32(hash)
	return int(num%uint32(high-low)) + low
}

func APIInt(api string) int {
	switch api {
	case standards.TCP:
		return 0
	case standards.HTTP:
		return 1
	case standards.REST:
		return 2
	case standards.GRPC:
		return 3
	default:
		return 0
	}
}

// ToPort strategy:
// APP-SVC-API
// Between 1100(0) and 4999(9)
// First 11 -> 49: hash app
// Next 0 -> 99: hash svc
// Last Digit: API
// 0: TCP
// 1: HTTP/ REST
// 2: gRPC
func ToPort(app string, svc string, api string) int {
	return HashInt(app, 11, 49)*1000 + HashInt(svc, 0, 99)*10 + APIInt(api)
}

func (r FixedStrategy) Reserve(ctx context.Context, host string, endpoints []*ApplicationEndpoint) (*ApplicationEndpointInstances, error) {
	w := wool.Get(ctx).In("FixedStrategy.Reserve")
	m := &ApplicationEndpointInstances{}
	for _, endpoint := range endpoints {
		api, err := configurations.WhichAPI(endpoint.Endpoint.Api)
		if err != nil {
			return nil, w.Wrapf(err, "cannot get api")
		}
		port := ToPort(endpoint.Application, endpoint.Service, api)
		w.Focus("port", wool.ThisField(endpoint), wool.Field("port", port))
		m.ApplicationEndpointInstances = append(m.ApplicationEndpointInstances,
			&ApplicationEndpointInstance{
				ApplicationEndpoint: endpoint,
				Port:                port,
				Host:                host,
			})
	}
	return m, nil
}

func NewServicePortManager(_ context.Context) (*ServiceManager, error) {
	return &ServiceManager{
		strategy: &FixedStrategy{},
		ids:      make(map[string]int),
		host:     "localhost",
	}, nil
}
