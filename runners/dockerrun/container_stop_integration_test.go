//go:build !skip_infra

package dockerrun

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/require"
)

func stopFixture(t *testing.T, name string, labels map[string]string, host *container.HostConfig, command string) (*DockerEnvironment, string) {
	t.Helper()
	if name == "" {
		name = ContainerName(uniqueName(t))
	}
	env, err := NewDockerHeadlessEnvironment(t.Context(), resources.NewDockerImage("alpine:latest"), name)
	require.NoError(t, err)
	if labels != nil {
		labels = maps.Clone(labels)
		labels[LabelCodeflyName] = name
	}
	response, err := env.client.ContainerCreate(t.Context(), &container.Config{Image: "alpine:latest", Labels: labels,
		Cmd: []string{"sh", "-c", command}}, host, nil, nil, name)
	require.NoError(t, err)
	t.Cleanup(func() {
		// This test created the exact ID; fixture disposal never resolves names.
		_ = env.client.ContainerRemove(context.Background(), response.ID, container.RemoveOptions{Force: true})
		_ = env.client.Close()
	})
	require.NoError(t, env.client.ContainerStart(t.Context(), response.ID, container.StartOptions{}))
	return env, response.ID
}

func TestContainerStopRetainsExactGenerationDataAndControls(t *testing.T) {
	backend := newBackend(t)
	vol, err := backend.client.VolumeCreate(t.Context(), volume.CreateOptions{Name: uniqueName(t)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.client.VolumeRemove(context.Background(), vol.Name, true) })
	labels := map[string]string{LabelCodeflyOwner: "true", LabelCodeflyRecoveryScope: "owned-stop-test", LabelCodeflyRecoveryNamespace: "owned-test-host"}
	env, id := stopFixture(t, "", labels, &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: vol.Name, Target: "/data"}}},
		"echo layer > /retained.tmp; mv /retained.tmp /retained; echo volume > /data/retained.tmp; mv /data/retained.tmp /data/retained; exec sleep 300")
	_, sibling := stopFixture(t, "", map[string]string{LabelCodeflyOwner: "true", LabelCodeflyRecoveryScope: "sibling"}, &container.HostConfig{}, "exec sleep 300")
	_, foreign := stopFixture(t, "", nil, &container.HostConfig{}, "exec sleep 300")
	controls := map[string]container.InspectResponse{}
	for _, control := range []string{sibling, foreign} {
		controls[control], err = backend.client.ContainerInspect(t.Context(), control)
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		_, err := env.client.ContainerStatPath(t.Context(), id, "/data/retained")
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	expected, err := PreviewContainerStop(t.Context(), id)
	require.NoError(t, err)
	require.NotContains(t, expected.Labels, LabelCodeflyService)
	require.NotContains(t, expected.Labels, LabelCodeflyInvocation)
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	parsed, err := ParseContainerStopExpectation(append(data, '\n'))
	require.NoError(t, err)
	for name, mutate := range map[string]func(*ContainerStopExpectation){
		"ID":        func(e *ContainerStopExpectation) { e.ID = sibling },
		"creation":  func(e *ContainerStopExpectation) { e.Created = time.Now().UTC().Format(time.RFC3339Nano) },
		"name":      func(e *ContainerStopExpectation) { e.Name += "-other"; e.Labels[LabelCodeflyName] = e.Name },
		"scope":     func(e *ContainerStopExpectation) { e.Labels[LabelCodeflyRecoveryScope] = "other" },
		"namespace": func(e *ContainerStopExpectation) { e.Labels[LabelCodeflyRecoveryNamespace] = "other" },
		"mount":     func(e *ContainerStopExpectation) { e.Mounts[0].Source += "-other" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := parsed
			changed.Labels, changed.Mounts = maps.Clone(parsed.Labels), slices.Clone(parsed.Mounts)
			mutate(&changed)
			result, err := StopContainer(t.Context(), changed)
			require.Error(t, err)
			require.False(t, result.Stopped)
			actual, err := env.client.ContainerInspect(t.Context(), id)
			require.NoError(t, err)
			require.True(t, actual.State.Running)
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = StopContainer(canceled, parsed)
	require.Error(t, err)
	result, err := StopContainer(t.Context(), parsed)
	require.NoError(t, err)
	require.Equal(t, &ContainerStopResult{Stopped: true, Retained: true, MountsUnchanged: true}, result)
	result, err = StopContainer(t.Context(), parsed)
	require.NoError(t, err, "same stopped generation is an idempotent retry")
	require.True(t, result.Stopped && result.Retained && result.MountsUnchanged)
	for file, want := range map[string]string{"/retained": "layer\n", "/data/retained": "volume\n"} {
		reader, _, err := env.client.CopyFromContainer(t.Context(), id, file)
		require.NoError(t, err)
		archive := tar.NewReader(reader)
		_, err = archive.Next()
		require.NoError(t, err)
		actual, err := io.ReadAll(archive)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, want, string(actual))
	}
	for control, before := range controls {
		after, err := env.client.ContainerInspect(t.Context(), control)
		require.NoError(t, err)
		require.True(t, after.State.Running)
		require.Equal(t, before.Created, after.Created)
		require.Equal(t, before.State.StartedAt, after.State.StartedAt)
		require.Equal(t, before.Config.Labels, after.Config.Labels)
		require.Equal(t, before.Mounts, after.Mounts)
	}
	// A new object with the same name is never resolved by an old receipt.
	require.NoError(t, env.client.ContainerRemove(t.Context(), id, container.RemoveOptions{}))
	_, replacement := stopFixture(t, expected.Name, labels, &container.HostConfig{}, "exec sleep 300")
	_, err = StopContainer(t.Context(), parsed)
	require.Error(t, err)
	actual, err := env.client.ContainerInspect(t.Context(), replacement)
	require.NoError(t, err)
	require.True(t, actual.State.Running)
}

func TestContainerStopRefusesAutoRemoveBeforeEffect(t *testing.T) {
	env, id := stopFixture(t, "", map[string]string{LabelCodeflyOwner: "true"}, &container.HostConfig{AutoRemove: true}, "exec sleep 300")
	_, err := PreviewContainerStop(t.Context(), id)
	require.ErrorContains(t, err, "AutoRemove")
	before, err := env.client.ContainerInspect(t.Context(), id)
	require.NoError(t, err)
	// Supplying an otherwise complete receipt cannot bypass execution's own
	// AutoRemove inspection. No manufactured ledger ownership is involved.
	expected := ContainerStopExpectation{ID: id, Created: before.Created, Name: before.Config.Labels[LabelCodeflyName],
		Labels: maps.Clone(before.Config.Labels), Mounts: []container.MountPoint{}}
	_, err = StopContainer(t.Context(), expected)
	require.ErrorContains(t, err, "AutoRemove")
	after, err := env.client.ContainerInspect(t.Context(), id)
	require.NoError(t, err)
	require.True(t, after.State.Running)
	require.Equal(t, before.State.StartedAt, after.State.StartedAt)
}

func TestContainerStopUnmountedEncoderRoundTrip(t *testing.T) {
	_, id := stopFixture(t, "", map[string]string{LabelCodeflyOwner: "true"}, &container.HostConfig{}, "exec sleep 300")
	expected, err := PreviewContainerStop(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, expected.Mounts)
	require.Empty(t, expected.Mounts)
	var output bytes.Buffer
	require.NoError(t, json.NewEncoder(&output).Encode(expected))
	parsed, err := ParseContainerStopExpectation(output.Bytes())
	require.NoError(t, err)
	result, err := StopContainer(t.Context(), parsed)
	require.NoError(t, err)
	require.Equal(t, &ContainerStopResult{Stopped: true, Retained: true, MountsUnchanged: true}, result)
}
