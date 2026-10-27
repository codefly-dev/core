package base_test

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

func TestTrackedSpawnUsesCurrentLaunchWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, parent := range []string{"current workspace", "", "absent"} {
		t.Run(parent, func(t *testing.T) {
			t.Setenv(base.LaunchWorkspaceEnv, parent)
			if parent == "absent" {
				require.NoError(t, os.Unsetenv(base.LaunchWorkspaceEnv))
			}
			cmd := exec.Command("sh", "-c", `printf '%s' "${CODEFLY_RUN_WORKSPACE-unset}:$SERVICE_VALUE"`)
			cmd.Env = []string{base.LaunchWorkspaceEnv + "=stale cache", "SERVICE_VALUE=retained"}
			original := append([]string(nil), cmd.Env...)
			var output bytes.Buffer
			cmd.Stdout = &output
			group, err := base.StartTrackedProcessGroup(cmd)
			require.NoError(t, err)
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = group.RemoveIfDead() })
			require.NoError(t, cmd.Wait())
			require.Equal(t, original, cmd.Env, "spawn must not mutate the caller's cached environment")
			expected := parent
			if parent == "absent" {
				expected = "unset"
			}
			require.Equal(t, expected+":retained", output.String())
			require.NoError(t, group.RemoveIfDead())
		})
	}
}
