package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/manager"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
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

func TestCachedAgentDoesNotSubstituteAnEarlierSelection(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv("TEST_AGENT_CONTRACT", "")
	binary := filepath.Join(t.TempDir(), "agent")
	output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../agents/testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	selected := resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.test", Name: "peer", Version: "1.0.0"}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		installed := selected
		installed.Version = version
		path, err := installed.Path(t.Context())
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.Symlink(binary, path))
	}
	t.Run("connection", func(t *testing.T) {
		t.Cleanup(ClearAgents)
		original := selected
		first, err := LoadAgent(t.Context(), &original, "fixture/peer")
		require.NoError(t, err)
		replacement := selected
		replacement.Version = "2.0.0"
		_, err = LoadAgent(t.Context(), &replacement, "fixture/peer")
		require.ErrorContains(t, err, "agent selection changed")
		same, err := LoadAgent(t.Context(), &original, "fixture/peer")
		require.NoError(t, err)
		require.Equal(t, first.ProcessInfo.PID, same.ProcessInfo.PID)
		_, err = first.GetAgentInformation(t.Context(), &agentv0.AgentInformationRequest{})
		require.NoError(t, err)
		ClearAgent("fixture/peer")
		upgraded, err := LoadAgent(t.Context(), &replacement, "fixture/peer")
		require.NoError(t, err)
		require.Equal(t, "2.0.0", upgraded.Agent.Version)
		require.NotEqual(t, first.ProcessInfo.PID, upgraded.ProcessInfo.PID)
	})
	t.Run("instance", func(t *testing.T) {
		t.Cleanup(ClearAgents)
		original := selected
		service := &resources.Service{Name: "peer", Agent: &original}
		service.WithModule("fixture")
		first, err := Load(t.Context(), nil, nil, service)
		require.NoError(t, err)
		service.Agent.Version = "2.0.0"
		_, err = Load(t.Context(), nil, nil, service)
		require.ErrorContains(t, err, "agent selection changed")
		require.Equal(t, "1.0.0", first.Agent.Agent.Version)
		_, err = LoadBuilder(t.Context(), service)
		require.ErrorContains(t, err, "agent selection changed")
		_, err = LoadRuntime(t.Context(), service)
		require.ErrorContains(t, err, "agent selection changed")
		_, err = LoadCode(t.Context(), service)
		require.ErrorContains(t, err, "agent selection changed")
		_, err = first.Agent.GetAgentInformation(t.Context(), &agentv0.AgentInformationRequest{})
		require.NoError(t, err)
		ClearAgent(ServiceCacheKey(service))
		upgraded, err := Load(t.Context(), nil, nil, service)
		require.NoError(t, err)
		require.Equal(t, "2.0.0", upgraded.Agent.Agent.Version)
		require.NotEqual(t, first.ProcessInfo.AgentPID, upgraded.ProcessInfo.AgentPID)
	})
	t.Run("in-flight", func(t *testing.T) {
		t.Cleanup(ClearAgents)
		gate := filepath.Join(t.TempDir(), "start")
		t.Setenv("TEST_AGENT_START_GATE", gate)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		original := selected
		done := make(chan struct{})
		var loadErr error
		go func() {
			_, loadErr = LoadAgent(ctx, &original, "fixture/peer")
			close(done)
		}()
		t.Cleanup(func() { cancel(); <-done })
		require.Eventually(t, func() bool {
			connCacheMu.Lock()
			defer connCacheMu.Unlock()
			_, loading := connLoads["fixture/peer"]
			return loading
		}, 5*time.Second, 10*time.Millisecond)
		replacement := selected
		replacement.Version = "2.0.0"
		requestCtx, requestCancel := context.WithTimeout(ctx, time.Second)
		defer requestCancel()
		_, err := LoadAgent(requestCtx, &replacement, "fixture/peer")
		require.ErrorContains(t, err, "agent selection changed")
		require.NoError(t, os.WriteFile(gate, nil, 0o600))
		<-done
		require.NoError(t, loadErr)
		_, err = LoadAgent(ctx, &original, "fixture/peer")
		require.NoError(t, err)
	})
}

func TestUpdateAgentValidatesBeforeChangingTheSelection(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv(manager.AgentSourceEnv, "local")
	binary := filepath.Join(t.TempDir(), "agent")
	output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../agents/testdata/recoveryagent").CombinedOutput()
	require.NoError(t, err, "%s", output)
	candidate := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.test", Name: "peer", Version: "2.0.0"}
	installed, err := candidate.Path(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
	require.NoError(t, os.Symlink(binary, installed))
	for _, declaration := range []string{"future", "undeclared", "compatible"} {
		t.Run(declaration, func(t *testing.T) {
			t.Setenv("TEST_AGENT_CONTRACT", declaration)
			original := *candidate
			original.Version = "1.0.0"
			service := &resources.Service{Name: "peer", Agent: &original}
			dir := t.TempDir()
			require.NoError(t, service.SaveAtDir(t.Context(), dir))
			before := readServiceSelection(t, dir)
			updated, err := UpdateAgent(t.Context(), service)
			if declaration != "compatible" {
				require.ErrorContains(t, err, "incompatible agent")
				require.Nil(t, updated)
				require.Equal(t, "1.0.0", service.Agent.Version)
				require.Equal(t, before, readServiceSelection(t, dir))
			} else {
				require.NoError(t, err)
				require.Equal(t, "1.0.0", updated.From)
				require.Equal(t, "2.0.0", updated.To)
				require.Equal(t, "2.0.0", service.Agent.Version)
				loaded, err := resources.LoadServiceFromDir(t.Context(), dir)
				require.NoError(t, err)
				require.Equal(t, "2.0.0", loaded.Agent.Version)
			}
			require.Equal(t, "1.0.0", original.Version)
		})
	}
	t.Run("overwrite-refused", func(t *testing.T) {
		original := *candidate
		original.Version = "1.0.0"
		service := &resources.Service{Name: "peer", Agent: &original}
		dir := t.TempDir()
		require.NoError(t, service.SaveAtDir(t.Context(), dir))
		before := readServiceSelection(t, dir)
		ctx := shared.WithOverride(t.Context(), shared.SkipAll())
		updated, err := UpdateAgent(ctx, service)
		require.ErrorContains(t, err, "requires replacing")
		require.Nil(t, updated)
		require.Same(t, &original, service.Agent)
		require.Equal(t, "1.0.0", original.Version)
		require.Equal(t, before, readServiceSelection(t, dir))
	})
	t.Run("write-failure", func(t *testing.T) {
		t.Setenv("TEST_AGENT_CONTRACT", "compatible")
		original := *candidate
		original.Version = "1.0.0"
		service := &resources.Service{Name: "peer", Agent: &original}
		dir := t.TempDir()
		require.NoError(t, service.SaveAtDir(t.Context(), dir))
		configurationPath := filepath.Join(dir, resources.ServiceConfigurationName)
		require.NoError(t, os.Rename(configurationPath, configurationPath+".preserved"))
		require.NoError(t, os.Mkdir(configurationPath, 0o700))
		updated, err := UpdateAgent(t.Context(), service)
		require.Error(t, err)
		require.Nil(t, updated)
		require.Same(t, &original, service.Agent)
		require.Equal(t, "1.0.0", original.Version)
		require.FileExists(t, configurationPath+".preserved")
	})
}

func readServiceSelection(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, resources.ServiceConfigurationName))
	require.NoError(t, err)
	return data
}
