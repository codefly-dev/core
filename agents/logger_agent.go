package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// AgentLogger writes structured wool logs to stderr as JSON.
// The CLI reads these from the spawned process's stderr.
type AgentLogger struct {
	source *wool.Identifier
}

// LogMessage is the JSON envelope written to stderr.
type LogMessage struct {
	Log    *wool.Log        `json:"log"`
	Source *wool.Identifier `json:"identifier"`
}

func (w *AgentLogger) Process(log *wool.Log) {
	msg := &LogMessage{Log: log, Source: w.source}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	// Write as a single line to stderr
	fmt.Fprintf(os.Stderr, "%s\n", data)
}

// NewAgentLogger creates a log processor for an agent that writes to stderr.
func NewAgentLogger(agent *resources.Agent) wool.LogProcessor {
	r := agent.AsResource()
	source := &wool.Identifier{Kind: r.Kind, Unique: r.Unique}
	return &AgentLogger{source: source}
}

// NewAgentServiceLogger creates a log processor for a service agent.
func NewAgentServiceLogger(identity *resources.ServiceIdentity) wool.LogProcessor {
	r := identity.AsAgentResource()
	source := &wool.Identifier{Kind: r.Kind, Unique: r.Unique}
	return &AgentLogger{source: source}
}

// NewServiceLogger creates a log processor for a service.
func NewServiceLogger(identity *resources.ServiceIdentity) wool.LogProcessor {
	r := identity.AsResource()
	source := &wool.Identifier{Kind: r.Kind, Unique: r.Unique}
	return &AgentLogger{source: source}
}

// NewAgentProvider creates a wool provider for an agent.
func NewAgentProvider(ctx context.Context, agent *resources.Agent) *wool.Provider {
	res := agent.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentLogger(agent))
	return provider
}

// NewServiceAgentProvider creates a wool provider for a service agent.
func NewServiceAgentProvider(ctx context.Context, identity *resources.ServiceIdentity) *wool.Provider {
	res := identity.AsAgentResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewAgentServiceLogger(identity))
	return provider
}

// NewServiceProvider creates a wool provider for a service.
func NewServiceProvider(ctx context.Context, identity *resources.ServiceIdentity) *wool.Provider {
	res := identity.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(NewServiceLogger(identity))
	return provider
}
