package services

import (
	"context"
	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	v1 "github.com/codefly-dev/core/proto/v1/go/services"
	v1agent "github.com/codefly-dev/core/proto/v1/go/services/agent"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	"github.com/codefly-dev/core/shared"
)

type ServiceInstance struct {
	*configurations.Service
	Agent   Agent
	Factory Factory
	Runtime Runtime
}

/* Factory methods */

type InitOutput struct {
	Readme string
}

func (instance *ServiceInstance) Init(ctx context.Context) (*InitOutput, error) {
	logger := shared.NewLogger().With("agents.FactoryInit<%s||%s>", instance.Unique(), instance.Service.Agent.Identifier())
	init, err := instance.Factory.Init(ctx, &v1.InitRequest{
		Debug:    shared.IsDebug(),
		Location: instance.Dir(),
		Identity: &v1.ServiceIdentity{
			Name:        instance.Name,
			Application: instance.Application,
			Domain:      instance.Domain,
			Namespace:   instance.Namespace,
		},
	})

	if err != nil {
		return nil, logger.Wrapf(err, "init failed")
	}
	logger.DebugMe("init successful")

	_, err = instance.Factory.Create(ctx, &factoryv1.CreateRequest{})
	if err != nil {
		return nil, logger.Wrapf(err, "create failed")
	}
	return &InitOutput{Readme: init.ReadMe}, nil
}

type CreateOutput struct {
}

func (instance *ServiceInstance) Create(ctx context.Context) (*CreateOutput, error) {
	logger := shared.NewLogger().With("agents.FactoryCreate<%s||%s>", instance.Unique(), instance.Service.Agent.Identifier())

	_, err := instance.Factory.Create(ctx, &factoryv1.CreateRequest{})
	if err != nil {
		return nil, logger.Wrapf(err, "create failed")
	}
	return &CreateOutput{}, nil
}

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
	instance.Factory = factory
	return nil
}
