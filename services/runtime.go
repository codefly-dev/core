package services

import (
	"context"

	coreservices "github.com/codefly-dev/core/agents/services"
	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// LoadRuntime creates a RuntimeAgent from the cached agent connection.
func LoadRuntime(ctx context.Context, service *resources.Service) (*coreservices.RuntimeAgent, error) {
	if service == nil {
		return nil, wool.Get(ctx).NewError("service cannot be nil")
	}
	if service.Agent == nil {
		return nil, wool.Get(ctx).NewError("agent cannot be nil")
	}

	conn, err := getConn(ctx, ServiceCacheKey(service), service.Agent)
	if err != nil {
		return nil, err
	}

	runtime := coreservices.NewRuntimeAgentClient(conn.GRPCConn())
	selection := *service.Agent
	runtime.Agent = &selection
	runtime.ProcessInfo = conn.ProcessInfo()

	return runtime, nil
}
