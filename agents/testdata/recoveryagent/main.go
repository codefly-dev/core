package main

import (
	"context"
	"os"
	"time"

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
	info := &agentv0.AgentInformation{Contract: declaration}
	switch os.Getenv("TEST_AGENT_CONTRACT") {
	case "sdk-compatible":
		info.Capabilities = []*agentv0.Capability{{Type: agentv0.Capability_BUILDER}, {Type: agentv0.Capability_RUNTIME}}
	case "sdk-no-builder":
		info.Capabilities = []*agentv0.Capability{{Type: agentv0.Capability_RUNTIME}}
	case "sdk-no-runtime":
		info.Capabilities = []*agentv0.Capability{{Type: agentv0.Capability_BUILDER}}
	}
	return info
}()

func (server) GetAgentInformation(context.Context, *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return information, nil
}

func main() {
	if gate := os.Getenv("TEST_AGENT_START_GATE"); gate != "" {
		if err := os.WriteFile(gate+".started", nil, 0o600); err != nil {
			panic(err)
		}
		for {
			if _, err := os.Stat(gate); err == nil {
				break
			} else if !os.IsNotExist(err) {
				panic(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	agents.Serve(agents.PluginRegistration{Agent: server{}})
}
