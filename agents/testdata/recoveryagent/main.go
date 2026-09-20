package main

import (
	"context"
	"os"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type server struct {
	agentv0.UnimplementedAgentServer
}

var information = func() *agentv0.AgentInformation {
	declaration := contract.Current()
	switch os.Getenv("TEST_AGENT_CONTRACT") {
	case "absent":
		return &agentv0.AgentInformation{}
	case "future":
		declaration.ProtocolVersion++
	case "future-startup":
		declaration.StartupProtocolVersion++
	case "undeclared-startup":
		declaration.StartupProtocolVersion = 0
	case "undeclared":
		declaration.ProtocolVersion = 0
	case "no-recovery":
		declaration.Capabilities = nil
	}
	declaration.Capabilities = append(declaration.Capabilities, "fixture-feature/v1")
	return &agentv0.AgentInformation{Contract: declaration}
}()

func (server) GetAgentInformation(context.Context, *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return information, nil
}

func main() { agents.Serve(agents.PluginRegistration{Agent: server{}}) }
