package base_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/runners/sandbox"
)

// TestDockerEnvironment_WithSandbox_API_Parity verifies the chainable
// WithSandbox setter exists and returns the same env. Docker treats
// the sandbox as a no-op (the container IS the boundary); this test
// pins the API surface so workspace orchestrators can pass the same
// sandbox to every environment without type-switching on the
// implementation. If we ever want Docker to USE the sandbox (we
// won't — a container already is one), this is where to start.
func TestDockerEnvironment_WithSandbox_API_Parity(t *testing.T) {
	// Build an env with a nil image; we don't pull or run anything,
	// we just exercise the setter. We can't call NewDockerEnvironment
	// (that does daemon work and image pull); instead, build the zero
	// value directly and confirm WithSandbox is chainable. This is
	// strictly an API-shape test.
	var env base.DockerEnvironment
	got := env.WithSandbox(sandbox.NewNative())
	require.Same(t, &env, got, "WithSandbox must return the same env (chainable, like Native/Nix versions)")
}
