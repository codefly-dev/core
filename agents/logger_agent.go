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

// nonBlocking wraps a log processor so writing a log NEVER blocks the agent on a
// slow/stalled parent consumer. The synchronous stderr write in AgentLogger.Process
// otherwise backpressures the whole agent (and can wedge shutdown) when the CLI is
// slow to drain — an agent must never block on logging, the same discipline as
// "never panic". Bounded queue, drop-on-full: a dropped log line is strictly better
// than a wedged agent.
func nonBlocking(inner wool.LogProcessor) wool.LogProcessor {
	return wool.NewBufferedProcessor(inner, 0) // 0 → default capacity (256)
}

// NewAgentProvider creates a wool provider for an agent.
func NewAgentProvider(ctx context.Context, agent *resources.Agent) *wool.Provider {
	res := agent.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(nonBlocking(NewAgentLogger(agent)))
	return provider
}

// NewServiceAgentProvider creates a wool provider for a service agent.
func NewServiceAgentProvider(ctx context.Context, identity *resources.ServiceIdentity) *wool.Provider {
	res := identity.AsAgentResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(nonBlocking(NewAgentServiceLogger(identity)))
	return provider
}

// NewServiceProvider creates a wool provider for a service.
func NewServiceProvider(ctx context.Context, identity *resources.ServiceIdentity) *wool.Provider {
	res := identity.AsResource()
	provider := wool.New(ctx, res)
	provider.WithLogger(nonBlocking(NewServiceLogger(identity)))
	return provider
}
