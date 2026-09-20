package services

import (
	"context"

	coreservices "github.com/codefly-dev/core/agents/services"
	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// LoadBuilder creates a BuilderAgent from the cached agent connection.
func LoadBuilder(ctx context.Context, service *resources.Service) (*coreservices.BuilderAgent, error) {
	if service == nil {
		return nil, wool.Get(ctx).NewError("service cannot be nil")
	}
	if service.Agent == nil {
		return nil, wool.Get(ctx).NewError("agent cannot be nil")
	}

	conn, err := getConn(ServiceCacheKey(service), service.Agent)
	if err != nil {
		return nil, err
	}

	builder := coreservices.NewBuilderAgentClient(conn.GRPCConn())
	selection := *service.Agent
	builder.Agent = &selection
	builder.ProcessInfo = conn.ProcessInfo()

	return builder, nil
}
