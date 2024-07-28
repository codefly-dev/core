package network

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/standards"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type DeployManager struct {
	dnsManager DNSManager
}

var _ Manager = &DeployManager{}

func (m *DeployManager) GetNamespace(_ context.Context, env *resources.Environment, workspace *resources.Workspace, service *resources.ServiceIdentity) (string, error) {
	if workspace.Layout == resources.LayoutKindFlat {
		return fmt.Sprintf("%s-%s", workspace.Name, env.Name), nil
	}
	return fmt.Sprintf("%s-%s-%s", workspace.Name, service.Module, env.Name), nil
}

func (m *DeployManager) KubernetesService(service *resources.ServiceIdentity, endpoint *basev0.Endpoint, namespace string, port uint16) *basev0.NetworkInstance {
	host := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, namespace)
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = resources.NewNetworkInstance(host, port)
	}
	instance.Access = resources.NewContainerNetworkAccess()
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *DeployManager) GenerateNetworkMappings(ctx context.Context,
	env *resources.Environment,
	workspace *resources.Workspace,
	service *resources.ServiceIdentity,
	endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	var out []*basev0.NetworkMapping
	for _, endpoint := range endpoints {
		nm := &basev0.NetworkMapping{
			Endpoint: endpoint,
		}
		// Get DNS name for external endpoints
		if endpoint.Visibility == resources.VisibilityExternal {
			dns, err := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
			if err != nil {
				if !env.Local() {
					return nil, err
				}
				w.Warn("using named port")
				port := ToNamedPort(ctx, workspace.Name, service.Module, service.Name, endpoint.Name, endpoint.Api)
				dns = &basev0.DNS{
					Name:     service.Unique(),
					Module:   service.Module,
					Service:  service.Name,
					Endpoint: endpoint.Name,
					Host:     "localhost",
					Port:     uint32(port),
					Secured:  false,
				}
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
		if endpoint.Visibility == resources.VisibilityPublic {
			var dns *basev0.DNS
			var err error
			dns, err = m.dnsManager.GetDNS(ctx, service, endpoint.Name)
			if err != nil {
				// For local* environment, just use named port mapping
				if !env.Local() {
					return nil, err
				}
				w.Warn("using named port")
				port = ToNamedPort(ctx, workspace.Name, service.Module, service.Name, endpoint.Name, endpoint.Api)
				dns = &basev0.DNS{
					Name:     service.Unique(),
					Module:   service.Module,
					Service:  service.Name,
					Endpoint: endpoint.Name,
					Host:     "host.docker.internal",
					Port:     uint32(port),
					Secured:  false,
				}
			}
			if dns == nil {
				return nil, w.NewError("cannot find dns for endpoint %s", endpoint.Name)
			}
			nm.Instances = []*basev0.NetworkInstance{
				PublicInstance(DNS(service, endpoint, dns)),
			}
			w.Debug("will expose public endpoint to load balancer", wool.Field("dns", dns))
		}
		namespace, err := m.GetNamespace(ctx, env, workspace, service)
		if err != nil {
			return nil, err
		}
		nm.Instances = append(nm.Instances, ContainerInstance(m.KubernetesService(service, endpoint, namespace, port)))
		out = append(out, nm)
	}
	return out, nil
}

//
//func LoadBalanced(_ context.Context, env *resources.Environment, service *resources.Service, endpoint *basev0.Endpoint) *basev0.NetworkInstance {
//	host := fmt.Sprintf("kopkfeqwuk-%s-%s-%s-%s.%s", env.Name, service.Name, service.Module, service., env.LoadBalancer)
//	var instance *basev0.NetworkInstance
//	if endpoint.API == standards.HTTP || endpoint.API == standards.REST {
//		instance = resources.NewHTTPNetworkInstance(host, 443, true)
//	} else {
//		panic("only load balance http and rest for now")
//	}
//	return instance
//}

func NewDeployManager(_ context.Context, dnsManager DNSManager) (*DeployManager, error) {
	return &DeployManager{dnsManager: dnsManager}, nil
}
