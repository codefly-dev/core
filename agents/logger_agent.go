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
	source *wool.Identifier
	writer io.Writer
}

type HCLogMessageOut struct {
	Log    *wool.Log        `json:"log"`
	Source *wool.Identifier `json:"identifier"`
}

func (w *AgentLogger) Process(log *wool.Log) {
	msg := &HCLogMessageOut{Log: log}
	msg.Source = w.source
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
	source := agent.AsResource().Identifier
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{source: source, writer: writer}
}

func NewAgentServiceLogger(identity *configurations.ServiceIdentity) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	source := identity.AsAgentResource().Identifier
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{source: source, writer: writer}
}

func NewServiceLogger(identity *configurations.ServiceIdentity) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	source := identity.AsResource().Identifier
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{source: source, writer: writer}
}

func NewAgentProvider(ctx context.Context, agent *configurations.Agent) *wool.Provider {
	res := agent.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentLogger(agent))
	return provider
}

func NewServiceAgentProvider(ctx context.Context, identity *configurations.ServiceIdentity) *wool.Provider {
	res := identity.AsAgentResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentServiceLogger(identity))
	return provider
}

func NewServiceProvider(ctx context.Context, identity *configurations.ServiceIdentity) *wool.Provider {
	res := identity.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewServiceLogger(identity))
	return provider
}
