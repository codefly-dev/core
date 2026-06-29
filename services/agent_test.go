package services

import (
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestServiceCacheKey_isolatesServicesSharingOneAgent is a regression test for the
// shared-agent-state bug.
//
// The agent connection cache used to be keyed by agent.Unique(), so two services
// using the SAME agent (e.g. two `go-grpc` services — saas/accounts and
// platform/eventlog) shared ONE agent process. That process holds a single Runtime
// whose per-service state (Endpoints, GrpcEndpoint, NetworkMappings) lives in one
// struct, so the second service's Load OVERWROTE the first's — and the first then
// resolved the second's endpoint at Init, failing with
//   "no network instance for endpoint: platform/eventlog/grpc"
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
