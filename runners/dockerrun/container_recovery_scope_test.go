package dockerrun

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	// Pre-upgrade containers carry the exact scope and no namespace at all.
	preUpgrade := ContainerRecoveryScope{id: scope.id}
	preUpgradeForeignScope := ContainerRecoveryScope{id: otherScope.id}
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
		// Docker cannot add a label to an existing container, so refusing these
		// would strand every container created before the namespace shipped.
		{name: "pre-upgrade same scope stopped", scope: preUpgrade, pid: deadPID, state: "exited", want: true},
		{name: "pre-upgrade same scope ephemeral", scope: preUpgrade, pid: deadPID, state: "running", ephemeral: true, want: true},
		{name: "pre-upgrade cross scope stays", scope: preUpgradeForeignScope, pid: deadPID, state: "running", ephemeral: true},
		{name: "missing PID", scope: scope, state: "exited"},
		{name: "malformed PID", scope: scope, pid: "invalid", state: "exited"},
		{name: "zero PID", scope: scope, pid: "0", state: "exited"},
		{name: "init PID", scope: scope, pid: "1", state: "exited", ephemeral: true},
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
	t.Run("host without durable identity", func(t *testing.T) {
		degraded := ContainerRecoveryScope{id: scope.id}
		require.False(t, staleContainerInScope(container.Summary{State: "exited", Labels: map[string]string{
			LabelCodeflyOwner: "true", LabelCodeflyRecoveryScope: scope.id,
			LabelCodeflyRecoveryNamespace: scope.namespace, LabelCodeflySession: deadPID,
		}}, degraded), "a caller that cannot prove a host identity must not reap a container bound to one")
		require.True(t, staleContainerInScope(container.Summary{State: "exited", Labels: map[string]string{
			LabelCodeflyOwner: "true", LabelCodeflyRecoveryScope: scope.id, LabelCodeflySession: deadPID,
		}}, degraded), "exact-scope recovery must survive the loss of a durable host identity")
	})
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
	// Every common Linux container base image supplies no machine ID, so an
	// unidentifiable host must narrow recovery rather than fail to resolve.
	degraded, err := newContainerRecoveryScope(home, workspace, "", "")
	require.NoError(t, err)
	require.Equal(t, first.id, degraded.id)
	require.Empty(t, degraded.namespace)
	require.NoError(t, SetContainerRecoveryScope(degraded))
	require.NoError(t, ReapStaleContainers(t.Context(), degraded))
	_, err = NewContainerRecoveryScope(filepath.Join(root, "missing"), workspace, "")
	require.Error(t, err)
	require.Error(t, SetContainerRecoveryScope(ContainerRecoveryScope{}))
	require.Error(t, ReapStaleContainers(t.Context(), ContainerRecoveryScope{}))
}

func TestContainerRecoveryScopeAgentProcess(t *testing.T) {
	if os.Getenv("RECOVERY_SCOPE_AGENT_TEST") == "1" {
		env := &DockerEnvironment{name: "test", image: &resources.DockerImage{Name: "alpine", Tag: "latest"}}
		config, _, err := env.desiredContainerConfigs(t.Context())
		if err != nil {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(config.Labels)
		os.Exit(0)
	}
	t.Setenv(ContainerRecoveryScopeEnvironment, "")
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "agent")
	require.NoError(t, err)
	require.NoError(t, SetContainerRecoveryScope(scope))
	env := &DockerEnvironment{name: "test", image: &resources.DockerImage{Name: "alpine", Tag: "latest"}}
	config, _, err := env.desiredContainerConfigs(t.Context())
	require.NoError(t, err)
	require.Equal(t, scope.id, config.Labels[LabelCodeflyRecoveryScope])
	require.Equal(t, scope.namespace, config.Labels[LabelCodeflyRecoveryNamespace])
	pid := strconv.Itoa(os.Getpid())
	tagged := func(digests ...string) string {
		return pid + ":" + containerRecoveryMarkerVersion + ":" + strings.Join(digests, ":")
	}
	for _, tc := range []struct {
		name, marker, want, namespace string
		wantError                     bool
	}{
		{"direct child", os.Getenv(ContainerRecoveryScopeEnvironment), scope.id, scope.namespace, false},
		{"legacy", "", "", "", false},
		{"stale parent", "999999999:" + containerRecoveryMarkerVersion + ":" + scope.id + ":" + scope.namespace, "", "", true},
		{"malformed identity", tagged("bad", scope.namespace), "", "", true},
		// A revision older than the layout tag projects the exact scope alone. It
		// stays labeled, but never delegates cross-scope recovery.
		{"untagged exact scope", pid + ":" + scope.id, scope.id, "", false},
		// A revision older than the tag also wrote pid:scope:group here. Reading
		// that group as a namespace stamped the container with ownership no sweep
		// could ever match, leaking it permanently — refuse instead of guessing.
		{"untagged trailing field", pid + ":" + scope.id + ":" + strings.Repeat("d", 64), "", "", true},
		{"scope and namespace", tagged(scope.id, scope.namespace), scope.id, scope.namespace, false},
		// A host with no durable identity projects an empty namespace field.
		{"scope without namespace", tagged(scope.id, ""), scope.id, "", false},
		{"missing namespace field", tagged(scope.id), "", "", true},
		{"malformed namespace", tagged(scope.id, "bad"), "", "", true},
		{"trailing field", tagged(scope.id, scope.namespace, strings.Repeat("e", 64)), "", "", true},
		{"malformed marker", "bad", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ContainerRecoveryScopeEnvironment, tc.marker)
			cmd := exec.Command(os.Args[0], "-test.run=^TestContainerRecoveryScopeAgentProcess$")
			cmd.Env = append(os.Environ(), "RECOVERY_SCOPE_AGENT_TEST=1")
			output, err := cmd.Output()
			if tc.wantError {
				require.Error(t, err, "invalid ownership must refuse container configuration")
				require.Empty(t, output)
				return
			}
			require.NoError(t, err)
			labels := map[string]string{}
			require.NoError(t, json.Unmarshal(output, &labels))
			require.Equal(t, tc.want, labels[LabelCodeflyRecoveryScope])
			require.Equal(t, tc.namespace, labels[LabelCodeflyRecoveryNamespace])
		})
	}
	t.Run("zero owner cannot claim namespace init", func(t *testing.T) {
		t.Setenv(ContainerRecoveryScopeEnvironment, tagged(scope.id, scope.namespace))
		require.NotEmpty(t, InheritedContainerRecoveryScope())
		t.Setenv(ContainerRecoveryScopeEnvironment, "0:"+containerRecoveryMarkerVersion+":"+scope.id+":"+scope.namespace)
		require.Empty(t, InheritedContainerRecoveryScope())
	})
	t.Run("acknowledgement echoes an empty namespace", func(t *testing.T) {
		// A compatible agent on a host with no durable identity must not look
		// like an agent that failed to understand the marker at all.
		t.Setenv(ContainerRecoveryScopeEnvironment, tagged(scope.id, ""))
		require.Equal(t, scope.id+":", InheritedContainerRecoveryScope())
	})
}

func TestContainerRecoveryScopeSurvivesParentExit(t *testing.T) {
	role := os.Getenv("RECOVERY_PARENT_EXIT_ROLE")
	ready := os.Getenv("RECOVERY_PARENT_EXIT_READY")
	if role == "parent" {
		scope, err := inheritedContainerRecoveryScope()
		require.NoError(t, err)
		require.NoError(t, SetContainerRecoveryScope(scope))
		SetEphemeralContainers(true)
		cmd := exec.Command(os.Args[0], "-test.run=^TestContainerRecoveryScopeSurvivesParentExit$")
		cmd.Env = append(os.Environ(), "RECOVERY_PARENT_EXIT_ROLE=agent")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		require.NoError(t, cmd.Start())
		require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 5*time.Second, time.Millisecond)
		os.Exit(0)
	}
	if role == "agent" {
		require.NoError(t, os.WriteFile(ready, nil, 0600))
		require.Eventually(t, func() bool { return os.Getppid() != containerRecoveryParentPID }, 5*time.Second, time.Millisecond)
		env := &DockerEnvironment{image: resources.NewDockerImage("alpine:latest")}
		config, _, err := env.desiredContainerConfigs(t.Context())
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(os.Stdout).Encode(config.Labels))
		os.Exit(0)
	}
	t.Setenv(ContainerRecoveryScopeEnvironment, "")
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "crash")
	require.NoError(t, err)
	require.NoError(t, SetContainerRecoveryScope(scope))
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestContainerRecoveryScopeSurvivesParentExit$")
	cmd.Env = append(os.Environ(), "RECOVERY_PARENT_EXIT_ROLE=parent", "RECOVERY_PARENT_EXIT_READY="+filepath.Join(t.TempDir(), "ready"))
	output, err := cmd.Output()
	require.NoError(t, err)
	labels := map[string]string{}
	require.NoError(t, json.Unmarshal(output, &labels))
	require.Equal(t, scope.id, labels[LabelCodeflyRecoveryScope])
	require.Equal(t, scope.namespace, labels[LabelCodeflyRecoveryNamespace])
	require.Equal(t, labelTrue, labels[LabelCodeflyEphemeral])
}
