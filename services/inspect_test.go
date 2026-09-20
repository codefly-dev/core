package services

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

func TestInspectAgentChecksTheRunningProtocolNotIdentityOrRelease(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	binary := filepath.Join(t.TempDir(), "agent")
	output, err := exec.Command("go", "build", "-o", binary, "../agents/testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	for _, tc := range []struct {
		name, version, declaration, wantError string
	}{
		{name: "unknown-old", version: "0.0.1"},
		{name: "unknown-new", version: "99.0.0"},
		{name: "unsupported", version: "99.0.0", declaration: "future", wantError: "host requires version"},
		{name: "undeclared", version: "99.0.0", declaration: "undeclared", wantError: "does not declare"},
		{name: "optional-features", version: "1.0.0", declaration: "no-recovery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_AGENT_CONTRACT", tc.declaration)
			selected := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.test", Name: tc.name, Version: tc.version}
			path, err := selected.Path(t.Context())
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.Symlink(binary, path))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			resolved, info, err := InspectAgent(ctx, selected)
			require.Equal(t, tc.version, selected.Version)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, resolved)
				require.Nil(t, info)
				return
			}
			require.NoError(t, err)
			require.Equal(t, selected, resolved)
			require.Contains(t, info.GetContract().GetCapabilities(), "fixture-feature/v1")
		})
	}
}
