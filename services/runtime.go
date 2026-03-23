package services

import (
	"context"

	coreservices "github.com/codefly-dev/core/agents/services"
	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/wool"
)

// LoadRuntime creates a RuntimeAgent from the cached agent connection.
func LoadRuntime(ctx context.Context, service *resources.Service) (*coreservices.RuntimeAgent, error) {
	if service == nil {
		return nil, wool.Get(ctx).NewError("service cannot be nil")
	}
	if service.Agent == nil {
		return nil, wool.Get(ctx).NewError("agent cannot be nil")
	}

	conn := getConn(service.Agent)

	runtime := coreservices.NewRuntimeAgentClient(conn.GRPCConn())
	runtime.Agent = service.Agent
	runtime.ProcessInfo = conn.ProcessInfo()

	return runtime, nil
}

