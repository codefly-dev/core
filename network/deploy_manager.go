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

var loadBalancer string

func SetLoadBalancer(lb string) {
	loadBalancer = lb
}

func (m *DeployManager) GetNamespace(_ context.Context, service *configurations.Service, env *configurations.Environment) (string, error) {
	return fmt.Sprintf("%s-%s-%s", service.Project, service.Application, env.Name), nil
}

func (m *DeployManager) KubernetesService(service *configurations.Service, endpoint *basev0.Endpoint, namespace string, port uint16) *basev0.NetworkInstance {
	host := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, namespace)
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, port)
	} else {
		instance = configurations.NewNetworkInstance(host, port)
	}
	instance.Scope = basev0.NetworkScope_Container
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *DeployManager) GenerateNetworkMappings(ctx context.Context, service *configurations.Service, endpoints []*basev0.Endpoint, env *configurations.Environment) ([]*basev0.NetworkMapping, error) {
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
				ExternalInstance(DNS(service, endpoint, dns)),
			}
			out = append(out, nm)
			continue
		}
		// Get canonical port
		port := standards.Port(endpoint.Api)
		if endpoint.Visibility == configurations.VisibilityPublic {

			if loadBalancer != "" {
				nm.Instances = []*basev0.NetworkInstance{
					PublicInstance(LoadBalanced(service, loadBalancer, endpoint, port)),
				}
				out = append(out, nm)
			} else {
				dns, err := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
				if err != nil {
					return nil, err
				}
				if dns == nil {
					return nil, w.NewError("cannot find dns for endpoint %s", endpoint.Name)
				}
				nm.Instances = []*basev0.NetworkInstance{
					PublicInstance(DNS(service, endpoint, dns)),
				}
			}
		}
		namespace, err := m.GetNamespace(ctx, service, env)
		if err != nil {
			return nil, err
		}
		nm.Instances = append(nm.Instances, ContainerInstance(m.KubernetesService(service, endpoint, namespace, port)))
		out = append(out, nm)
	}
	return out, nil
}

func LoadBalanced(service *configurations.Service, balancer string, endpoint *basev0.Endpoint, port uint16) *basev0.NetworkInstance {
	host := fmt.Sprintf("%s-%s-%s.%s", service.Name, service.Application, service.Project, balancer)
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, port)
	} else {
		instance = configurations.NewNetworkInstance(host, port)
	}
	return instance
}

func NewDeployManager(_ context.Context, dnsManager DNSManager) (*DeployManager, error) {
	return &DeployManager{dnsManager: dnsManager}, nil
}
