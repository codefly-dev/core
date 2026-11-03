//go:build !skip_infra

package dockerrun

import (
	"context"
	"sync"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// Only containers created by this test are removed; this never runs a sweep.
func TestRemoveDisposableContainerConcurrentRemoval(t *testing.T) {
	backend := newBackend(t)
	for range 10 {
		created, err := backend.client.ContainerCreate(t.Context(), &container.Config{Image: "alpine:latest", Cmd: []string{"sleep", "300"}}, &container.HostConfig{NetworkMode: "none"}, nil, nil, uniqueName(t))
		require.NoError(t, err)
		id := created.ID
		t.Cleanup(func() {
			_ = backend.client.ContainerRemove(context.Background(), id, container.RemoveOptions{Force: true})
		})
		require.NoError(t, backend.client.ContainerStart(t.Context(), id, container.StartOptions{}))
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		require.ErrorIs(t, removeDisposableContainer(cancelled, backend.client, id), context.Canceled)
		_, err = backend.client.ContainerInspect(t.Context(), id)
		require.NoError(t, err)
		start := make(chan struct{})
		errs := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for range 2 {
			go func() { ready.Done(); <-start; errs <- removeDisposableContainer(t.Context(), backend.client, id) }()
		}
		ready.Wait()
		close(start)
		first, second := <-errs, <-errs
		require.NoError(t, first)
		require.NoError(t, second)
		_, err = backend.client.ContainerInspect(t.Context(), id)
		require.True(t, errdefs.IsNotFound(err), "%v", err)
	}
}
