package services

import (
	"context"
	"fmt"
	"io"
	"strings"
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

// ServiceCacheKey is the per-SERVICE cache key for an agent connection. Two
// services using the same agent (e.g. two `go-grpc` services) MUST get their own
// agent process: the agent's Runtime holds per-service state (Endpoints,
// GrpcEndpoint, NetworkMappings) in a single struct, so a shared process lets the
// second service's Load overwrite the first's — and the first then resolves the
// second's endpoint ("no network instance for <other>/grpc"). Keying by service,
// not by agent, isolates that state. A service's own Builder+Runtime+Code still
// share ONE process (same key).
func ServiceCacheKey(service *resources.Service) string {
	if service == nil {
		return ""
	}
	if id, err := service.Identity(); err == nil && id != nil {
		return id.Unique()
	}
	if service.Agent != nil {
		return service.Agent.Unique()
	}
	return ""
}

// LoadAgent spawns the agent binary (or reuses a cached connection) and returns a
// ServiceAgent client. `cacheKey` scopes the cached connection — pass
// ServiceCacheKey(service) so each SERVICE gets an isolated agent process; an empty
// key falls back to the agent's unique (for non-service, agent-only operations).
// The underlying connection is cached internally and reused by LoadBuilder/
// LoadRuntime (which must pass the SAME key) on the same process.
func LoadAgent(ctx context.Context, agent *resources.Agent, cacheKey string) (*coreservices.ServiceAgent, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}
	if cacheKey == "" {
		cacheKey = agent.Unique()
	}
	w := wool.Get(ctx).In("services.LoadAgent", wool.Field("agent", agent.Name))
	requestedVersion := agent.Version

	source, err := manager.ResolveLatest(ctx, agent)
	if err != nil {
		return nil, w.Wrap(err)
	}

	// One aggregated INFO line per service. The per-step resolution chatter
	// (FindLocalLatest / ResolveLatest) is TRACE; this is the single line a
	// default run shows for agent resolution.
	annotations := []string{source}
	if requestedVersion == "latest" {
		annotations = append(annotations, "latest")
	} else if requestedVersion != "" && requestedVersion != agent.Version {
		annotations = append(annotations, fmt.Sprintf("requested %s", requestedVersion))
	}
	w.Info(
		fmt.Sprintf("resolved %s → %s (%s)", agent.Name, agent.Version, strings.Join(annotations, ", ")),
		wool.Field("publisher", agent.Publisher),
		wool.Field("agent", agent.Name),
		wool.Field("version", agent.Version),
		wool.Field("source", source),
	)

	conn, err := getOrCreateConn(ctx, cacheKey, agent)
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
func getOrCreateConn(ctx context.Context, cacheKey string, agent *resources.Agent) (*manager.AgentConn, error) {
	connCacheMu.Lock()
	defer connCacheMu.Unlock()
	if conn, ok := connCache[cacheKey]; ok {
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
	connCache[cacheKey] = conn
	return conn, nil
}

// getConn returns the cached connection for a cache key (see ServiceCacheKey).
// Panics if not loaded. Callers MUST pass the SAME key LoadAgent was called with.
func getConn(cacheKey string) *manager.AgentConn {
	connCacheMu.Lock()
	defer connCacheMu.Unlock()
	conn, ok := connCache[cacheKey]
	if !ok {
		panic(fmt.Sprintf("agent connection %q not loaded -- call LoadAgent first", cacheKey))
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
