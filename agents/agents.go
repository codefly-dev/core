package agents

import (
	"github.com/codefly-dev/core/resources"
	"github.com/hashicorp/go-plugin"
)

type Agent struct {
	Configuration  *resources.Agent
	Type           string
	Implementation plugin.Plugin
}

var HandshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "codefly::agents",
	MagicCookieValue: "0.0.0",
}

type AgentImplementation struct {
	Agent         plugin.Plugin
	Configuration *resources.Agent
}

func Register(agents ...AgentImplementation) {
	agentMap := make(map[string]plugin.Plugin)
	for _, p := range agents {
		agentMap[p.Configuration.Unique()] = p.Agent
	}
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins:         agentMap,
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}
