//go:build !skip_infra

package dockerrun

import (
	"context"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
)

func TestPinImageBindsWitnessCreationAndReuseToSameBytes(t *testing.T) {
	ctx := context.Background()
	name := uniqueName(t)
	env, err := NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), name)
	require.NoError(t, err)
	defer env.client.Close()
	id, err := env.PinImage(ctx)
	require.NoError(t, err)
	again, err := env.PinImage(ctx)
	require.NoError(t, err)
	require.Equal(t, id, again)
	env.WithCommand("sleep", "60")
	config, host, err := env.desiredContainerConfigs(ctx)
	require.NoError(t, err)
	require.Equal(t, id, config.Image)
	created, err := env.client.ContainerCreate(ctx, config, host, nil, nil, name)
	require.NoError(t, err)
	defer env.client.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
	actual, err := env.client.ContainerInspect(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, id, actual.Image)

	// A changed executable configuration under one tag must change both the
	// recorded identity and the reuse fingerprint. No registry is required.
	tag := strings.ToLower(name) + ":test"
	require.NoError(t, env.client.ImageTag(ctx, id, tag))
	defer env.client.ImageRemove(ctx, tag, image.RemoveOptions{Force: true})
	changed, err := env.client.ContainerCommit(ctx, created.ID, container.CommitOptions{Changes: []string{"ENTRYPOINT [\"/bin/echo\", \"changed\"]"}})
	require.NoError(t, err)
	defer env.client.ImageRemove(ctx, changed.ID, image.RemoveOptions{Force: true})
	require.NoError(t, env.client.ImageTag(ctx, changed.ID, tag))
	fresh, err := NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage(tag), name)
	require.NoError(t, err)
	defer fresh.client.Close()
	next, err := fresh.PinImage(ctx)
	require.NoError(t, err)
	require.Equal(t, changed.ID, next)
	require.NotEqual(t, id, next)
	fresh.WithCommand("sleep", "60")
	newConfig, _, err := fresh.desiredContainerConfigs(ctx)
	require.NoError(t, err)
	require.NotEqual(t, config.Labels[LabelCodeflyConfig], newConfig.Labels[LabelCodeflyConfig])
	again, err = env.PinImage(ctx)
	require.NoError(t, err)
	require.Equal(t, id, again, "an already pinned environment must not follow the moved tag")
	env.instance = &DockerContainerInstance{ID: created.ID}
	_, err = env.PinImage(ctx)
	require.ErrorContains(t, err, "after container initialization")
}
