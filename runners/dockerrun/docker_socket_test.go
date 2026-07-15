package dockerrun

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/mount"
)

func TestDockerSocketMountIsOptIn(t *testing.T) {
	env := &DockerEnvironment{}
	env.WithMount("/host/workspace", "/workspace")

	hostConfig := env.createHostConfig(context.Background())
	if hasDockerSocketMount(hostConfig.Mounts) {
		t.Fatal("ordinary container received the host Docker socket")
	}
	if len(hostConfig.Mounts) != 1 || hostConfig.Mounts[0].Target != "/workspace" {
		t.Fatalf("user mounts were not preserved: %+v", hostConfig.Mounts)
	}

	env.WithDockerSocket()
	hostConfig = env.createHostConfig(context.Background())
	if !hasDockerSocketMount(hostConfig.Mounts) {
		t.Fatal("WithDockerSocket did not add the requested socket mount")
	}
}

func hasDockerSocketMount(mounts []mount.Mount) bool {
	for _, candidate := range mounts {
		if candidate.Type == mount.TypeBind && candidate.Source == "/var/run/docker.sock" && candidate.Target == "/var/run/docker.sock" {
			return true
		}
	}
	return false
}
