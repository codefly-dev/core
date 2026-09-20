package services

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/contract"
	"github.com/codefly-dev/core/agents/manager"
	coreservices "github.com/codefly-dev/core/agents/services"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
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
	connCacheMu    sync.Mutex
	connCache      = make(map[string]*connLoad)
	connLoads      = make(map[string]*connLoad)
	connGeneration uint64
	// connKeyGenerations lets one completed flow evict only the service agents
	// it owned without invalidating unrelated parallel launches.
	connKeyGenerations = make(map[string]uint64)
	managerLoad        = loadCompatibleAgent
)

type connLoad struct {
	done      chan struct{}
	conn      *manager.AgentConn
	err       error
	selection resources.Agent
}

func checkAgentSelection(cacheKey string, admitted, requested resources.Agent) error {
	if admitted != requested {
		return fmt.Errorf("agent selection changed for %q from %s to %s; clear this service's agent before loading its replacement", cacheKey, admitted.Unique(), requested.Unique())
	}
	return nil
}

func loadCompatibleAgent(ctx context.Context, agent *resources.Agent, options ...manager.LoadOption) (*manager.AgentConn, error) {
	conn, err := manager.Load(ctx, agent, options...)
	if err != nil {
		return nil, err
	}
	info, err := agentv0.NewAgentClient(conn.GRPCConn()).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err == nil {
		err = contract.Check(info.GetContract())
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("admit agent %s: %w", agent, err)
	}
	return conn, nil
}

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

	selection := *agent
	conn, err := getOrCreateConn(ctx, cacheKey, &selection)
	if err != nil {
		return nil, w.Wrap(err)
	}

	sa := coreservices.NewServiceAgentClient(conn.GRPCConn())
	sa.Agent = &selection
	sa.ProcessInfo = conn.ProcessInfo()

	return sa, nil
}

// getOrCreateConn returns a cached connection or spawns a new agent process.
// It wires agent stderr through ForwardLogs so structured logs reach the
// CLI display pipeline.
//
// Concurrent requests for one cache key share an in-flight launch; different
// keys spawn independently. The generation check prevents a launch completing
// after ClearAgents from repopulating the freshly-cleared cache.
func getOrCreateConn(ctx context.Context, cacheKey string, agent *resources.Agent) (*manager.AgentConn, error) {
	connCacheMu.Lock()
	if load, ok := connCache[cacheKey]; ok {
		connCacheMu.Unlock()
		if err := checkAgentSelection(cacheKey, load.selection, *agent); err != nil {
			return nil, err
		}
		if err := load.conn.CheckArtifact(ctx, agent); err != nil {
			return nil, err
		}
		return load.conn, nil
	}
	if load, ok := connLoads[cacheKey]; ok {
		connCacheMu.Unlock()
		if err := checkAgentSelection(cacheKey, load.selection, *agent); err != nil {
			return nil, err
		}
		select {
		case <-load.done:
			if load.err == nil {
				if err := load.conn.CheckArtifact(ctx, agent); err != nil {
					return nil, err
				}
			}
			return load.conn, load.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	load := &connLoad{done: make(chan struct{}), selection: *agent}
	generation := connGeneration
	keyGeneration := connKeyGenerations[cacheKey]
	connLoads[cacheKey] = load
	connCacheMu.Unlock()

	pr, pw := io.Pipe()
	go agents.GetLogHandler().ForwardLogs(pr)
	// Explicit WithoutSandbox: service agents (Runtime, Builder, Code)
	// run user code, build containers, and otherwise need the host's
	// ambient authority. Per-agent sandbox profiles are the right
	// long-term fix; this opt-out is the audit-visible marker for
	// the gap. See toolbox/launch for the path that DOES sandbox.
	conn, err := managerLoad(ctx, agent,
		manager.WithLogWriter(pw),
		manager.WithoutSandbox(),
		// Service lifecycle agents are orchestration plumbing: they compile and
		// run the user's own code but do not act as a SaaS end-user principal.
		// Keep that authority choice explicit so manager admission fails closed
		// everywhere a caller forgets to decide.
		manager.WithoutPrincipal())
	if err != nil {
		_ = pw.Close()
	}

	connCacheMu.Lock()
	if current, ok := connLoads[cacheKey]; ok && current == load {
		delete(connLoads, cacheKey)
	}
	if err == nil && (generation != connGeneration || keyGeneration != connKeyGenerations[cacheKey]) {
		err = fmt.Errorf("agent connection cache was cleared while %q was loading", cacheKey)
	}
	if err == nil {
		connCache[cacheKey] = load
	}
	load.conn, load.err = conn, err
	if err != nil {
		load.conn = nil
	}
	close(load.done)
	connCacheMu.Unlock()

	if err != nil && conn != nil {
		conn.Close()
	}
	return load.conn, load.err
}

// getConn returns the cached connection for a cache key (see ServiceCacheKey).
// Callers MUST pass the SAME key LoadAgent was called with.
func getConn(ctx context.Context, cacheKey string, agent *resources.Agent) (*manager.AgentConn, error) {
	connCacheMu.Lock()
	load, ok := connCache[cacheKey]
	connCacheMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("agent connection %q not loaded; call LoadAgent first", cacheKey)
	}
	if err := checkAgentSelection(cacheKey, load.selection, *agent); err != nil {
		return nil, err
	}
	if err := load.conn.CheckArtifact(ctx, agent); err != nil {
		return nil, err
	}
	return load.conn, nil
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
// The instances cache (service.go) MUST be reset alongside connCache:
// both are keyed by identity.Unique(), and a cached *Instance holds an
// Agent/Info built from a connection this function closes. Leaving it
// behind means a clear-then-reload in the same process returns that
// stale Instance, whose subsequent LoadBuilder/LoadRuntime panics in
// getConn (connCache now empty) or fails RPCs on the closed conn.
//
// Concurrency: snapshot under lock, swap to a fresh map, then Close
// outside the lock. Closing a gRPC conn can take several seconds
// (graceful SIGTERM grace) — holding the lock across it would block
// any concurrent LoadAgent.
func ClearAgents() {
	connCacheMu.Lock()
	old := connCache
	connCache = make(map[string]*connLoad)
	connLoads = make(map[string]*connLoad)
	connKeyGenerations = make(map[string]uint64)
	connGeneration++
	connCacheMu.Unlock()

	instancesMu.Lock()
	instances = map[string]*Instance{}
	instancesMu.Unlock()

	for _, load := range old {
		if load != nil {
			load.conn.Close()
		}
	}
}

// ClearAgent evicts one service-scoped agent connection and its cached
// Instance. It is safe to call while unrelated service agents are loading or
// running. A same-key launch already in flight is invalidated and cannot
// repopulate the cache after this call.
func ClearAgent(cacheKey string) {
	if cacheKey == "" {
		return
	}
	connCacheMu.Lock()
	load := connCache[cacheKey]
	delete(connCache, cacheKey)
	connKeyGenerations[cacheKey]++
	connCacheMu.Unlock()

	instancesMu.Lock()
	delete(instances, cacheKey)
	instancesMu.Unlock()

	if load != nil {
		load.conn.Close()
	}
}
