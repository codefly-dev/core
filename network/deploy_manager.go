package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type DeployManager struct {
}

func Namespace(service *configurations.Service) string {
	return service.Application
}

func KubernetesService(service *configurations.Service, port uint16) *basev0.NetworkInstance {
	instance := &basev0.NetworkInstance{
		Scope: basev0.RuntimeScope_Container,
		Port:  uint32(port),
		Host:  fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, Namespace(service)),
	}
	instance.Address = fmt.Sprintf("%s:%d", instance.Host, instance.Port)
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *DeployManager) GenerateNetworkMappings(_ context.Context, service *configurations.Service, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
	//w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	var out []*basev0.NetworkMapping
	for _, endpoint := range endpoints {
		// Get canonical port
		port := standards.Port(endpoint.Api)
		nm := &basev0.NetworkMapping{
			Endpoint: endpoint,
			Instances: []*basev0.NetworkInstance{
				KubernetesService(service, port),
			},
		}
		out = append(out, nm)
	}
	return out, nil
}

func NewDeployManager(_ context.Context) (*DeployManager, error) {
	return &DeployManager{}, nil
}
