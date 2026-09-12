//go:build !skip_infra

package dockerrun

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func TestContainerRecoveryRejectsAdoptionBeforeReuseOrReplacement(t *testing.T) {
	backend := newBackend(t)
	t.Setenv(ContainerRecoveryScopeEnvironment, "")
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "reuse")
	require.NoError(t, err)
	require.NoError(t, SetContainerRecoveryScope(scope))
	owner := exec.Command("true")
	require.NoError(t, owner.Run())
	for _, tc := range []struct {
		name                                                             string
		ephemeral, sameOwner, foreign, legacy, changed, unscoped, reject bool
	}{
		{name: "ephemeral cannot transfer", ephemeral: true, reject: true},
		{name: "own ephemeral can restart", ephemeral: true, sameOwner: true},
		{name: "stateful can survive agent restart"},
		{name: "foreign cannot reuse", foreign: true, reject: true},
		{name: "foreign cannot replace", foreign: true, changed: true, reject: true},
		{name: "legacy cannot adopt", legacy: true, reject: true},
		{name: "unscoped cannot adopt", unscoped: true, reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := NewDockerHeadlessEnvironment(t.Context(), resources.NewDockerImage("alpine:latest"), uniqueName(t))
			require.NoError(t, err)
			t.Cleanup(func() { _ = env.client.Close() })
			env.WithPause()
			if tc.ephemeral {
				env.WithEphemeral()
			}
			config, hostConfig, err := env.desiredContainerConfigs(t.Context())
			require.NoError(t, err)
			config.Labels[LabelCodeflySession] = strconv.Itoa(owner.Process.Pid)
			if tc.sameOwner {
				config.Labels[LabelCodeflySession] = strconv.Itoa(os.Getpid())
			}
			if tc.foreign {
				config.Labels[LabelCodeflyRecoveryNamespace] = "foreign"
			}
			if tc.legacy {
				delete(config.Labels, LabelCodeflyRecoveryNamespace)
			}
			if tc.changed {
				config.Labels[LabelCodeflyConfig] = "old-configuration"
			}
			created, err := backend.client.ContainerCreate(t.Context(), config, hostConfig, nil, nil, env.name)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = backend.client.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
			})
			if tc.unscoped {
				t.Setenv(ContainerRecoveryScopeEnvironment, "")
			}
			err = env.GetContainer(t.Context())
			if tc.reject {
				require.ErrorContains(t, err, "recover its owner explicitly")
			} else {
				require.NoError(t, err)
			}
			inspected, err := backend.client.ContainerInspect(t.Context(), created.ID)
			require.NoError(t, err, "ownership rejection must not delete or replace the old container")
			if tc.reject {
				require.False(t, inspected.State.Running, "ownership rejection must precede starting the old container")
			}
		})
	}
}
