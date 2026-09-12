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
}
