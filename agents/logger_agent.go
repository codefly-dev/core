package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// AgentLogger writes structured wool logs to stderr as JSON.
// The CLI reads these from the spawned process's stderr.
type AgentLogger struct {
	source *wool.Identifier
	writer io.Writer
	lines  chan []byte
	done   chan struct{}
	once   sync.Once
	mu     sync.RWMutex
	closed bool
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

	// Marshal before returning so the queued line owns an immutable snapshot of
	// every field. Log fields commonly point at request/configuration objects that
	// callers are free to mutate as soon as Process returns; queueing the *Log
	// itself would race that mutation in the background writer.
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return
	}
	select {
	case w.lines <- data:
	default:
		// Logging must never backpressure an agent. A dropped diagnostic is less
		// harmful than wedging the service when the parent stops reading stderr.
	}
}

func (w *AgentLogger) run() {
	for line := range w.lines {
		_, _ = fmt.Fprintf(w.writer, "%s\n", line)
	}
	close(w.done)
}

// Flush writes every queued line and permanently closes the logger. It is safe
// to call concurrently with Process and more than once.
func (w *AgentLogger) Flush() {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.lines)
		w.mu.Unlock()
	})
	<-w.done
}

func newAgentLogger(source *wool.Identifier, writer io.Writer, bufferSize int) *AgentLogger {
	if bufferSize <= 0 {
		bufferSize = 256
	}
	logger := &AgentLogger{
		source: source,
		writer: writer,
		lines:  make(chan []byte, bufferSize),
		done:   make(chan struct{}),
	}
	go logger.run()
	return logger
}

// NewAgentLogger creates a log processor for an agent that writes to stderr.
func NewAgentLogger(agent *resources.Agent) *AgentLogger {
	r := agent.AsResource()
	source := &wool.Identifier{Kind: r.Kind, Unique: r.Unique}
	return newAgentLogger(source, os.Stderr, 0)
}

// NewAgentServiceLogger creates a log processor for a service agent.
func NewAgentServiceLogger(identity *resources.ServiceIdentity) *AgentLogger {
	r := identity.AsAgentResource()
	source := &wool.Identifier{Kind: r.Kind, Unique: r.Unique}
	return newAgentLogger(source, os.Stderr, 0)
}

// NewServiceLogger creates a log processor for a service.
func NewServiceLogger(identity *resources.ServiceIdentity) *AgentLogger {
	r := identity.AsResource()
	source := &wool.Identifier{Kind: r.Kind, Unique: r.Unique}
	return newAgentLogger(source, os.Stderr, 0)
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
