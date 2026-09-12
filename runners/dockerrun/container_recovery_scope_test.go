package dockerrun

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/sdk/session"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func disposableRecoveryScope(t *testing.T, home, workspace, label, identity string) ContainerRecoveryScope {
	t.Helper()
	t.Setenv(EphemeralContainersEnvironment, strconv.Itoa(os.Getppid()))
	t.Setenv(session.IDEnvironment, identity)
	t.Setenv(session.SecretEnvironment, "test-secret")
	invocation := &session.Session{ID: identity}
	name := invocation.Scope()
	if label != "" {
		name = label + "-" + name
	}
	scope, err := NewContainerRecoveryScope(home, workspace, name)
	require.NoError(t, err)
	require.NotEmpty(t, scope.group)
	return scope
}

func TestDisposableRecoveryGroupRequiresInvocationAndMode(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	first := disposableRecoveryScope(t, home, workspace, "tests", strings.Repeat("a", 32))
	second := disposableRecoveryScope(t, home, workspace, "tests", strings.Repeat("b", 32))
	require.NotEqual(t, first.id, second.id)
	require.Equal(t, first.group, second.group)
	otherLabel := disposableRecoveryScope(t, home, workspace, "other", strings.Repeat("c", 32))
	require.NotEqual(t, first.group, otherLabel.group)
	otherHome := disposableRecoveryScope(t, t.TempDir(), workspace, "tests", strings.Repeat("d", 32))
	require.NotEqual(t, first.group, otherHome.group)
	otherWorkspace := disposableRecoveryScope(t, home, t.TempDir(), "tests", strings.Repeat("e", 32))
	require.NotEqual(t, first.group, otherWorkspace.group)
	for _, tc := range []struct{ name, identity, marker, scope string }{
		{"reusable", strings.Repeat("a", 32), "", "tests-saaaaaaaaaaaa"},
		{"not an invocation", "", strconv.Itoa(os.Getppid()), "tests-saaaaaaaaaaaa"},
		{"malformed invocation", "a", strconv.Itoa(os.Getppid()), "tests-sa"},
		{"different invocation", strings.Repeat("b", 32), strconv.Itoa(os.Getppid()), "tests-saaaaaaaaaaaa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(session.IDEnvironment, tc.identity)
			t.Setenv(EphemeralContainersEnvironment, tc.marker)
			scope, err := NewContainerRecoveryScope(home, workspace, tc.scope)
			require.NoError(t, err)
			require.Empty(t, scope.group)
		})
	}
}

func TestContainerRecoveryScopeIsolation(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	scope, err := NewContainerRecoveryScope(home, workspace, "candidate")
	require.NoError(t, err)
	otherHome, err := NewContainerRecoveryScope(t.TempDir(), workspace, "candidate")
	require.NoError(t, err)
	otherScope, err := NewContainerRecoveryScope(home, workspace, "foreign")
	require.NoError(t, err)
	otherWorkspace, err := NewContainerRecoveryScope(home, t.TempDir(), "candidate")
	require.NoError(t, err)
	child := exec.Command("true")
	require.NoError(t, child.Run())
	deadPID := strconv.Itoa(child.Process.Pid)
	for _, tc := range []struct {
		name                string
		scope               ContainerRecoveryScope
		pid, state          string
		ephemeral, ledgered bool
		want                bool
	}{
		{name: "same scope stopped", scope: scope, pid: deadPID, state: "exited", want: true},
		{name: "same scope ephemeral", scope: scope, pid: deadPID, state: "running", ephemeral: true, want: true},
		{name: "same scope stateful reuse", scope: scope, pid: deadPID, state: "running"},
		{name: "same scope live owner", scope: scope, pid: strconv.Itoa(os.Getpid()), state: "exited", ephemeral: true},
		{name: "same scope ledger", scope: scope, pid: deadPID, state: "exited", ledgered: true},
		{name: "foreign home stopped", scope: otherHome, pid: deadPID, state: "exited"},
		{name: "foreign scope ephemeral", scope: otherScope, pid: deadPID, state: "running", ephemeral: true},
		{name: "foreign workspace stopped", scope: otherWorkspace, pid: deadPID, state: "exited"},
		{name: "legacy no scope", pid: deadPID, state: "exited", ephemeral: true},
		{name: "missing PID", scope: scope, state: "exited"},
		{name: "malformed PID", scope: scope, pid: "invalid", state: "exited"},
		{name: "zero PID", scope: scope, pid: "0", state: "exited"},
		{name: "negative PID", scope: scope, pid: "-1", state: "exited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{LabelCodeflyOwner: "true", LabelCodeflyRecoveryScope: tc.scope.id, LabelCodeflySession: tc.pid}
			if tc.ephemeral {
				labels[LabelCodeflyEphemeral] = "true"
			}
			if tc.ledgered {
				labels[LabelCodeflyInvocation] = "ledger"
			}
			c := container.Summary{State: tc.state, Labels: labels}
			require.Equal(t, tc.want, staleContainerInScope(c, scope))
			require.False(t, staleContainerInScope(c, ContainerRecoveryScope{}))
			delete(labels, LabelCodeflyOwner)
			require.False(t, staleContainerInScope(c, scope))
		})
	}
}

func TestContainerRecoveryScopeCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	home, workspace := filepath.Join(root, "home"), filepath.Join(root, "workspace")
	require.NoError(t, os.Mkdir(home, 0700))
	require.NoError(t, os.Mkdir(workspace, 0700))
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(home, alias))
	first, err := NewContainerRecoveryScope(home, workspace, "")
	require.NoError(t, err)
	second, err := NewContainerRecoveryScope(alias, workspace, "")
	require.NoError(t, err)
	require.Equal(t, first, second)
	_, err = NewContainerRecoveryScope("", workspace, "")
	require.Error(t, err)
	_, err = NewContainerRecoveryScope(filepath.Join(root, "missing"), workspace, "")
	require.Error(t, err)
	require.Error(t, SetContainerRecoveryScope(ContainerRecoveryScope{}))
	require.Error(t, ReapStaleContainers(t.Context(), ContainerRecoveryScope{}))
}

func TestContainerRecoveryScopeAgentProcess(t *testing.T) {
	if os.Getenv("RECOVERY_SCOPE_AGENT_TEST") == "1" {
		env := &DockerEnvironment{name: "test", image: &resources.DockerImage{Name: "alpine", Tag: "latest"}}
		_ = json.NewEncoder(os.Stdout).Encode(env.createContainerConfig(t.Context()).Labels)
		os.Exit(0)
	}
	t.Setenv(ContainerRecoveryScopeEnvironment, "")
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "agent")
	require.NoError(t, err)
	scope.group = strings.Repeat("c", 64)
	require.NoError(t, SetContainerRecoveryScope(scope))
	env := &DockerEnvironment{name: "test", image: &resources.DockerImage{Name: "alpine", Tag: "latest"}}
	require.Equal(t, scope.id, env.createContainerConfig(t.Context()).Labels[LabelCodeflyRecoveryScope])
	for _, tc := range []struct{ name, marker, want string }{
		{"direct child", os.Getenv(ContainerRecoveryScopeEnvironment), scope.id},
		{"legacy", "", ""},
		{"stale parent", "999999999:" + scope.id, ""},
		{"malformed identity", strconv.Itoa(os.Getpid()) + ":bad", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ContainerRecoveryScopeEnvironment, tc.marker)
			cmd := exec.Command(os.Args[0], "-test.run=^TestContainerRecoveryScopeAgentProcess$")
			cmd.Env = append(os.Environ(), "RECOVERY_SCOPE_AGENT_TEST=1")
			output, err := cmd.Output()
			require.NoError(t, err)
			labels := map[string]string{}
			require.NoError(t, json.Unmarshal(output, &labels))
			require.Equal(t, tc.want, labels[LabelCodeflyRecoveryScope])
			if tc.want != "" {
				require.Equal(t, scope.group, labels[LabelCodeflyRecoveryGroup])
			}
		})
	}
}
