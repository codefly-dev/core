//go:build !skip_infra

package dockerrun_test

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

// TestImageExistsResolvesTheExactReference: an image imported here, never
// pulled, is present under its tag and under its own name@digest, which is the
// form a local build hands back; the same digest requested under another
// repository name is a reference the daemon cannot resolve and is reported
// absent, not present; and a daemon that cannot be reached is an error, not an
// absence.
func TestImageExistsResolvesTheExactReference(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer c.Close()

	// Imported from a tarball, so nothing is pulled: the image exists only here.
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	content := []byte("codefly image-exists test " + time.Now().String())
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "marker", Mode: 0o600, Size: int64(len(content))}))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	name := fmt.Sprintf("codefly-test/image-exists-%d", time.Now().UnixNano())
	tag := "local"
	body, err := c.ImageImport(ctx, image.ImportSource{Source: &tarball, SourceName: "-"}, name+":"+tag, image.ImportOptions{})
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
	inspected, err := c.ImageInspect(ctx, name+":"+tag)
	require.NoError(t, err)
	defer func() { _, _ = c.ImageRemove(ctx, inspected.ID, image.RemoveOptions{Force: true}) }()
	digest := inspected.ID
	require.True(t, strings.HasPrefix(digest, "sha256:"))

	byTag := &resources.DockerImage{Name: name, Tag: tag}
	exists, err := dockerrun.ImageExists(ctx, c, byTag)
	require.NoError(t, err)
	require.True(t, exists, "present under its tag")

	byDigest := &resources.DockerImage{Name: name, Tag: tag, Digest: digest}
	exists, err = dockerrun.ImageExists(ctx, c, byDigest)
	require.NoError(t, err)
	require.True(t, exists, "present under its own name@digest, the form a local build hands back")

	otherRepository := &resources.DockerImage{Repository: "example.invalid", Name: "review-no-such-image", Digest: digest}
	exists, err = dockerrun.ImageExists(ctx, c, otherRepository)
	require.NoError(t, err, "a reference the daemon cannot resolve is an answer, not a failure")
	require.False(t, exists, "the same digest under another repository name is not a reference this daemon resolves")

	otherTag := &resources.DockerImage{Name: name, Tag: "never-tagged"}
	exists, err = dockerrun.ImageExists(ctx, c, otherTag)
	require.NoError(t, err)
	require.False(t, exists)

	unreachable, err := client.NewClientWithOpts(client.WithHost("tcp://127.0.0.1:1"), client.WithVersion(c.ClientVersion()))
	require.NoError(t, err)
	defer unreachable.Close()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = dockerrun.ImageExists(deadline, unreachable, byTag)
	require.Error(t, err, "a daemon that cannot answer is an error, never an absence that leads to a pull")
	require.ErrorContains(t, err, "cannot inspect image")
}
