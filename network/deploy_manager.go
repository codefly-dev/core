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

func (m *DeployManager) GetNamespace(_ context.Context, service *configurations.Service, env *configurations.Environment) (string, error) {
	return fmt.Sprintf("%s-%s-%s", service.Project, service.Application, env.Name), nil
}

func (m *DeployManager) KubernetesService(service *configurations.Service, endpoint *basev0.Endpoint, namespace string, port uint16) *basev0.NetworkInstance {
	host := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, namespace)
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, port, false)
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
			var dns *basev0.DNS
			var err error
			if env.LoadBalancer != "" {
				host := fmt.Sprintf("kopkfeqwuk-%s-%s-%s-%s.%s", env.Name, service.Name, service.Application, service.Project, env.LoadBalancer)
				dns = &basev0.DNS{
					Host:    host,
					Port:    443,
					Secured: true,
				}
				nm.Instances = []*basev0.NetworkInstance{
					PublicInstance(LoadBalanced(ctx, env, service, endpoint)),
				}
			} else {
				// Case without Load Balancer
				dns, err = m.dnsManager.GetDNS(ctx, service, endpoint.Name)
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
			w.Focus("will expose public endpoint to load balancer", wool.Field("dns", dns))
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

func LoadBalanced(_ context.Context, env *configurations.Environment, service *configurations.Service, endpoint *basev0.Endpoint) *basev0.NetworkInstance {
	host := fmt.Sprintf("kopkfeqwuk-%s-%s-%s-%s.%s", env.Name, service.Name, service.Application, service.Project, env.LoadBalancer)
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = configurations.NewHTTPNetworkInstance(host, 443, true)
	} else {
		panic("only load balance http and rest for now")
	}
	return instance
}

func NewDeployManager(_ context.Context, dnsManager DNSManager) (*DeployManager, error) {
	return &DeployManager{dnsManager: dnsManager}, nil
}
