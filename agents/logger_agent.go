package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/hashicorp/go-hclog"
)

// Hijack the Agent Logger for debugging
var toConsole bool

func LogToConsole() {
	toConsole = true
}

type AgentLogger struct {
	source *wool.Identifier
	writer io.Writer
}

type HCLogMessageOut struct {
	Log    *wool.Log        `json:"log"`
	Source *wool.Identifier `json:"identifier"`
}

func (w *AgentLogger) Process(log *wool.Log) {
	if toConsole {
		fmt.Println(log.Message, log.Fields)
		return
	}
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

func NewAgentLogger(agent *resources.Agent) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	source := agent.AsResource().Identifier
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{source: source, writer: writer}
}

func NewAgentServiceLogger(identity *resources.ServiceIdentity) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	source := identity.AsAgentResource().Identifier
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{source: source, writer: writer}
}

func NewServiceLogger(identity *resources.ServiceIdentity) wool.LogProcessor {
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
	})
	source := identity.AsResource().Identifier
	writer := logger.StandardWriter(&hclog.StandardLoggerOptions{})
	return &AgentLogger{source: source, writer: writer}
}

func NewAgentProvider(ctx context.Context, agent *resources.Agent) *wool.Provider {
	res := agent.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentLogger(agent))
	return provider
}

func NewServiceAgentProvider(ctx context.Context, identity *resources.ServiceIdentity) *wool.Provider {
	res := identity.AsAgentResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentServiceLogger(identity))
	return provider
}

func NewServiceProvider(ctx context.Context, identity *resources.ServiceIdentity) *wool.Provider {
	res := identity.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewServiceLogger(identity))
	return provider
}
