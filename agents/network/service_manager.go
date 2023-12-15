package network

import (
	basev1 "github.com/codefly-dev/core/generated/v1/go/proto/base"
	runtimev1 "github.com/codefly-dev/core/generated/v1/go/proto/services/runtime"
	"github.com/codefly-dev/core/shared"
)

// A ServiceManager helps go from a service to applications endpoint instances
type ServiceManager struct {
	service   *basev1.ServiceIdentity
	endpoints []*basev1.Endpoint

	strategy Strategy
	specs    []ApplicationEndpoint

	ids  map[string]int
	host string

	reserved *ApplicationEndpointInstances
	logger   shared.BaseLogger
}

func NewServiceManager(identity *basev1.ServiceIdentity, endpoints ...*basev1.Endpoint) *ServiceManager {
	logger := shared.NewLogger().With("network.NewServicePortManager<%s>", identity.Name)
	return &ServiceManager{
		logger:    logger,
		service:   identity,
		endpoints: endpoints,
		strategy:  &FixedStrategy{},
		ids:       make(map[string]int),
	}
}

func (pm *ServiceManager) Bind(endpoint *basev1.Endpoint, portBinding string) error {
	if endpoint == nil {
		return pm.logger.Errorf("cannot expose nil endpoint")
	}
	pm.logger.Tracef("binding endpoint <%v>", endpoint.Name)
	pm.specs = append(pm.specs,
		ApplicationEndpoint{
			Service:     pm.service.Name,
			Application: pm.service.Application,
			Namespace:   pm.service.Namespace,
			Endpoint:    endpoint,
			PortBinding: portBinding,
		})
	pm.ids[ToUnique(endpoint)]++
	return nil
}

func (pm *ServiceManager) Expose(endpoint *basev1.Endpoint) error {
	if endpoint == nil {
		return pm.logger.Errorf("cannot expose nil endpoint")
	}
	pm.logger.Tracef("exposing endpoint <%s>", endpoint.Name)
	pm.logger.TODO("Protocol from basev1.Endpoint")
	pm.specs = append(pm.specs,
		ApplicationEndpoint{
			Service:     pm.service.Name,
			Application: pm.service.Application,
			Namespace:   pm.service.Namespace,
			Endpoint:    endpoint,
		})
	pm.ids[ToUnique(endpoint)]++
	return nil
}

func (pm *ServiceManager) Reserve() error {
	m, err := pm.strategy.Reserve(pm.host, pm.specs)
	if err != nil {
		return pm.logger.Wrapf(err, "cannot reserve ports")
	}
	pm.reserved = m
	return nil
}

// NetworkMapping returns the network mapping for the service to be passed back to codefly
func (pm *ServiceManager) NetworkMapping() ([]*runtimev1.NetworkMapping, error) {
	pm.logger.TODO("fail over is broken")

	var nets []*runtimev1.NetworkMapping
	for _, instance := range pm.reserved.ApplicationEndpointInstances {
		nets = append(nets, &runtimev1.NetworkMapping{
			Application: instance.ApplicationEndpoint.Application,
			Service:     instance.ApplicationEndpoint.Service,
			Endpoint:    instance.ApplicationEndpoint.Endpoint,
			Addresses:   []string{instance.Address()},
		})
	}
	return nets, nil
}

func (pm *ServiceManager) ApplicationEndpointInstance(endpoint *basev1.Endpoint) (*ApplicationEndpointInstance, error) {
	var result *ApplicationEndpointInstance
	for _, e := range pm.reserved.ApplicationEndpointInstances {
		if ToUnique(e.ApplicationEndpoint.Endpoint) == ToUnique(endpoint) {
			if result != nil {
				return nil, pm.logger.Errorf("duplicate endpoint found <%s>", endpoint.Name)
			}
			result = e
		}
	}
	return result, nil
}

func (pm *ServiceManager) Port(endpoint *basev1.Endpoint) (int, error) {
	instance, err := pm.ApplicationEndpointInstance(endpoint)
	if err != nil {
		return 0, pm.logger.Wrapf(err, "cannot find endpoint <%s>", endpoint.Name)
	}
	return instance.Port, nil
}

func (pm *ServiceManager) ApplicationEndpointInstances() []*ApplicationEndpointInstance {
	return pm.reserved.ApplicationEndpointInstances
}
