//go:build !skip_infra

package dockerrun

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

func TestImageLookupUsesDaemonIdentityRatherThanDisplayTags(t *testing.T) {
	ctx := context.Background()
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer c.Close()
	stream, err := c.ImagePull(ctx, "alpine:3.19.1", image.PullOptions{})
	require.NoError(t, err, "Docker is required")
	_, err = io.Copy(io.Discard, stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	inspected, err := c.ImageInspect(ctx, "alpine:3.19.1")
	require.NoError(t, err)
	require.NotEmpty(t, inspected.RepoDigests)
	for _, reference := range []string{"alpine:3.19.1", "docker.io/library/alpine:3.19.1", inspected.RepoDigests[0]} {
		t.Run(reference, func(t *testing.T) {
			found, err := ImageExists(ctx, c, resources.NewDockerImage(reference))
			require.NoError(t, err)
			require.True(t, found)
		})
	}
	found, err := ImageExists(ctx, c, resources.NewDockerImage("codefly-test/missing@sha256:"+strings.Repeat("0", 64)))
	require.NoError(t, err)
	require.False(t, found)
	// An unreachable daemon is an operational error, never an absent image.
	directory, err := os.MkdirTemp("/tmp", "image-lookup-")
	require.NoError(t, err)
	defer os.RemoveAll(directory)
	absent, err := client.NewClientWithOpts(client.WithHost("unix://"+directory+"/absent.sock"), client.WithVersion("1.47"))
	require.NoError(t, err)
	defer absent.Close()
	found, err = ImageExists(ctx, absent, resources.NewDockerImage("alpine:3.19.1"))
	require.Error(t, err)
	require.False(t, found)
}
