package base

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func recoveryOrphan(t *testing.T, leaderless bool) (int, string, *TrackedProcessGroup) {
	t.Helper()
	pid, path := spawnOrphanedGroup(t, leaderless)
	group, err := LookupProcessGroup(pid)
	require.NoError(t, err)
	t.Cleanup(func() { _ = group.Terminate(context.Background()); _ = group.RemoveIfDead() })
	return pid, path, group
}

func TestRecoveryExactGenerationAndControls(t *testing.T) {
	for _, leaderless := range []bool{false, true} {
		t.Run(map[bool]string{false: "leader", true: "credentialed descendants"}[leaderless], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			pid, path, _ := recoveryOrphan(t, leaderless)
			sibling := startRegistryLeader(t, "member")
			defer stopRegistryLeader(t, sibling)
			siblingPath := recordPath(t, sibling.Process.Pid)
			siblingBytes, err := os.ReadFile(siblingPath)
			require.NoError(t, err)
			// An owned, unregistered control: no recovery credential or attribution.
			foreign := registryHelperCommand("member")
			ready := filepath.Join(t.TempDir(), "ready")
			foreign.Env = append(foreign.Env, processGroupReadyFileEnv+"="+ready)
			foreign.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			require.NoError(t, foreign.Start())
			waitForTestFile(t, ready)
			identity, err := InspectProcess(foreign.Process.Pid)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, SignalProcess(t.Context(), identity, syscall.SIGTERM))
				require.NoError(t, foreign.Wait())
			}()
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			expected, err := PreviewProcessGroupRecovery(t.Context(), pid)
			require.NoError(t, err)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
			raw, err := json.Marshal(expected)
			require.NoError(t, err)
			decoded, err := ParseProcessGroupRecoveryExpectation(raw)
			require.NoError(t, err)
			require.Equal(t, *expected, decoded)
			result, err := RecoverProcessGroup(t.Context(), decoded)
			require.NoError(t, err)
			require.Equal(t, &ProcessGroupRecoveryResult{GroupDead: true, RecordRemoved: true}, result)
			_, err = os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
			observed, err := sibling.group.InspectOwnership(t.Context())
			require.NoError(t, err)
			require.True(t, observed.Authenticated)
			after, err = os.ReadFile(siblingPath)
			require.NoError(t, err)
			require.Equal(t, siblingBytes, after)
			current, err := InspectProcess(identity.PID)
			require.NoError(t, err)
			require.Equal(t, identity, current)
		})
	}
}

func TestRecoveryRefusesChangedEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, path, _ := recoveryOrphan(t, false)
	expected, err := PreviewProcessGroupRecovery(t.Context(), pid)
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	for name, mutate := range map[string]func(*ProcessGroupRecoveryExpectation){
		"target":           func(e *ProcessGroupRecoveryExpectation) { e.PGID++ },
		"record":           func(e *ProcessGroupRecoveryExpectation) { e.RecordSHA256 = strings.Repeat("0", 64) },
		"namespace":        func(e *ProcessGroupRecoveryExpectation) { e.Namespace = pgidPreviousNamespace },
		"leader birth":     func(e *ProcessGroupRecoveryExpectation) { e.Leader.StartID++ },
		"owner birth":      func(e *ProcessGroupRecoveryExpectation) { e.Owner.StartID++ },
		"boot":             func(e *ProcessGroupRecoveryExpectation) { e.Leader.BootID = "wrong" },
		"member birth":     func(e *ProcessGroupRecoveryExpectation) { e.Members[0].StartID++ },
		"member boot":      func(e *ProcessGroupRecoveryExpectation) { e.Members[0].BootID = "wrong" },
		"missing proof":    func(e *ProcessGroupRecoveryExpectation) { e.Members = nil },
		"duplicate member": func(e *ProcessGroupRecoveryExpectation) { e.Members = append(e.Members, e.Members[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *expected
			changed.Members = append([]ProcessIdentity(nil), expected.Members...)
			mutate(&changed)
			result, err := RecoverProcessGroup(t.Context(), changed)
			require.Error(t, err)
			require.False(t, result.RecordRemoved)
			assertGroupAlive(t, pid)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
	for _, change := range []string{"replacement", "birth", "duplicate namespace", "missing record"} {
		t.Run(change, func(t *testing.T) {
			oldDir, err := pgidNamespaceDir(pgidPreviousNamespace)
			require.NoError(t, err)
			oldPath := filepath.Join(oldDir, filepath.Base(path))
			defer func() { _ = os.Remove(oldPath); require.NoError(t, os.WriteFile(path, before, 0600)) }()
			switch change {
			case "replacement":
				rewriteRecord(t, path, func(r *pgidRecord) { r.Authentication = strings.Repeat("0", groupAuthBytes*2) })
			case "birth":
				rewriteRecord(t, path, func(r *pgidRecord) { r.Leader.StartID++ })
			case "duplicate namespace":
				group, err := LookupProcessGroup(pid)
				require.NoError(t, err)
				raw, err := json.Marshal(group.record)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(oldPath, raw, 0600))
			case "missing record":
				require.NoError(t, os.Remove(path))
			}
			selected, _ := os.ReadFile(path)
			result, err := RecoverProcessGroup(t.Context(), *expected)
			require.Error(t, err)
			require.False(t, result.RecordRemoved)
			assertGroupAlive(t, pid)
			after, _ := os.ReadFile(path)
			require.Equal(t, selected, after)
			if change == "birth" {
				_, err := PreviewProcessGroupRecovery(t.Context(), pid)
				require.Error(t, err)
			}
		})
	}
}

func TestRecoveryRefusesLiveOwnerAndMissingAuthentication(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	_, err := PreviewProcessGroupRecovery(t.Context(), leader.Process.Pid)
	require.ErrorContains(t, err, "owner")
	assertGroupAlive(t, leader.Process.Pid)
	pid, path, _ := recoveryOrphan(t, true)
	rewriteRecord(t, path, func(rec *pgidRecord) { rec.Authentication = strings.Repeat("0", groupAuthBytes*2) })
	_, err = PreviewProcessGroupRecovery(t.Context(), pid)
	require.Error(t, err)
	assertGroupAlive(t, pid)
}

func TestRecoveryExpectationStrictDecoding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, _, _ := recoveryOrphan(t, false)
	expected, err := PreviewProcessGroupRecovery(t.Context(), pid)
	require.NoError(t, err)
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	for name, raw := range map[string]string{
		"unknown":          strings.Replace(string(data), "{", `{"unknown":true,`, 1),
		"nested unknown":   strings.Replace(string(data), `"PID":`, `"unknown":true,"PID":`, 1),
		"duplicate":        strings.Replace(string(data), "{", `{"pgid":1,`, 1),
		"nested duplicate": strings.Replace(string(data), `"PID":`, `"PID":1,"PID":`, 1),
		"trailing":         string(data) + " {}",
		"null":             "null", "empty": "{}", "malformed": "{",
		"missing birth": strings.Replace(string(data), `"StartID":`, `"StartID":0,"old":`, 1),
	} {
		t.Run(name, func(t *testing.T) { _, err := ParseProcessGroupRecoveryExpectation([]byte(raw)); require.Error(t, err) })
	}
	assertGroupAlive(t, pid)
}

func TestRecoveryPreservesReplacementDuringTermination(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	pidPath, ready := filepath.Join(dir, "pid"), filepath.Join(dir, "ready")
	owner := registryHelperCommand("recovery-replacement-owner")
	owner.Env = append(owner.Env, processGroupPIDFileEnv+"="+pidPath, processGroupReadyFileEnv+"="+ready)
	require.NoError(t, owner.Run())
	raw, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	var pid int
	_, err = fmt.Sscan(string(raw), &pid)
	require.NoError(t, err)
	group, err := LookupProcessGroup(pid)
	require.NoError(t, err)
	defer func() { _ = group.Terminate(context.Background()) }()
	expected, err := PreviewProcessGroupRecovery(t.Context(), pid)
	require.NoError(t, err)
	result, err := RecoverProcessGroup(t.Context(), *expected)
	require.ErrorContains(t, err, "expected evidence")
	require.True(t, result.GroupDead)
	require.False(t, result.RecordRemoved)
	rec, _, err := readPgidRecord(recordPath(t, pid))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("0", groupAuthBytes*2), rec.Authentication)
}
