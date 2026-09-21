package sdk

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestDirectSDKAgentAdmissionPrecedesServiceCreation(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	binary := filepath.Join(t.TempDir(), "agent")
	output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../agents/testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	selected := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "sdk-peer", Version: "71.0.0"}
	installed, err := selected.Path(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
	require.NoError(t, os.Symlink(binary, installed))
	for _, tc := range []struct {
		declaration, wantError string
		admitted               bool
	}{
		{declaration: "undeclared", wantError: "does not declare"},
		{declaration: "future", wantError: "host requires version"},
		{declaration: "future-startup", wantError: "startup protocol version"},
		{declaration: "sdk-no-builder", wantError: "missing capability BUILDER"},
		{declaration: "sdk-no-runtime", wantError: "missing capability RUNTIME"},
		{declaration: "sdk-compatible", wantError: "builder.Load:", admitted: true},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			t.Setenv("TEST_AGENT_CONTRACT", tc.declaration)
			env := New()
			env.tmpDir = t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			err := env.startAgent(ctx, "sdk-peer")
			require.ErrorContains(t, err, tc.wantError)
			entries, err := os.ReadDir(env.tmpDir)
			require.NoError(t, err)
			if tc.admitted {
				require.NotEmpty(t, entries)
			} else {
				require.Empty(t, entries, "admission must precede service files and lifecycle calls")
			}
			require.Empty(t, env.running)
		})
	}
}
