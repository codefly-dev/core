package services

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/manager"
	coreservices "github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// connCache caches agent connections by unique key.
var connCache map[string]*manager.AgentConn

func init() {
	connCache = make(map[string]*manager.AgentConn)
}

// LoadAgent spawns the agent binary (or reuses a cached connection) and
// returns a ServiceAgent client. The underlying connection is cached
// internally and used by LoadBuilder/LoadRuntime to create additional
// gRPC clients on the same process.
func LoadAgent(ctx context.Context, agent *resources.Agent) (*coreservices.ServiceAgent, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}
	w := wool.Get(ctx).In("services.LoadAgent", wool.Field("agent", agent.Name))
	w.Debug("loading service agent")

	if agent.Version == "latest" {
		err := manager.PinToLatestRelease(ctx, agent)
		if err != nil {
			return nil, w.Wrap(err)
		}
	}

	conn, err := getOrCreateConn(ctx, agent)
	if err != nil {
		return nil, w.Wrap(err)
	}

	sa := coreservices.NewServiceAgentClient(conn.GRPCConn())
	sa.Agent = agent
	sa.ProcessInfo = conn.ProcessInfo()

	return sa, nil
}

// getOrCreateConn returns a cached connection or spawns a new agent process.
// It wires agent stderr through ForwardLogs so structured logs reach the
// CLI display pipeline.
func getOrCreateConn(ctx context.Context, agent *resources.Agent) (*manager.AgentConn, error) {
	if conn, ok := connCache[agent.Unique()]; ok {
		return conn, nil
	}
	pr, pw := io.Pipe()
	go agents.GetLogHandler().ForwardLogs(pr)
	conn, err := manager.Load(ctx, agent, manager.WithLogWriter(pw))
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	connCache[agent.Unique()] = conn
	return conn, nil
}

// getConn returns the cached connection for an agent. Panics if not loaded.
func getConn(agent *resources.Agent) *manager.AgentConn {
	conn, ok := connCache[agent.Unique()]
	if !ok {
		panic(fmt.Sprintf("agent %s not loaded -- call LoadAgent first", agent.Unique()))
	}
	return conn
}

// ClearAgents kills all active agent processes.
func ClearAgents() {
	for key, conn := range connCache {
		if conn.ProcessInfo() != nil {
			process, err := os.FindProcess(conn.ProcessInfo().PID)
			if err == nil {
				_ = process.Signal(syscall.SIGTERM)
			}
		}
		conn.Close()
		delete(connCache, key)
	}
}
