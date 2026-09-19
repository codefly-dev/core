package main

import (
	"context"

	"github.com/codefly-dev/core/agents"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type server struct {
	agentv0.UnimplementedAgentServer
}

var information = &agentv0.AgentInformation{
	Contract: &agentv0.AgentContract{Capabilities: []string{"fixture-feature/v1"}},
}

func (server) GetAgentInformation(context.Context, *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return information, nil
}

func main() { agents.Serve(agents.PluginRegistration{Agent: server{}}) }
