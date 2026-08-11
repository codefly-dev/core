package dockerrun

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
)

func TestCreateContainerConfigEntrypointOverride(t *testing.T) {
	env := &DockerEnvironment{image: &resources.DockerImage{Name: "toolchain", Tag: "1.0"}}
	entrypoint := []string{"/bin/sh", "-c"}
	env.WithEntrypoint(entrypoint...)
	entrypoint[0] = "/mutated"

	config := env.createContainerConfig(context.Background())
	if len(config.Entrypoint) != 2 || config.Entrypoint[0] != "/bin/sh" || config.Entrypoint[1] != "-c" {
		t.Fatalf("entrypoint = %v, want [/bin/sh -c]", config.Entrypoint)
	}

	env.WithEntrypoint()
	config = env.createContainerConfig(context.Background())
	if config.Entrypoint == nil || len(config.Entrypoint) != 0 {
		t.Fatalf("cleared entrypoint = %#v, want explicit empty slice", config.Entrypoint)
	}
}
