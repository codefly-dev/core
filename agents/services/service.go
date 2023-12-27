package services

import (
	"context"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/wool"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"

	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"

	"github.com/codefly-dev/core/configurations"
	v1agent "github.com/codefly-dev/core/generated/go/services/agent/v1"
	factoryv1 "github.com/codefly-dev/core/generated/go/services/factory/v1"
)

type ProcessInfo struct {
	AgentPID int
	Trackers []*runtimev1.Tracker
}

type ServiceInstance struct {
	*configurations.Service
	Agent   Agent
	Factory *FactoryInstance
	Runtime *RuntimeInstance
	ProcessInfo
}

type FactoryInstance struct {
	*configurations.Service
	Factory
}

type RuntimeInstance struct {
	*configurations.Service
	Runtime
}

// Factory methods

func (instance *FactoryInstance) Load(ctx context.Context) (*factoryv1.LoadResponse, error) {
	init := &factoryv1.LoadRequest{
		Debug: wool.IsDebug(),
		Identity: &basev1.ServiceIdentity{
			Name:        instance.Name,
			Application: instance.Application,
			Domain:      instance.Domain,
			Namespace:   instance.Namespace,
			Location:    instance.Dir(),
		},
	}
	return instance.Factory.Load(ctx, init)

}

func (instance *FactoryInstance) Create(ctx context.Context) (*factoryv1.CreateResponse, error) {
	return instance.Factory.Create(ctx, &factoryv1.CreateRequest{})
}

// Runtime methods

func (instance *RuntimeInstance) Load(ctx context.Context) (*runtimev1.LoadResponse, error) {
	init := &runtimev1.LoadRequest{
		Debug: wool.IsDebug(),
		Identity: &basev1.ServiceIdentity{
			Name:        instance.Name,
			Application: instance.Application,
			Domain:      instance.Domain,
			Namespace:   instance.Namespace,
			Location:    instance.Dir(),
		},
	}
	return instance.Runtime.Load(ctx, init)
}

// Loader

func Load(ctx context.Context, service *configurations.Service) (*ServiceInstance, error) {
	w := wool.Get(ctx).In("services.Init", wool.Field("service", service.Name))
	agent, proc, err := manager.Load[ServiceAgentContext, ServiceAgent](ctx, service.Agent, service.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service agent")
	}
	// Init capabilities
	instance := &ServiceInstance{
		Service: service,
		Agent:   agent,
	}
	instance.ProcessInfo.AgentPID = proc.PID

	info, err := agent.GetAgentInformation(ctx, &v1agent.AgentInformationRequest{})
	if err != nil {
		return nil, w.Wrapf(err, "cannot get agent information")
	}

	for _, capability := range info.Capabilities {
		switch capability.Type {
		case v1agent.Capability_FACTORY:
			err = instance.LoadFactory(ctx, service)
			if err != nil {
				return nil, w.Wrapf(err, "cannot provide factory")
			}
		case v1agent.Capability_RUNTIME:
			err = instance.LoadRuntime(ctx, service)
			if err != nil {
				return nil, w.Wrapf(err, "cannot provide runtime")
			}
		}

	}
	return instance, nil
}

func (instance *ServiceInstance) LoadFactory(ctx context.Context, service *configurations.Service) error {
	w := wool.Get(ctx).In("ServiceInstance::LoadFactory", wool.NameField(service.Unique()))
	factory, err := LoadFactory(ctx, service)
	if err != nil {
		return w.Wrapf(err, "cannot load factory")
	}
	instance.Factory = &FactoryInstance{Service: service, Factory: factory}
	return nil
}

func (instance *ServiceInstance) LoadRuntime(ctx context.Context, service *configurations.Service) error {
	w := wool.Get(ctx).In("ServiceInstance::LoadRuntime", wool.NameField(service.Unique()))
	runtime, err := LoadRuntime(ctx, service)
	if err != nil {
		return w.Wrapf(err, "cannot load runtime")
	}
	instance.Runtime = &RuntimeInstance{Service: service, Runtime: runtime}
	return nil
}

func UpdateAgent(ctx context.Context, service *configurations.Service) error {
	w := wool.Get(ctx).In("ServiceInstance::Update")
	// Fetch the latest agent version
	err := manager.PinToLatestRelease(ctx, service.Agent)
	if err != nil {
		return w.Wrap(err)
	}
	err = service.Save(ctx)
	if err != nil {
		return w.Wrap(err)
	}
	return nil
}
