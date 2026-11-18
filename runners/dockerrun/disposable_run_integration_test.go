//go:build !skip_infra

package dockerrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/recoveryscope"
	"github.com/codefly-dev/core/runners/runstate"
)

// stoppedRun is what a run that stopped leaves on the container runtime, made
// the way its agents make it: a store's container with its data directory
// (WithPersistentCacheMount, what the Postgres agent keeps a cluster in),
// stopped by the agent's own Stop, and a second container still running with
// an anonymous volume (an image that declares VOLUME: Redis, MinIO).
type stoppedRun struct {
	scope           ContainerRecoveryScope
	run             runstate.Run
	store, server   string
	dataDir, volume string
	client          client.APIClient
}

func leaveStoppedRun(t *testing.T, backend *SessionBackend, home, workspace, namingScope string, disposable bool) stoppedRun {
	t.Helper()
	scope, err := NewContainerRecoveryScope(home, workspace, namingScope)
	require.NoError(t, err)
	require.NoError(t, SetContainerRecoveryScope(scope))
	left := stoppedRun{scope: scope, client: backend.client,
		run: runstate.Run{Workspace: workspace, NamingScope: namingScope, Disposable: disposable}}

	store, err := NewDockerHeadlessEnvironment(t.Context(), resources.NewDockerImage("alpine:latest"), uniqueName(t)+"-store")
	require.NoError(t, err)
	store.WithPause()
	left.dataDir, err = store.WithPersistentCacheMount(t.Context(), "data", "/var/lib/store")
	require.NoError(t, err)
	require.NoError(t, store.Init(t.Context()))
	left.store = store.instance.ID
	t.Cleanup(func() { _ = store.Shutdown(context.Background()) })
	require.NoError(t, os.WriteFile(filepath.Join(left.dataDir, "PG_VERSION"), []byte("17\n"), 0o600))
	require.NoError(t, store.Stop(t.Context()))

	labels := map[string]string{LabelCodeflyOwner: labelTrue, LabelCodeflySession: "999999",
		LabelCodeflyRecoveryScope: scope.id, LabelCodeflyRecoveryNamespace: scope.namespace}
	name := uniqueName(t) + "-server"
	labels[LabelCodeflyName] = name
	created, err := backend.client.ContainerCreate(t.Context(), client.ContainerCreateOptions{Name: name,
		Config:     &container.Config{Image: "alpine:latest", Cmd: []string{"sleep", "300"}, Labels: labels, Volumes: map[string]struct{}{"/data": {}}},
		HostConfig: &container.HostConfig{NetworkMode: "none"}})
	require.NoError(t, err)
	left.server = created.ID
	t.Cleanup(func() {
		_, _ = backend.client.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	})
	_, err = backend.client.ContainerStart(t.Context(), created.ID, client.ContainerStartOptions{})
	require.NoError(t, err)
	inspect, err := inspectContainer(t.Context(), backend.client, created.ID)
	require.NoError(t, err)
	for _, point := range inspect.Mounts {
		if point.Type == mount.TypeVolume && point.Destination == "/data" {
			left.volume = point.Name
		}
	}
	require.NotEmpty(t, left.volume, "the server has no anonymous volume")
	return left
}

func (left stoppedRun) containerExists(t *testing.T, id string) bool {
	t.Helper()
	_, err := inspectContainer(t.Context(), left.client, id)
	if errdefs.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func (left stoppedRun) volumeExists(t *testing.T) bool {
	t.Helper()
	_, err := left.client.VolumeInspect(t.Context(), left.volume, client.VolumeInspectOptions{})
	if errdefs.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

// A disposable run's scope was generated for it and is never run again, so
// when the run ends nothing can come back for what it left on the container
// runtime: its containers, running or stopped, go, with the anonymous volumes
// they created and the data directories core keeps for them. A stop used to
// keep all three, for every kind of run.
func TestADisposableRunsContainersVolumesAndDataGoWhenItEnds(t *testing.T) {
	backend := newBackend(t)
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, home)
	t.Setenv(recoveryscope.EnvironmentVariable, "")
	// Another run of the same workspace, and a developer's own containers.
	other := leaveStoppedRun(t, backend, home, workspace, "invotherrun", true)
	developer := leaveStoppedRun(t, backend, home, workspace, "", false)
	left := leaveStoppedRun(t, backend, home, workspace, "invthisrun", true)

	// A cancelled removal fails and leaves the run to be removed again.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := RemoveDisposableRun(cancelled, left.scope, left.run)
	require.Error(t, err)
	require.True(t, left.containerExists(t, left.store))
	require.True(t, left.containerExists(t, left.server))

	removed, err := RemoveDisposableRun(t.Context(), left.scope, left.run)
	require.NoError(t, err)
	require.Len(t, removed.Containers, 2)
	require.Equal(t, []string{filepath.Dir(left.dataDir)}, removed.DataDirectories)
	require.False(t, left.containerExists(t, left.store), "the stopped store container of a disposable run was kept")
	require.False(t, left.containerExists(t, left.server), "the running container of a disposable run was kept")
	require.False(t, left.volumeExists(t), "the anonymous volume of a disposable run's container was kept")
	require.NoDirExists(t, filepath.Dir(left.dataDir), "the data directory of a disposable run's container was kept")

	for name, kept := range map[string]stoppedRun{"another run": other, "a developer's run": developer} {
		require.True(t, kept.containerExists(t, kept.store), "%s lost its store container", name)
		require.True(t, kept.containerExists(t, kept.server), "%s lost its server container", name)
		require.True(t, kept.volumeExists(t), "%s lost its volume", name)
		require.FileExists(t, filepath.Join(kept.dataDir, "PG_VERSION"), "%s lost its data", name)
	}

	// Again: nothing is left, and that is no error.
	removed, err = RemoveDisposableRun(t.Context(), left.scope, left.run)
	require.NoError(t, err)
	require.Empty(t, removed.Containers)
	require.Empty(t, removed.DataDirectories)
}

// A persistent run is a developer's: a scope they named, or none. Its
// containers are the ones the next run reuses and its data directory is their
// database, whatever the caller of the collection believes.
func TestAPersistentRunsContainersAndDataSurviveItsEnd(t *testing.T) {
	backend := newBackend(t)
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, home)
	t.Setenv(recoveryscope.EnvironmentVariable, "")
	for name, tc := range map[string]struct {
		scope    string
		declared bool
	}{
		"plain":                             {},
		"a scope the developer named":       {scope: "pinned"},
		"declared disposable with no scope": {declared: true},
	} {
		t.Run(name, func(t *testing.T) {
			left := leaveStoppedRun(t, backend, home, workspace, tc.scope, false)
			left.run.Disposable = tc.declared
			removed, err := RemoveDisposableRun(t.Context(), left.scope, left.run)
			require.NoError(t, err)
			require.Empty(t, removed.Containers)
			require.Empty(t, removed.DataDirectories)
			require.True(t, left.containerExists(t, left.store))
			require.True(t, left.containerExists(t, left.server))
			require.True(t, left.volumeExists(t))
			require.FileExists(t, filepath.Join(left.dataDir, "PG_VERSION"))
		})
	}
}

// What a disposable run does not own stays: a container of its scope bound to
// a session ledger, whose record decides what becomes of it, one another host
// created under the same paths, and a named volume, which is not the
// container's own.
func TestADisposableRunsEndLeavesWhatIsNotItsOwn(t *testing.T) {
	backend := newBackend(t)
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, home)
	scope, err := NewContainerRecoveryScope(home, workspace, "invthisrun")
	require.NoError(t, err)
	foreign, err := newContainerRecoveryScope(home, workspace, "invthisrun", "different-host")
	require.NoError(t, err)
	require.Equal(t, scope.id, foreign.id)
	named, err := backend.client.VolumeCreate(t.Context(), client.VolumeCreateOptions{Name: uniqueName(t)})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = backend.client.VolumeRemove(context.Background(), named.Volume.Name, client.VolumeRemoveOptions{Force: true})
	})
	create := func(namespace, invocation string) string {
		labels := map[string]string{LabelCodeflyOwner: labelTrue, LabelCodeflySession: "999999",
			LabelCodeflyRecoveryScope: scope.id, LabelCodeflyRecoveryNamespace: namespace}
		if invocation != "" {
			labels[LabelCodeflyInvocation] = invocation
		}
		created, err := backend.client.ContainerCreate(t.Context(), client.ContainerCreateOptions{Name: uniqueName(t),
			Config: &container.Config{Image: "alpine:latest", Cmd: []string{"sleep", "300"}, Labels: labels},
			HostConfig: &container.HostConfig{NetworkMode: "none",
				Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: named.Volume.Name, Target: "/named"}}}})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = backend.client.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
		})
		return created.ID
	}
	ledgered, otherHost, own := create(scope.namespace, "retained-session"), create(foreign.namespace, ""), create(scope.namespace, "")

	removed, err := RemoveDisposableRun(t.Context(), scope, runstate.Run{Workspace: workspace, NamingScope: "invthisrun", Disposable: true})
	require.NoError(t, err)
	require.Len(t, removed.Containers, 1)
	exists := func(id string) bool {
		_, err := inspectContainer(t.Context(), backend.client, id)
		return err == nil
	}
	require.False(t, exists(own))
	require.True(t, exists(ledgered), "a ledgered container is its session ledger's")
	require.True(t, exists(otherHost), "a container another host created is not this run's")
	_, err = backend.client.VolumeInspect(t.Context(), named.Volume.Name, client.VolumeInspectOptions{})
	require.NoError(t, err, "a named volume is not the container's own")
}
