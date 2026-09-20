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
	output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../agents/testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	for _, tc := range []struct {
		name, version, declaration, wantError string
	}{
		{name: "unknown-old", version: "0.0.1"},
		{name: "unknown-new", version: "99.0.0"},
		{name: "unsupported", version: "99.0.0", declaration: "future", wantError: "host requires version"},
		{name: "undeclared", version: "99.0.0", declaration: "undeclared", wantError: "does not declare"},
		{name: "server-declared", version: "1.0.0", declaration: "absent"},
		{name: "future-startup", version: "1.0.0", declaration: "future-startup", wantError: "startup protocol version 3"},
		{name: "undeclared-startup", version: "1.0.0", declaration: "undeclared-startup", wantError: "startup protocol version 0"},
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
			t.Cleanup(ClearAgents)
			loaded, loadErr := LoadAgent(ctx, selected, "fixture/"+tc.name)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, resolved)
				require.Nil(t, info)
				require.ErrorContains(t, loadErr, tc.wantError)
				require.Nil(t, loaded)
				connCacheMu.Lock()
				_, cached := connCache["fixture/"+tc.name]
				connCacheMu.Unlock()
				require.False(t, cached, "incompatible agents cannot back direct lifecycle clients")
				return
			}
			require.NoError(t, err)
			require.Equal(t, selected, resolved)
			if tc.declaration != "absent" {
				require.Contains(t, info.GetContract().GetCapabilities(), "fixture-feature/v1")
			}
			require.NoError(t, loadErr)
			require.NotNil(t, loaded)
		})
	}
}

func TestIndependentAgentAdmission(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv("GOWORK", "off")
	binary := filepath.Join(t.TempDir(), "peer")
	build := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "-o", binary, ".")
	build.Dir = filepath.Join("testdata", "independent-agent")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	selected := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "independent.example", Name: "peer", Version: "71.9.3"}
	path, err := selected.Path(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.Symlink(binary, path))
	for _, tc := range []struct{ declaration, wantError string }{
		{declaration: "compatible"},
		{declaration: "absent", wantError: "incompatible agent contract"},
		{declaration: "future", wantError: "incompatible agent contract"},
		{declaration: "undeclared-startup", wantError: "incompatible agent contract"},
		{declaration: "future-startup", wantError: "incompatible agent contract"},
		{declaration: "unimplemented", wantError: "agent information is unavailable"},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			t.Setenv("TEST_AGENT_CONTRACT", tc.declaration)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			t.Cleanup(ClearAgents)
			_, _, inspectErr := InspectAgent(ctx, selected)
			loaded, loadErr := LoadAgent(ctx, selected, "independent/peer")
			if tc.wantError == "" {
				require.NoError(t, inspectErr)
				require.NoError(t, loadErr)
				require.NotNil(t, loaded)
			} else {
				require.ErrorContains(t, inspectErr, tc.wantError)
				require.ErrorContains(t, loadErr, tc.wantError)
				require.Nil(t, loaded)
				connCacheMu.Lock()
				_, cached := connCache["independent/peer"]
				connCacheMu.Unlock()
				require.False(t, cached)
			}
		})
	}
}
