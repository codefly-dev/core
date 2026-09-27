package dockerrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/require"
)

// A container's large value is written to a private host directory mounted
// read-only into the container, and the container's environment carries the
// path as the container sees it. No Docker daemon is needed to prove the
// declaration; running it is the integration suites' job.
func TestAContainerReceivesALargeValueThroughItsCarrierMount(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	large := resources.Env("CODEFLY__WORKSPACE_CONFIGURATION__RUNNABLE_BINDINGS__DESCRIPTOR_SET__AB", strings.Repeat("d", resources.FileCarrierThreshold+1))
	large.File = true
	docker := &DockerEnvironment{name: "carrier-test", envs: []*resources.EnvironmentVariable{resources.Env("CODEFLY__SMALL", "v"), large}}
	config, host := &container.Config{}, &container.HostConfig{}
	require.NoError(t, docker.deliverCarriers(config, host))
	t.Cleanup(docker.releaseCarriers)

	require.Contains(t, config.Env, "CODEFLY__SMALL=v")
	var path string
	for _, entry := range config.Env {
		require.Less(t, len(entry)+1, resources.MaxEnvironmentStringBytes)
		if key, value, _ := strings.Cut(entry, "="); key == resources.FileCarrierKey(large.Key) {
			path = value
		}
	}
	require.True(t, strings.HasPrefix(path, resources.ContainerFileCarrierMount+"/"), path)
	require.Len(t, host.Mounts, 1)
	require.Equal(t, mount.Mount{Type: mount.TypeBind, Source: docker.carrierRoot(), Target: resources.ContainerFileCarrierMount, ReadOnly: true}, host.Mounts[0])

	onHost := filepath.Join(docker.carrierRoot(), filepath.Base(path))
	content, err := os.ReadFile(onHost)
	require.NoError(t, err)
	require.Equal(t, large.ValueAsString(), string(content))
	info, err := os.Stat(onHost)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dir, err := os.Stat(docker.carrierRoot())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dir.Mode().Perm())

	docker.releaseCarriers()
	_, err = os.Stat(docker.carrierRoot())
	require.True(t, os.IsNotExist(err), "the carrier directory goes with the container")
}

// A container with nothing large is declared exactly as before: no mount.
func TestAContainerWithoutLargeValuesHasNoCarrierMount(t *testing.T) {
	docker := &DockerEnvironment{name: "carrier-none", envs: []*resources.EnvironmentVariable{resources.Env("CODEFLY__SMALL", "v")}}
	config, host := &container.Config{}, &container.HostConfig{}
	require.NoError(t, docker.deliverCarriers(config, host))
	require.Empty(t, host.Mounts)
	require.Equal(t, []string{"CODEFLY__SMALL=v"}, config.Env)
}

// A value to be delivered by file at exec cannot reach a container created
// without a carrier mount; it is refused, never silently inlined.
func TestAnExecCannotDeliverAFileIntoAContainerWithoutAMount(t *testing.T) {
	docker := &DockerEnvironment{name: "carrier-exec"}
	large := resources.Env("CODEFLY__LARGE", strings.Repeat("d", resources.FileCarrierThreshold+1))
	large.File = true
	_, err := docker.execCarriers([]*resources.EnvironmentVariable{large})
	require.ErrorIs(t, err, resources.ErrFileCarrier)
}
