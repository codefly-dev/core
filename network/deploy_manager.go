package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type DeployManager struct {
	dnsManager DNSManager
}

func (m *DeployManager) GetNamespace(_ context.Context, service *configurations.Service, _ *configurations.Environment) (string, error) {
	return fmt.Sprintf("%s-%s", service.Project, service.Application), nil
}

func Namespace(service *configurations.Service) string {
	return service.Application
}

func KubernetesService(service *configurations.Service, port uint16) *basev0.NetworkInstance {
	host := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, Namespace(service))
	instance := configurations.NewNetworkInstance(host, port)
	instance.Scope = basev0.NetworkScope_Container
	instance.Address = fmt.Sprintf("%s:%d", instance.Host, instance.Port)
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *DeployManager) GenerateNetworkMappings(ctx context.Context, service *configurations.Service, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	var out []*basev0.NetworkMapping
	for _, endpoint := range endpoints {
		nm := &basev0.NetworkMapping{
			Endpoint: endpoint,
		}
		// Get DNS name for external endpoints
		if endpoint.Visibility == configurations.VisibilityExternal {
			dns, err := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
			if err != nil {
				return nil, err
			}
			if dns == nil {
				return nil, w.NewError("cannot find dns for endpoint %s", endpoint.Name)
			}
			nm.Instances = []*basev0.NetworkInstance{
				ExternalInstance(DNS(service, dns)),
			}
			out = append(out, nm)
			continue
		}
		if endpoint.Visibility == configurations.VisibilityPublic {
			dns, err := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
			if err != nil {
				return nil, err
			}
			if dns == nil {
				return nil, w.NewError("cannot find dns for endpoint %s", endpoint.Name)
			}
			nm.Instances = []*basev0.NetworkInstance{
				PublicInstance(DNS(service, dns)),
			}
			out = append(out, nm)
		}
		// Get canonical port
		port := standards.Port(endpoint.Api)
		nm.Instances = append(nm.Instances, ContainerInstance(KubernetesService(service, port)))
		out = append(out, nm)
	}
	return out, nil
}

func NewDeployManager(_ context.Context, dnsManager DNSManager) (*DeployManager, error) {
	return &DeployManager{dnsManager: dnsManager}, nil
}
