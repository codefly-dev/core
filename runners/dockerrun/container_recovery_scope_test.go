package dockerrun

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func TestContainerRecoveryScopeIsolation(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	scope, err := NewContainerRecoveryScope(home, workspace, "candidate")
	require.NoError(t, err)
	otherHost, err := newContainerRecoveryScope(home, workspace, "candidate", "different-host")
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
		{name: "foreign host stopped", scope: otherHost, pid: deadPID, state: "exited", ephemeral: true},
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
			labels := map[string]string{LabelCodeflyOwner: "true", LabelCodeflyRecoveryScope: tc.scope.id, LabelCodeflyRecoveryNamespace: tc.scope.namespace, LabelCodeflySession: tc.pid}
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

func TestContainerRecoveryScopeSurvivesParentExit(t *testing.T) {
	if os.Getenv("RECOVERY_REPARENT_TEST") == "1" {
		require.NoError(t, os.WriteFile(os.Getenv("RECOVERY_TEST_READY"), nil, 0600))
		deadline := time.Now().Add(10 * time.Second)
		for os.Getppid() == containerRecoveryParentPID && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		require.NotEqual(t, containerRecoveryParentPID, os.Getppid())
		env := &DockerEnvironment{name: "test", image: &resources.DockerImage{Name: "alpine", Tag: "latest"}}
		require.NoError(t, json.NewEncoder(os.Stdout).Encode(env.createContainerConfig(t.Context()).Labels))
		os.Exit(0)
	}
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "orphan")
	require.NoError(t, err)
	// The short-lived shell is the real creator. It waits until the agent has
	// initialized its identity, then exits before the agent creates its config.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", `
export CODEFLY_CONTAINER_RECOVERY_SCOPE="$$:$RECOVERY_TEST_ID"
"$RECOVERY_TEST_BINARY" -test.run=^TestContainerRecoveryScopeSurvivesParentExit$ &
while [ ! -f "$RECOVERY_TEST_READY" ]; do sleep 0.01; done
`)
	command.Env = append(os.Environ(), "RECOVERY_REPARENT_TEST=1", "RECOVERY_TEST_ID="+scope.id+":"+scope.namespace, "RECOVERY_TEST_BINARY="+os.Args[0], "RECOVERY_TEST_READY="+filepath.Join(t.TempDir(), "ready"))
	output, err := command.Output()
	require.NoError(t, err)
	labels := map[string]string{}
	require.NoError(t, json.Unmarshal(output, &labels))
	require.Equal(t, scope.id, labels[LabelCodeflyRecoveryScope])
	require.Equal(t, scope.namespace, labels[LabelCodeflyRecoveryNamespace])
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
	_, err = newContainerRecoveryScope(home, workspace, "", "")
	require.ErrorContains(t, err, "stable host identity")
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
	require.NoError(t, SetContainerRecoveryScope(scope))
	env := &DockerEnvironment{name: "test", image: &resources.DockerImage{Name: "alpine", Tag: "latest"}}
	require.Equal(t, scope.id, env.createContainerConfig(t.Context()).Labels[LabelCodeflyRecoveryScope])
	for _, tc := range []struct{ name, marker, want, namespace string }{
		{"direct child", os.Getenv(ContainerRecoveryScopeEnvironment), scope.id, scope.namespace},
		{"legacy", "", "", ""},
		{"stale parent", "999999999:" + scope.id, "", ""},
		{"malformed identity", strconv.Itoa(os.Getpid()) + ":bad", "", ""},
		{"old CLI exact scope", strconv.Itoa(os.Getpid()) + ":" + scope.id, scope.id, ""},
		{"malformed namespace", strconv.Itoa(os.Getpid()) + ":" + scope.id + ":bad", "", ""},
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
			require.Equal(t, tc.namespace, labels[LabelCodeflyRecoveryNamespace])
		})
	}
}
