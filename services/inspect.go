package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents/contract"
	"github.com/codefly-dev/core/agents/manager"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
)

// InspectAgent obtains capabilities from the selected running service agent,
// checks its protocol, and closes the process without invoking lifecycle RPCs.
// The caller's selection is never mutated, and an incompatible selection is
// never silently replaced with a different release.
func InspectAgent(ctx context.Context, selected *resources.Agent) (*resources.Agent, *agentv0.AgentInformation, error) {
	if selected == nil {
		return nil, nil, fmt.Errorf("agent is required")
	}
	agent := *selected
	if _, err := manager.ResolveLatest(ctx, &agent); err != nil {
		return nil, nil, err
	}
	conn, err := manager.Load(ctx, &agent, manager.WithoutSandbox(), manager.WithoutPrincipal())
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	info, err := agentv0.NewAgentClient(conn.GRPCConn()).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect agent %s: %w", &agent, err)
	}
	if err := contract.Check(info.GetContract()); err != nil {
		return nil, nil, fmt.Errorf("incompatible agent %s: %w", &agent, err)
	}
	return &agent, info, nil
}
