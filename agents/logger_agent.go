package agents

import (
	"context"
	"encoding/json"
	"io"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/wool"
	"github.com/hashicorp/go-hclog"
)

type AgentLogger struct {
	agent    *configurations.Agent
	identity *configurations.ServiceIdentity
	writer   io.Writer
}

type HCLogMessageOut struct {
	Log    *wool.Log        `json:"log"`
	Source *wool.Identifier `json:"identifier"`
}

func (w *AgentLogger) Process(log *wool.Log) {
	msg := &HCLogMessageOut{Log: log}
	if w.agent != nil {
		msg.Source = &wool.Identifier{
			Kind:   "agent",
			Unique: w.agent.Unique(),
		}
	}
	if w.identity != nil {
		msg.Source = &wool.Identifier{
			Kind:   "service",
			Unique: w.identity.Unique(),
		}

	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_, err = w.writer.Write(data)
	if err != nil {
		return
	}
}

func NewAgentLogger(agent *configurations.Agent) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{agent: agent, writer: writer}
}

func NewServiceLogger(identity *configurations.ServiceIdentity) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{identity: identity, writer: writer}
}

func NewAgentProvider(ctx context.Context, agent *configurations.Agent) *wool.Provider {
	res := agent.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentLogger(agent))
	return provider
}

func NewServiceProvider(ctx context.Context, identity *configurations.ServiceIdentity) *wool.Provider {
	res := identity.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewServiceLogger(identity))
	return provider
}
