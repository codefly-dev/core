package services

import (
	"context"

	coreservices "github.com/codefly-dev/core/agents/services"
	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// LoadCode creates a CodeAgent from the cached agent connection.
func LoadCode(ctx context.Context, service *resources.Service) (*coreservices.CodeAgent, error) {
	if service == nil {
		return nil, wool.Get(ctx).NewError("service cannot be nil")
	}
	if service.Agent == nil {
		return nil, wool.Get(ctx).NewError("agent cannot be nil")
	}

	conn := getConn(service.Agent)

	codeAgent := coreservices.NewCodeAgentClient(conn.GRPCConn())
	codeAgent.Agent = service.Agent
	codeAgent.ProcessInfo = conn.ProcessInfo()

	return codeAgent, nil
}
