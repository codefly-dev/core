package dockerrun

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/errdefs"
)

// ShutdownNamedContainer explicitly acquires the current generation of a named
// Codefly container for destruction. Unlike environment Shutdown, this is for
// callers that no longer hold the environment that started the container.
// Ownership and identity come from one inspection; subsequent teardown never
// resolves the name again, so a replacement after acquisition is left alone.
func ShutdownNamedContainer(ctx context.Context, name string) error {
	cli, _, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("cannot create teardown client: %w", err)
	}
	defer func() { _ = cli.Close() }()

	containerName := ContainerName(name)
	inspectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inspect, err := cli.ContainerInspect(inspectCtx, containerName)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot acquire container %s for teardown: %w", containerName, err)
	}
	const ownerLabelValue = "true"
	if inspect.ContainerJSONBase == nil || inspect.ID == "" ||
		labelOf(inspect.Config, LabelCodeflyOwner) != ownerLabelValue ||
		labelOf(inspect.Config, LabelCodeflyName) != containerName {
		return fmt.Errorf("cannot prove identity and Codefly ownership of container %s", containerName)
	}
	env := &DockerEnvironment{client: cli, name: containerName, instance: &DockerContainerInstance{ID: inspect.ID}}
	return env.Shutdown(ctx)
}
