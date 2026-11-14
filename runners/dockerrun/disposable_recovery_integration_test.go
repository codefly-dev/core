//go:build !skip_infra

package dockerrun

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func TestDisposableRecoveryAcrossScopesPreservesOtherOwnersAndVolumes(t *testing.T) {
	backend := newBackend(t)
	home, workspace := t.TempDir(), t.TempDir()
	old, err := NewContainerRecoveryScope(home, workspace, "old-disposable")
	require.NoError(t, err)
	next, err := NewContainerRecoveryScope(home, workspace, "new-disposable")
	require.NoError(t, err)
	foreignHost, err := newContainerRecoveryScope(home, workspace, "old-disposable", "different-host")
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
	vol, err := backend.client.VolumeCreate(t.Context(), client.VolumeCreateOptions{Name: uniqueName(t)})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = backend.client.VolumeRemove(context.Background(), vol.Volume.Name, client.VolumeRemoveOptions{Force: true})
	})
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
		{name: "foreign host", scope: foreignHost, ephemeral: true, keep: true},
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
			created, err := backend.client.ContainerCreate(t.Context(), client.ContainerCreateOptions{Config: &container.Config{Image: "alpine:latest", Cmd: []string{"sleep", "300"}, Labels: labels}, HostConfig: &container.HostConfig{NetworkMode: "none", Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: vol.Volume.Name, Target: "/data"}}}, Name: uniqueName(t)})
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = backend.client.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
			})
			if tc.running {
				_, err := backend.client.ContainerStart(t.Context(), created.ID, client.ContainerStartOptions{})
				require.NoError(t, err)
			}
			// Cancellation must leave ownership and resources available for retry.
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			require.Error(t, ReapDisposableContainers(cancelled, next))
			_, err = inspectContainer(t.Context(), backend.client, created.ID)
			require.NoError(t, err)
			require.NoError(t, ReapDisposableContainers(t.Context(), next))
			_, err = inspectContainer(t.Context(), backend.client, created.ID)
			if tc.keep {
				require.NoError(t, err)
			} else {
				require.True(t, errdefs.IsNotFound(err), "%v", err)
			}
			_, err = backend.client.VolumeInspect(t.Context(), vol.Volume.Name, client.VolumeInspectOptions{})
			require.NoError(t, err, "recovery must never delete volumes")
		})
	}
}
