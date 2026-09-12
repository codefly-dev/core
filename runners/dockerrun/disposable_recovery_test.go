package dockerrun

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func TestDisposableRecoveryUsesDurableNamespaceAndEphemeralOwnership(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	previous, err := NewContainerRecoveryScope(home, workspace, "previous")
	require.NoError(t, err)
	next, err := NewContainerRecoveryScope(home, workspace, "next")
	require.NoError(t, err)
	require.NotEqual(t, previous.id, next.id)
	require.Equal(t, previous.namespace, next.namespace)
	foreignHost, err := newContainerRecoveryScope(home, workspace, "previous", "different-host")
	require.NoError(t, err)
	foreignHome, err := NewContainerRecoveryScope(t.TempDir(), workspace, "previous")
	require.NoError(t, err)
	foreignWorkspace, err := NewContainerRecoveryScope(home, t.TempDir(), "previous")
	require.NoError(t, err)
	owner := exec.Command("true")
	require.NoError(t, owner.Run())
	for _, tc := range []struct {
		name                            string
		scope                           ContainerRecoveryScope
		ephemeral, ledgered, live, want bool
	}{
		{"previous disposable", previous, true, false, false, true},
		{"previous stateful", previous, false, false, false, false},
		{"live owner", previous, true, false, true, false},
		{"ledger", previous, true, true, false, false},
		{"foreign host", foreignHost, true, false, false, false},
		{"foreign home", foreignHome, true, false, false, false},
		{"foreign workspace", foreignWorkspace, true, false, false, false},
		{"legacy exact scope only", ContainerRecoveryScope{id: previous.id}, true, false, false, false},
		{"missing exact scope", ContainerRecoveryScope{namespace: previous.namespace}, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{LabelCodeflyOwner: labelTrue, LabelCodeflyRecoveryScope: tc.scope.id, LabelCodeflyRecoveryNamespace: tc.scope.namespace, LabelCodeflySession: strconv.Itoa(owner.Process.Pid)}
			if tc.ephemeral {
				labels[LabelCodeflyEphemeral] = labelTrue
			}
			if tc.live {
				labels[LabelCodeflySession] = strconv.Itoa(os.Getpid())
			}
			if tc.ledgered {
				labels[LabelCodeflyInvocation] = "session"
			}
			for _, state := range []string{"running", "exited"} {
				require.Equal(t, tc.want, disposableContainerInNamespace(container.Summary{State: state, Labels: labels}, next))
			}
		})
	}
	t.Run("host without durable identity reaps nothing and reports why", func(t *testing.T) {
		// Resolution now degrades instead of failing, so this sweep has to say it
		// cannot run rather than return a silent success that reads as "nothing
		// to collect" while the leak it exists for resumes.
		degraded := ContainerRecoveryScope{id: previous.id}
		require.False(t, disposableContainerInNamespace(container.Summary{State: "running", Labels: map[string]string{
			LabelCodeflyOwner: labelTrue, LabelCodeflyRecoveryScope: previous.id,
			LabelCodeflyRecoveryNamespace: previous.namespace, LabelCodeflyEphemeral: labelTrue,
			LabelCodeflySession: strconv.Itoa(owner.Process.Pid),
		}}, degraded))
		require.NoError(t, ReapDisposableContainers(t.Context(), degraded))
		require.Error(t, ReapDisposableContainers(t.Context(), ContainerRecoveryScope{}))
	})
}
