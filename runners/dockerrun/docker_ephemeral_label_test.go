package dockerrun

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestCreateContainerConfig_EphemeralLabel verifies the labeling half of the
// OrbStack-leak fix: when this process is in ephemeral (SDK / --cli-server)
// mode, every container it creates carries codefly.ephemeral=true so the sweep
// can reap running orphans; otherwise the label is absent and the container is
// treated as a reusable stateful service. No docker daemon needed —
// createContainerConfig just builds the container.Config struct.
func TestCreateContainerConfig_EphemeralLabel(t *testing.T) {
	env := &DockerEnvironment{
		name:  "infra/neo4j",
		image: &resources.DockerImage{Name: "neo4j", Tag: "5"},
	}
	ctx := context.Background()

	SetEphemeralContainers(false)
	cfg := env.createContainerConfig(ctx)
	if cfg.Labels[LabelCodeflyOwner] != "true" {
		t.Fatalf("owner label missing: %v", cfg.Labels)
	}
	if _, ok := cfg.Labels[LabelCodeflyEphemeral]; ok {
		t.Fatalf("non-ephemeral run set the ephemeral label: %v", cfg.Labels)
	}

	SetEphemeralContainers(true)
	cfg = env.createContainerConfig(ctx)
	if cfg.Labels[LabelCodeflyEphemeral] != "true" {
		t.Fatalf("ephemeral run did not set %s=true: %v", LabelCodeflyEphemeral, cfg.Labels)
	}

	SetEphemeralContainers(false) // reset for other tests
}
