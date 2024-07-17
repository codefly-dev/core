package services

import (
	"context"

	"github.com/codefly-dev/core/agents"
	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/manager"
	coreservices "github.com/codefly-dev/core/agents/services"
)

var buildersCache map[string]*coreservices.BuilderAgent
var buildersPid map[string]int

func init() {
	buildersCache = make(map[string]*coreservices.BuilderAgent)
	buildersPid = make(map[string]int)
}

func LoadBuilder(ctx context.Context, service *resources.Service) (*coreservices.BuilderAgent, error) {
	if service == nil {
		return nil, wool.Get(ctx).NewError("service cannot be nil")
	}
	if service.Agent == nil {
		return nil, wool.Get(ctx).NewError("agent cannot be nil")
	}
	w := wool.Get(ctx).In("services.LoadBuilder", wool.ServiceField(service.Name))

	identity, err := service.Identity()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get service identity")
	}

	if builder, ok := buildersCache[identity.Unique()]; ok {
		return builder, nil
	}

	builder, process, err := manager.Load[coreservices.ServiceBuilderAgentContext, coreservices.BuilderAgent](ctx, service.Agent.Of(resources.BuilderServiceAgent), identity.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service builder service")
	}

	buildersPid[identity.Unique()] = process.PID

	builder.Agent = service.Agent
	builder.ProcessInfo = process

	w.Debug("loaded builder", wool.Field("builder-pid", process.PID))

	buildersCache[identity.Unique()] = builder
	return builder, nil
}

func NewBuilderAgent(conf *resources.Agent, builder coreservices.Builder) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &coreservices.BuilderAgentGRPC{Builder: builder},
	}
}
