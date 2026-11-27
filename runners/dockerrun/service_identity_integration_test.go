//go:build !skip_infra

package dockerrun

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/recoveryscope"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func TestContainerParentServiceCreationAndReuse(t *testing.T) {
	t.Setenv(recoveryscope.EnvironmentVariable, "")
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "parent-service")
	require.NoError(t, err)
	require.NoError(t, SetContainerRecoveryScope(scope))
	for _, tc := range []struct {
		name, existing                    string
		legacy, changed, unbound, refused bool
	}{
		{name: "same service", existing: "web/gateway"},
		{name: "same service config change", existing: "web/gateway", changed: true},
		{name: "sibling cannot adopt", existing: "web/gateway-admin", refused: true},
		{name: "sibling cannot replace", existing: "web/gateway-admin", changed: true, refused: true},
		{name: "flattened collision", existing: "web-gateway/admin", changed: true, refused: true},
		{name: "unbound cannot adopt attributed", existing: "web/gateway", unbound: true, refused: true},
		{name: "malformed present cannot adopt", refused: true},
		{name: "legacy exact reuse without attribution", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case owns its container. The parent scope stays fixed.
			t.Parallel()
			name := uniqueName(t)
			newEnvironment := func() *DockerEnvironment {
				t.Helper()
				env, err := NewDockerHeadlessEnvironment(t.Context(), resources.NewDockerImage("alpine:latest"), name)
				require.NoError(t, err)
				t.Cleanup(func() { _ = env.client.Close() })
				env.WithCommand(gracefulWorkload("")...)
				return env
			}
			producer := newEnvironment()
			identity := &resources.ServiceIdentity{Module: "web", Name: "gateway", Version: "1.0.0"}
			require.NoError(t, producer.WithServiceIdentity(identity))
			identity.Name = "mutated"
			require.NoError(t, producer.Init(t.Context()))
			id := producer.instance.ID
			t.Cleanup(func() { _ = producer.Shutdown(context.Background()) })
			waitForWorkload(t, producer.client, id)
			inspect, err := inspectContainer(t.Context(), producer.client, id)
			require.NoError(t, err)
			require.Equal(t, "web/gateway", inspect.Config.Labels[LabelCodeflyService])
			require.Error(t, producer.WithServiceIdentity(identity), "first use freezes identity")
			// Docker labels are immutable. Seed legacy/foreign declarations by
			// recreating only this test's exact container, never shared state.
			if tc.legacy || tc.existing != "web/gateway" {
				require.NoError(t, producer.Shutdown(t.Context()))
				config, host, err := producer.desiredContainerConfigs(t.Context())
				require.NoError(t, err)
				config.Labels[LabelCodeflyService] = tc.existing
				if tc.legacy {
					delete(config.Labels, LabelCodeflyService)
				}
				created, err := producer.client.ContainerCreate(t.Context(), client.ContainerCreateOptions{Config: config, HostConfig: host, Name: producer.name})
				require.NoError(t, err)
				id = created.ID
				t.Cleanup(func() {
					_, _ = producer.client.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
				})
			}
			reuser := newEnvironment()
			t.Cleanup(func() { _ = reuser.Shutdown(context.Background()) })
			if !tc.unbound {
				require.NoError(t, reuser.WithServiceIdentity(&resources.ServiceIdentity{Module: "web", Name: "gateway", Version: "2.0.0"}))
			}
			if tc.changed {
				reuser.WithEnvironmentVariables(t.Context(), &resources.EnvironmentVariable{Key: "CONFIG_REVISION", Value: "2"})
			}
			err = reuser.Init(t.Context())
			if tc.refused {
				require.Error(t, err)
				require.Nil(t, reuser.instance, "refusal must not acquire the foreign instance")
				_, err = inspectContainer(t.Context(), producer.client, id)
				require.NoError(t, err, "refusal must not delete or replace")
				return
			}
			require.NoError(t, err)
			waitForWorkload(t, reuser.client, reuser.instance.ID)
			if tc.changed {
				require.NotEqual(t, id, reuser.instance.ID)
			} else {
				require.Equal(t, id, reuser.instance.ID)
			}
			inspect, err = inspectContainer(t.Context(), reuser.client, reuser.instance.ID)
			require.NoError(t, err)
			parent, err := ContainerServiceIdentity(inspect.Config.Labels)
			require.NoError(t, err)
			if tc.legacy {
				require.Nil(t, parent, "reuse must not fabricate or stamp legacy attribution")
			} else {
				require.Equal(t, "web/gateway", parent.Unique())
			}
		})
	}
}
