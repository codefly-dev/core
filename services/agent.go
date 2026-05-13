package services

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/manager"
	coreservices "github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// connCache caches agent connections by unique key. Guarded by
// connCacheMu — the CLI fan-loads agents in parallel via Flow,
// and Go panics on concurrent map writes. Without this lock, two
// concurrent LoadAgent calls for the same agent could even race
// the cache check + insert and double-spawn the process (the
// first connection then leaks, no Close).
var (
	connCacheMu sync.Mutex
	connCache   = make(map[string]*manager.AgentConn)
)

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

	if err := manager.ResolveLatest(ctx, agent); err != nil {
		return nil, w.Wrap(err)
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
//
// Concurrency note: we hold connCacheMu across the cache check, the
// process spawn, AND the cache insert. That serializes concurrent
// LoadAgent calls for DIFFERENT agents — slightly slower than the
// finer-grained "spawn outside lock then re-check" alternative, but
// the CLI's spawn rate is bounded (one per service in a graph), and
// this pattern is panic-free with no double-spawn risk. If contention
// shows up in profiles, switch to a per-key singleflight.
func getOrCreateConn(ctx context.Context, agent *resources.Agent) (*manager.AgentConn, error) {
	connCacheMu.Lock()
	defer connCacheMu.Unlock()
	if conn, ok := connCache[agent.Unique()]; ok {
		return conn, nil
	}
	pr, pw := io.Pipe()
	go agents.GetLogHandler().ForwardLogs(pr)
	// Explicit WithoutSandbox: service agents (Runtime, Builder, Code)
	// run user code, build containers, and otherwise need the host's
	// ambient authority. Per-agent sandbox profiles are the right
	// long-term fix; this opt-out is the audit-visible marker for
	// the gap. See toolbox/launch for the path that DOES sandbox.
	conn, err := manager.Load(ctx, agent,
		manager.WithLogWriter(pw),
		manager.WithoutSandbox())
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	connCache[agent.Unique()] = conn
	return conn, nil
}

// getConn returns the cached connection for an agent. Panics if not loaded.
func getConn(agent *resources.Agent) *manager.AgentConn {
	connCacheMu.Lock()
	defer connCacheMu.Unlock()
	conn, ok := connCache[agent.Unique()]
	if !ok {
		panic(fmt.Sprintf("agent %s not loaded -- call LoadAgent first", agent.Unique()))
	}
	return conn
}

// ClearAgents shuts down all active agent processes gracefully.
//
// The previous version sent SIGTERM and then immediately called
// conn.Close(), which sent SIGKILL — racing past the agent's own
// SIGTERM handler before it could reap its child processes (user
// binaries, Docker containers). conn.Close() is now graceful itself
// (SIGTERM → wait → SIGKILL fallback), so the explicit pre-signal
// here was both redundant and harmful.
//
// Concurrency: snapshot under lock, swap to a fresh map, then Close
// outside the lock. Closing a gRPC conn can take several seconds
// (graceful SIGTERM grace) — holding the lock across it would block
// any concurrent LoadAgent.
func ClearAgents() {
	connCacheMu.Lock()
	old := connCache
	connCache = make(map[string]*manager.AgentConn)
	connCacheMu.Unlock()
	for _, conn := range old {
		conn.Close()
	}
}
