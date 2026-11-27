//go:build !skip_infra

package dockerrun

import (
	"archive/tar"
	"context"
	"io"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/recoveryscope"
	"github.com/moby/moby/client"
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
			t.Setenv(recoveryscope.EnvironmentVariable, "")
			if tc.first.id != "" {
				require.NoError(t, SetContainerRecoveryScope(tc.first))
			}
			name := uniqueName(t)
			first, err := NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("alpine:latest"), name)
			require.NoError(t, err)
			t.Cleanup(func() { _ = first.Shutdown(context.Background()) })
			// Publish only the completed fixture: shell redirection creates an
			// empty file before echo writes it, so existence alone otherwise
			// lets the ownership assertion race the writer and read no content.
			command := gracefulWorkload("echo retained > /retained.tmp && mv /retained.tmp /retained")
			first.WithCommand(command...)
			require.NoError(t, first.Init(ctx))
			originalID := first.instance.ID
			waitForWorkload(t, first.client, originalID)

			t.Setenv(recoveryscope.EnvironmentVariable, "")
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
				waitForWorkload(t, second.client, second.instance.ID)
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
			// Teardown is fenced to the generation Init acquired (#462), so a
			// refused Init leaves nothing to tear down. What matters is that the
			// foreign container survives, which the inspection below asserts;
			// whether Shutdown reports anything is not part of that contract.
			_ = second.Shutdown(ctx)
			inspected, err := inspectContainer(ctx, first.client, originalID)
			require.NoError(t, err)
			require.True(t, inspected.State.Running)
			copied, err := first.client.CopyFromContainer(ctx, originalID, client.CopyFromContainerOptions{SourcePath: "/retained"})
			require.NoError(t, err)
			data := copied.Content
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
