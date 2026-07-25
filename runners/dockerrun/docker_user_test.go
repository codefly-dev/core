package dockerrun

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestCreateContainerConfig_User verifies WithUser is threaded into the
// container config so the container (and its execs) run as the given user.
// This is what lets the proto companion write bind-mounted output owned by a
// non-root host user rather than root. No docker daemon needed —
// createContainerConfig just builds the container.Config struct.
func TestCreateContainerConfig_User(t *testing.T) {
	env := &DockerEnvironment{
		name:  "proto",
		image: &resources.DockerImage{Name: "codeflydev/proto", Tag: "0.0.11"},
	}
	ctx := context.Background()

	if cfg := env.createContainerConfig(ctx); cfg.User != "" {
		t.Fatalf("expected default (empty) user, got %q", cfg.User)
	}

	env.WithUser("1000:1000")
	if cfg := env.createContainerConfig(ctx); cfg.User != "1000:1000" {
		t.Fatalf("expected user 1000:1000, got %q", cfg.User)
	}
}
