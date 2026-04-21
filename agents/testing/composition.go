// Package testing provides helpers for agent composition + lifecycle
// tests. The goal is to make adding tests to a new agent cheap — one
// import and a couple of one-line calls, matching the pattern the 13
// service agents use (Service with services.Base, Settings with YAML,
// PluginRegistration shape).
//
// This package is test-only in practice: callers import it from their
// agent's `composition_test.go`. It deliberately avoids spinning up
// actual subprocesses, network, or Docker — those belong in each
// agent's own integration tests.
package testing

import (
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/agents/services"
)

// BaseHolder is the minimal shape every agent's Service satisfies: it
// embeds or holds a pointer to a services.Base. Composition tests only
// need to reach into Base to verify wiring.
//
// Agents can satisfy this trivially — their Service already embeds
// *services.Base, so the interface is auto-implemented by promotion.
type BaseHolder interface {
	// GetBase returns the underlying services.Base. Most agents embed
	// *Base anonymously, in which case they need a one-line shim:
	//
	//	func (s *Service) GetBase() *services.Base { return s.Base }
	//
	// Zero cost at runtime; provides a stable test-only access point.
	GetBase() *services.Base
}

// AssertBaseWired is the canonical composition sanity check. Every
// service agent should call this in its composition_test.go.
//
//	func TestNewService_EmbedsBase(t *testing.T) {
//	    testing.AssertBaseWired(t, NewService())
//	}
//
// On failure the test exits immediately with a diagnostic pointing at
// the field that's nil — common causes are (a) someone forgot to call
// services.NewServiceBase, (b) the agent type stopped embedding *Base.
func AssertBaseWired(t *testing.T, holder BaseHolder) {
	t.Helper()
	if holder == nil {
		t.Fatal("service holder is nil")
	}
	base := holder.GetBase()
	if base == nil {
		t.Fatal("services.Base is nil — did NewServiceBase get called?")
	}
}

// AssertYAMLRoundTrip unmarshals a YAML string into a fresh zero-valued
// settings struct and calls check(settings) for field-level assertions.
// The check closure gets to run arbitrary expectations without the
// caller needing to redeclare the Settings type by hand.
//
//	testing.AssertYAMLRoundTrip(t, `hot-reload: true`, func(t *testing.T, s *Settings) {
//	    if !s.HotReload { t.Error("HotReload not populated") }
//	})
//
// The function is generic so each caller types its own Settings — this
// avoids leaking agent-specific symbols into this shared package.
func AssertYAMLRoundTrip[S any](t *testing.T, yamlDoc string, check func(t *testing.T, s *S)) {
	t.Helper()
	var out S
	if err := yaml.Unmarshal([]byte(yamlDoc), &out); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	check(t, &out)
}

// MissingField builds a Skip message for cases where an agent has
// intentionally no Settings (e.g. the generic agent). Prefer explicit
// nil-tolerance over skipping; this is provided only for corner cases
// where a shared helper doesn't apply.
func MissingField(field string) string {
	return fmt.Sprintf("%s not populated (intentional for agents without this setting)", field)
}
