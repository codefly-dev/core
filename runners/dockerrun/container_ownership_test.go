//go:build !skip_infra

package dockerrun

import (
	"archive/tar"
	"context"
	"io"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestContainerOwnershipGuardsReuseReplacementAndShutdown(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	owner, err := NewContainerRecoveryScope(home, workspace, "dev")
	require.NoError(t, err)
	otherHome, err := NewContainerRecoveryScope(t.TempDir(), workspace, "dev")
	require.NoError(t, err)
	otherWorkspace, err := NewContainerRecoveryScope(home, t.TempDir(), "dev")
	require.NoError(t, err)
	otherScope, err := NewContainerRecoveryScope(home, workspace, "other")
	require.NoError(t, err)
	for _, tc := range []struct {
		name              string
		first, second     ContainerRecoveryScope
		changed, rejected bool
	}{
		{"foreign home reuse", owner, otherHome, false, true},
		{"foreign home replace", owner, otherHome, true, true},
		{"foreign workspace", owner, otherWorkspace, true, true},
		{"foreign naming scope", owner, otherScope, true, true},
		{"legacy cannot be claimed", ContainerRecoveryScope{}, owner, true, true},
		{"unscoped cannot claim scoped", owner, ContainerRecoveryScope{}, true, true},
		{"same scope reuse", owner, owner, false, false},
		{"same scope replace", owner, owner, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			t.Setenv(ContainerRecoveryScopeEnvironment, "")
			if tc.first.id != "" {
				require.NoError(t, SetContainerRecoveryScope(tc.first))
			}
			name := uniqueName(t)
			first, err := NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("alpine:latest"), name)
			require.NoError(t, err)
			t.Cleanup(func() { _ = first.Shutdown(context.Background()) })
			command := []string{"sh", "-c", "echo retained > /retained; exec sleep 300"}
			first.WithCommand(command...)
			require.NoError(t, first.Init(ctx))
			originalID := first.instance.ID
			require.Eventually(t, func() bool {
				_, err := first.client.ContainerStatPath(ctx, originalID, "/retained")
				return err == nil
			}, 5*time.Second, 10*time.Millisecond)

			t.Setenv(ContainerRecoveryScopeEnvironment, "")
			if tc.second.id != "" {
				require.NoError(t, SetContainerRecoveryScope(tc.second))
			}
			second, err := NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("alpine:latest"), name)
			require.NoError(t, err)
			t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
			second.WithCommand(command...)
			if tc.changed {
				second.WithUser("0")
			}
			err = second.Init(ctx)
			if !tc.rejected {
				require.NoError(t, err)
				if tc.changed {
					require.NotEqual(t, originalID, second.instance.ID)
				} else {
					require.Equal(t, originalID, second.instance.ID)
				}
				return
			}
			require.ErrorContains(t, err, "refusing adoption or replacement")
			require.Nil(t, second.instance)
			// Failed initialization must not let teardown rediscover and delete
			// the very foreign container that initialization refused to claim.
			require.ErrorContains(t, second.Shutdown(ctx), "refusing adoption or replacement")
			inspected, err := first.client.ContainerInspect(ctx, originalID)
			require.NoError(t, err)
			require.True(t, inspected.State.Running)
			data, _, err := first.client.CopyFromContainer(ctx, originalID, "/retained")
			require.NoError(t, err)
			defer data.Close()
			archive := tar.NewReader(data)
			_, err = archive.Next()
			require.NoError(t, err)
			content, err := io.ReadAll(archive)
			require.NoError(t, err)
			require.Equal(t, "retained\n", string(content))
			// The first environment keeps its original scope despite the second
			// flow changing the ambient marker.
			require.NoError(t, first.Shutdown(ctx))
		})
	}
}
