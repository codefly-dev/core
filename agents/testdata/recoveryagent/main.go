package main

import (
	"context"

	"github.com/codefly-dev/core/agents"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type server struct {
	agentv0.UnimplementedAgentServer
}

func (server) GetAgentInformation(context.Context, *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return &agentv0.AgentInformation{}, nil
}

func main() { agents.Serve(agents.PluginRegistration{Agent: server{}}) }
