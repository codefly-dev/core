package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"
)

func resetConnectionCacheForTest() {
	connCacheMu.Lock()
	connCache = make(map[string]*manager.AgentConn)
	connLoads = make(map[string]*connLoad)
	connGeneration = 0
	connKeyGenerations = make(map[string]uint64)
	connCacheMu.Unlock()
}

func TestClearAgentInvalidatesOnlyOneServiceCache(t *testing.T) {
	resetConnectionCacheForTest()
	defer resetConnectionCacheForTest()

	connCacheMu.Lock()
	connCache["module/one"] = nil
	connCache["module/two"] = nil
	connCacheMu.Unlock()
	instancesMu.Lock()
	instances["module/one"] = &Instance{}
	instances["module/two"] = &Instance{}
	instancesMu.Unlock()

	ClearAgent("module/one")

	connCacheMu.Lock()
	_, oneConn := connCache["module/one"]
	_, twoConn := connCache["module/two"]
	connCacheMu.Unlock()
	instancesMu.Lock()
	_, oneInstance := instances["module/one"]
	_, twoInstance := instances["module/two"]
	instancesMu.Unlock()
	if oneConn || oneInstance || !twoConn || !twoInstance {
		t.Fatalf("scoped clear state: one=(conn:%v instance:%v) two=(conn:%v instance:%v)", oneConn, oneInstance, twoConn, twoInstance)
	}
}

func TestClearAgentInvalidatesSameKeyLaunchInFlight(t *testing.T) {
	resetConnectionCacheForTest()
	originalLoad := managerLoad
	defer func() {
		managerLoad = originalLoad
		resetConnectionCacheForTest()
	}()

	entered := make(chan struct{})
	release := make(chan struct{})
	managerLoad = func(context.Context, *resources.Agent, ...manager.LoadOption) (*manager.AgentConn, error) {
		close(entered)
		<-release
		return nil, nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := getOrCreateConn(context.Background(), "module/service", &resources.Agent{Publisher: "codefly.dev", Name: "test", Version: "1"})
		done <- err
	}()
	<-entered
	ClearAgent("module/service")
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "was cleared while") {
		t.Fatalf("in-flight launch error = %v, want scoped invalidation", err)
	}
}

func TestGetOrCreateConnLaunchesDifferentKeysConcurrently(t *testing.T) {
	resetConnectionCacheForTest()
	originalLoad := managerLoad
	defer func() {
		managerLoad = originalLoad
		resetConnectionCacheForTest()
	}()

	var calls atomic.Int32
	var serialized atomic.Bool
	bothEntered := make(chan struct{})
	managerLoad = func(context.Context, *resources.Agent, ...manager.LoadOption) (*manager.AgentConn, error) {
		if calls.Add(1) == 2 {
			close(bothEntered)
		}
		select {
		case <-bothEntered:
		case <-time.After(500 * time.Millisecond):
			serialized.Store(true)
		}
		return nil, errors.New("test launch failure")
	}

	agent := &resources.Agent{Publisher: "codefly.dev", Name: "test", Version: "1"}
	var wg sync.WaitGroup
	for _, key := range []string{"module/one", "module/two"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = getOrCreateConn(context.Background(), key, agent)
		}()
	}
	wg.Wait()
	if serialized.Load() || calls.Load() != 2 {
		t.Fatalf("different keys were serialized: calls=%d serialized=%v", calls.Load(), serialized.Load())
	}
}

func TestGetOrCreateConnCoalescesSameKey(t *testing.T) {
	resetConnectionCacheForTest()
	originalLoad := managerLoad
	defer func() {
		managerLoad = originalLoad
		resetConnectionCacheForTest()
	}()

	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	managerLoad = func(context.Context, *resources.Agent, ...manager.LoadOption) (*manager.AgentConn, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil, errors.New("test launch failure")
	}

	agent := &resources.Agent{Publisher: "codefly.dev", Name: "test", Version: "1"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = getOrCreateConn(context.Background(), "module/service", agent)
		}()
	}
	<-entered
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("same key launched %d agents, want 1", calls.Load())
	}
	close(release)
	wg.Wait()
}

// TestServiceCacheKey_isolatesServicesSharingOneAgent is a regression test for the
// shared-agent-state bug.
//
// The agent connection cache used to be keyed by agent.Unique(), so two services
// using the SAME agent (e.g. two `go-grpc` services — saas/accounts and
// platform/eventlog) shared ONE agent process. That process holds a single Runtime
// whose per-service state (Endpoints, GrpcEndpoint, NetworkMappings) lives in one
// struct, so the second service's Load OVERWROTE the first's — and the first then
// resolved the second's endpoint at Init, failing with
//
//	"no network instance for endpoint: platform/eventlog/grpc"
//
// while starting saas/accounts. Deterministic, and only when two services share an
// agent (which is why a single go-grpc service per workspace never hit it).
//
// The fix keys the cache per SERVICE (ServiceCacheKey) so each gets its own process.
// This test pins that: services sharing an agent must get DISTINCT keys.
func TestServiceCacheKey_isolatesServicesSharingOneAgent(t *testing.T) {
	agent := &resources.Agent{Publisher: "codefly.dev", Name: "go-grpc", Version: "0.1.4"}

	accounts := &resources.Service{Name: "accounts", Agent: agent}
	accounts.WithModule("saas")
	eventlog := &resources.Service{Name: "eventlog", Agent: agent}
	eventlog.WithModule("platform")

	ka := ServiceCacheKey(accounts)
	kb := ServiceCacheKey(eventlog)

	if ka == kb {
		t.Fatalf("two services sharing agent %q got the SAME cache key %q: they would share one "+
			"agent process and corrupt each other's per-service Runtime state", agent.Unique(), ka)
	}
	if ka == agent.Unique() || kb == agent.Unique() {
		t.Fatalf("cache key must be per-SERVICE, not the agent unique %q (got %q and %q)", agent.Unique(), ka, kb)
	}

	// A service's OWN Builder/Runtime/Code must still share one process, so the key
	// must be stable for the same service.
	if again := ServiceCacheKey(accounts); again != ka {
		t.Fatalf("ServiceCacheKey is not stable for the same service: %q then %q", ka, again)
	}
}

// TestClearAgents_invalidatesInstancesCache is a regression test for the
// stale-instance bug: ClearAgents reset connCache but left the instances cache
// (keyed identically by identity.Unique()) populated. A clear-then-reload in the
// same process then returned a cached *Instance whose Agent/Info were built from
// the now-closed connection, and the next LoadBuilder/LoadRuntime panicked in
// getConn (connCache empty) or failed RPCs on the dead conn. The two caches must
// be cleared together.
func TestClearAgents_invalidatesInstancesCache(t *testing.T) {
	svc := &resources.Service{Name: "accounts", Agent: &resources.Agent{Publisher: "codefly.dev", Name: "go-grpc", Version: "0.1.4"}}
	svc.WithModule("saas")
	id, err := svc.Identity()
	if err != nil {
		t.Fatalf("cannot get identity: %v", err)
	}

	instancesMu.Lock()
	instances[id.Unique()] = &Instance{Service: svc, Identity: id}
	instancesMu.Unlock()

	ClearAgents()

	instancesMu.Lock()
	_, stillCached := instances[id.Unique()]
	instancesMu.Unlock()
	if stillCached {
		t.Fatalf("ClearAgents left a stale *Instance for %q in the cache: a reload would return an "+
			"Instance bound to the closed connection", id.Unique())
	}
}
