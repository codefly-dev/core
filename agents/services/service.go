package services

import (
	"context"
	basev1 "github.com/codefly-dev/core/generated/v1/go/proto/base"

	runtimev1 "github.com/codefly-dev/core/generated/v1/go/proto/services/runtime"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	v1agent "github.com/codefly-dev/core/generated/v1/go/proto/services/agent"
	factoryv1 "github.com/codefly-dev/core/generated/v1/go/proto/services/factory"
	"github.com/codefly-dev/core/shared"
)

type ServiceInstance struct {
	*configurations.Service
	Agent   Agent
	Factory *FactoryInstance
	Runtime *RuntimeInstance
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

func (instance *FactoryInstance) Init(ctx context.Context) (*factoryv1.InitResponse, error) {
	init := &factoryv1.InitRequest{
		Debug: shared.IsDebug(),
		Identity: &basev1.ServiceIdentity{
			Name:        instance.Name,
			Application: instance.Application,
			Domain:      instance.Domain,
			Namespace:   instance.Namespace,
			Location:    instance.Dir(),
		},
	}
	return instance.Factory.Init(ctx, init)

}

func (instance *FactoryInstance) Create(ctx context.Context) (*factoryv1.CreateResponse, error) {
	return instance.Factory.Create(ctx, &factoryv1.CreateRequest{})
}

// Runtime methods

func (instance *RuntimeInstance) Init(ctx context.Context) (*runtimev1.InitResponse, error) {
	init := &runtimev1.InitRequest{
		Debug: shared.IsDebug(),
		Identity: &basev1.ServiceIdentity{
			Name:        instance.Name,
			Application: instance.Application,
			Domain:      instance.Domain,
			Namespace:   instance.Namespace,
			Location:    instance.Dir(),
		},
	}
	return instance.Runtime.Init(ctx, init)
}

// Loader

func Load(ctx context.Context, service *configurations.Service) (*ServiceInstance, error) {
	logger := shared.NewLogger().With("agents.Load<%s>", service.Unique())
	agent, err := agents.Load[ServiceAgentContext, ServiceAgent](ctx, service.Agent, service.Unique())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service agent")
	}
	// Load capabilities
	instance := &ServiceInstance{
		Service: service,
		Agent:   agent,
	}

	info, err := agent.GetAgentInformation(ctx, &v1agent.AgentInformationRequest{})
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get agent information")
	}

	for _, capability := range info.Capabilities {
		switch capability.Type {
		case v1agent.Capability_FACTORY:
			err = instance.LoadFactory(ctx, service)
			if err != nil {
				return nil, logger.Wrapf(err, "cannot provide factory")
			}
		case v1agent.Capability_RUNTIME:
			err = instance.LoadRuntime(ctx, service)
			if err != nil {
				return nil, logger.Wrapf(err, "cannot provide runtime")
			}
		}

	}
	return instance, nil
}

func (instance *ServiceInstance) LoadFactory(ctx context.Context, service *configurations.Service) error {
	logger := shared.NewLogger().With("agents.LoadFactory<%s>", service.Unique())
	factory, err := LoadFactory(ctx, service)
	if err != nil {
		return logger.Wrapf(err, "cannot load factory")
	}
	instance.Factory = &FactoryInstance{Service: service, Factory: factory}
	return nil
}

func (instance *ServiceInstance) LoadRuntime(ctx context.Context, service *configurations.Service) error {
	logger := shared.NewLogger().With("agents.LoadRuntime<%s>", service.Unique())
	runtime, err := LoadRuntime(ctx, service)
	if err != nil {
		return logger.Wrapf(err, "cannot load runtime")
	}
	instance.Runtime = &RuntimeInstance{Service: service, Runtime: runtime}
	return nil
}
