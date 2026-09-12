//go:build !skip_infra

package dockerrun

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
	"github.com/stretchr/testify/require"
)

func TestDisposableRecoveryAcrossScopesPreservesOtherOwnersAndVolumes(t *testing.T) {
	backend := newBackend(t)
	home, workspace := t.TempDir(), t.TempDir()
	old, err := NewContainerRecoveryScope(home, workspace, "old-disposable")
	require.NoError(t, err)
	next, err := NewContainerRecoveryScope(home, workspace, "new-disposable")
	require.NoError(t, err)
	foreignHome, err := NewContainerRecoveryScope(t.TempDir(), workspace, "old-disposable")
	require.NoError(t, err)
	foreignWorkspace, err := NewContainerRecoveryScope(home, t.TempDir(), "old-disposable")
	require.NoError(t, err)
	unregistered, err := NewContainerRecoveryScope(home, workspace, "unregistered")
	require.NoError(t, err)
	process := exec.Command("true")
	require.NoError(t, process.Run())
	deadPID := strconv.Itoa(process.Process.Pid)
	vol, err := backend.client.VolumeCreate(t.Context(), volume.CreateOptions{Name: uniqueName(t)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.client.VolumeRemove(context.Background(), vol.Name, true) })
	for _, tc := range []struct {
		name                                     string
		scope                                    ContainerRecoveryScope
		ephemeral, ledgered, live, running, keep bool
	}{
		{name: "orphan", scope: old, ephemeral: true, running: true},
		{name: "stopped orphan", scope: old, ephemeral: true},
		{name: "stateful", scope: old, keep: true},
		{name: "ledger", scope: old, ephemeral: true, ledgered: true, keep: true},
		{name: "live agent", scope: old, ephemeral: true, live: true, keep: true},
		{name: "foreign home", scope: foreignHome, ephemeral: true, keep: true},
		{name: "foreign workspace", scope: foreignWorkspace, ephemeral: true, keep: true},
		{name: "another ephemeral scope", scope: unregistered, ephemeral: true},
		{name: "legacy", ephemeral: true, keep: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{LabelCodeflyOwner: labelTrue, LabelCodeflySession: deadPID, LabelCodeflyRecoveryScope: tc.scope.id, LabelCodeflyRecoveryNamespace: tc.scope.namespace}
			if tc.ephemeral {
				labels[LabelCodeflyEphemeral] = labelTrue
			}
			if tc.ledgered {
				labels[LabelCodeflyInvocation] = "retained-session"
			}
			if tc.live {
				labels[LabelCodeflySession] = strconv.Itoa(os.Getpid())
			}
			created, err := backend.client.ContainerCreate(t.Context(), &container.Config{Image: "alpine:latest", Cmd: []string{"sleep", "300"}, Labels: labels}, &container.HostConfig{NetworkMode: "none", Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: vol.Name, Target: "/data"}}}, nil, nil, uniqueName(t))
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = backend.client.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
			})
			if tc.running {
				require.NoError(t, backend.client.ContainerStart(t.Context(), created.ID, container.StartOptions{}))
			}
			// Cancellation must leave ownership and resources available for retry.
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			require.Error(t, ReapDisposableContainers(cancelled, next))
			_, err = backend.client.ContainerInspect(t.Context(), created.ID)
			require.NoError(t, err)
			require.NoError(t, ReapDisposableContainers(t.Context(), next))
			_, err = backend.client.ContainerInspect(t.Context(), created.ID)
			if tc.keep {
				require.NoError(t, err)
			} else {
				require.True(t, errdefs.IsNotFound(err), "%v", err)
			}
			_, err = backend.client.VolumeInspect(t.Context(), vol.Name)
			require.NoError(t, err, "recovery must never delete volumes")
		})
	}
}
