package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents/endpoints"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
)

type DNS struct{}

func ToDNS(e *basev1.Endpoint) string {
	return fmt.Sprintf("%s-%s.%s.svc.cluster.local", e.Service, e.Application, e.Namespace)
}

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
			Host:                ToDNS(e.Endpoint),
		})
	}
	return m, nil
}

func NewServiceDNSManager(_ context.Context, identity *basev1.ServiceIdentity, endpoints ...*basev1.Endpoint) (*ServiceManager, error) {
	return &ServiceManager{
		service:   identity,
		endpoints: endpoints,
		strategy:  &DNS{},
		ids:       make(map[string]int),
	}, nil
}
