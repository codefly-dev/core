package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents/endpoints"

	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	servicev1 "github.com/codefly-dev/core/proto/v1/go/services"
	"github.com/codefly-dev/core/shared"
)

type DNS struct{}

func (r DNS) Reserve(_ string, es []ApplicationEndpoint) (*ApplicationEndpointInstances, error) {
	m := &ApplicationEndpointInstances{}
	for _, e := range es {
		port, err := endpoints.StandardPort(e.Endpoint.Api)
		if err != nil {
			return nil, err
		}
		m.ApplicationEndpointInstances = append(m.ApplicationEndpointInstances, &ApplicationEndpointInstance{
			ApplicationEndpoint: e,
			Port:                port,
			Host:                fmt.Sprintf("%s.%s.svc.cluster.local", e.Unique(), e.Namespace),
		})
	}
	return m, nil
}

func NewServiceDnsManager(ctx context.Context, identity *servicev1.ServiceIdentity, endpoints ...*basev1.Endpoint) (*ServiceManager, error) {
	logger := shared.NewLogger("network.NewServicePortManager<%s>", identity.Name)
	return &ServiceManager{
		logger:    logger,
		service:   identity,
		endpoints: endpoints,
		strategy:  &FixedStrategy{},
		ids:       make(map[string]int),
	}, nil
}
