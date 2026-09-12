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
		name                                                     string
		ephemeral, sameOwner, foreign, legacy, changed, unscoped bool
		// reject names the refusal expected, or is empty when reuse is allowed.
		// An unscoped caller is turned away by name-based lookup before reuse
		// validation is ever reached, so it reports that earlier refusal.
		reject string
	}{
		{name: "ephemeral cannot transfer", ephemeral: true, reject: "recover its owner explicitly"},
		{name: "own ephemeral can restart", ephemeral: true, sameOwner: true},
		{name: "stateful can survive agent restart"},
		{name: "foreign cannot reuse", foreign: true, reject: "recover its owner explicitly"},
		{name: "foreign cannot replace", foreign: true, changed: true, reject: "recover its owner explicitly"},
		{name: "legacy cannot adopt", legacy: true, reject: "recover its owner explicitly"},
		{name: "unscoped cannot adopt", unscoped: true, reject: "refusing adoption or replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := uniqueName(t)
			newEnvironment := func() *DockerEnvironment {
				t.Helper()
				env, err := NewDockerHeadlessEnvironment(t.Context(), resources.NewDockerImage("alpine:latest"), name)
				require.NoError(t, err)
				t.Cleanup(func() { _ = env.client.Close() })
				env.WithPause()
				if tc.ephemeral {
					env.WithEphemeral()
				}
				return env
			}
			env := newEnvironment()
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
			reuser := env
			if tc.unscoped {
				// Recovery ownership is fixed on an environment's first use, so
				// an unscoped caller is a fresh environment that never inherited
				// a marker — not this one with the ambient marker pulled out
				// from under its already-resolved identity.
				t.Setenv(ContainerRecoveryScopeEnvironment, "")
				reuser = newEnvironment()
			}
			err = reuser.GetContainer(t.Context())
			if tc.reject != "" {
				require.ErrorContains(t, err, tc.reject)
			} else {
				require.NoError(t, err)
			}
			inspected, err := backend.client.ContainerInspect(t.Context(), created.ID)
			require.NoError(t, err, "ownership rejection must not delete or replace the old container")
			if tc.reject != "" {
				require.False(t, inspected.State.Running, "ownership rejection must precede starting the old container")
			}
		})
	}
}
