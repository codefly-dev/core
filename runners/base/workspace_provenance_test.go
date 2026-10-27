package base

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceProvenanceSurvivesOwnerAndEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var groups []*TrackedProcessGroup
	for _, workspace := range []string{t.TempDir(), t.TempDir()} {
		t.Setenv(LaunchWorkspaceEnv, workspace)
		dir := t.TempDir()
		pidPath, readyPath := filepath.Join(dir, "pid"), filepath.Join(dir, "ready")
		owner := registryHelperCommand("workspace-owner")
		owner.Env = append(owner.Env, processGroupPIDFileEnv+"="+pidPath, processGroupReadyFileEnv+"="+readyPath)
		require.NoError(t, owner.Run())
		raw, err := os.ReadFile(pidPath)
		require.NoError(t, err)
		pid, err := strconv.Atoi(string(raw))
		require.NoError(t, err)
		t.Cleanup(func() { cleanupOrphanedGroup(pid, recordPath(t, pid)) })
		waitForTestFile(t, readyPath)
		group, err := LookupProcessGroup(pid)
		require.NoError(t, err)
		observation, err := group.InspectOwnership(t.Context())
		require.NoError(t, err)
		require.True(t, observation.Authenticated)
		require.False(t, observation.OwnerAlive)
		require.Equal(t, workspace, observation.LaunchWorkspace)
		// env -i removed even the authentication marker; the live leader's exact
		// birth remains the owner-supported proof, independent of its environment.
		credential, _, err := readProcessGroupAuthentication(pid)
		require.NoError(t, err)
		require.Empty(t, credential)
		groups = append(groups, group)
	}
	listed, err := ListProcessGroups(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.NoError(t, groups[0].Terminate(t.Context()))
	require.NoError(t, groups[0].RemoveIfDead())
	require.False(t, groups[0].Alive())
	require.True(t, groups[1].Alive())
	sibling, err := LookupProcessGroup(groups[1].PGID())
	require.NoError(t, err)
	observation, err := sibling.InspectOwnership(t.Context())
	require.NoError(t, err)
	require.True(t, observation.Authenticated)
	require.Equal(t, groups[1].record.LaunchWorkspace, observation.LaunchWorkspace)
}

func TestWorkspaceProvenanceNamespaceCompatibility(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(LaunchWorkspaceEnv, t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	path := recordPath(t, leader.Process.Pid)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	previousDir, err := pgidNamespaceDir(pgidPreviousNamespace)
	require.NoError(t, err)
	// An old strict reader scans only v1; a valid v2 must not be quarantined.
	_, err = sweepOnce(t.Context(), previousDir)
	require.NoError(t, err)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	previousPath := filepath.Join(previousDir, filepath.Base(path))
	require.NoError(t, os.WriteFile(previousPath, before, 0600))
	_, _, err = readPgidRecord(previousPath)
	require.ErrorContains(t, err, "unknown field")
	require.NoError(t, os.Remove(previousPath))
	// Actual old wire bytes remain readable without manufacturing provenance.
	oldBytes, err := json.Marshal(leader.group.record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(previousPath, oldBytes, 0600))
	_, err = LookupProcessGroup(leader.Process.Pid)
	require.ErrorContains(t, err, "multiple namespaces")
	listed, err := ListProcessGroups(t.Context())
	require.ErrorContains(t, err, "multiple namespaces")
	require.Nil(t, listed)
	require.NoError(t, os.Remove(path))
	oldGroup, err := LookupProcessGroup(leader.Process.Pid)
	require.NoError(t, err)
	leader.group = oldGroup
	listed, err = ListProcessGroups(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	observation, err := oldGroup.InspectOwnership(t.Context())
	require.NoError(t, err)
	require.True(t, observation.Authenticated)
	require.Empty(t, observation.LaunchWorkspace)
	require.NoError(t, ReapStaleProcessGroups(t.Context()))
	after, err = os.ReadFile(previousPath)
	require.NoError(t, err)
	require.Equal(t, oldBytes, after)
}

func TestWorkspaceProvenanceRequiresAuthenticatedBirth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(LaunchWorkspaceEnv, t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	stale := *leader.group
	stale.record.Leader.StartID++
	observation, err := stale.InspectOwnership(t.Context())
	require.NoError(t, err)
	require.False(t, observation.Authenticated)
	require.Empty(t, observation.LaunchWorkspace)
	require.True(t, leader.group.Alive())
}

func TestPreviousNamespaceOrphanRecovery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, path := spawnOrphanedGroup(t, false)
	t.Cleanup(func() { cleanupOrphanedGroup(pid, path) })
	rec, _, err := readPgidRecord(path)
	require.NoError(t, err)
	oldBytes, err := json.Marshal(rec)
	require.NoError(t, err)
	previousDir, err := pgidNamespaceDir(pgidPreviousNamespace)
	require.NoError(t, err)
	previousPath := filepath.Join(previousDir, filepath.Base(path))
	require.NoError(t, os.WriteFile(previousPath, oldBytes, 0600))
	require.NoError(t, os.Remove(path))
	require.NoError(t, ReapStaleProcessGroups(t.Context()))
	require.False(t, isProcessGroupAlive(pid))
	_, err = os.Stat(previousPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProcessGroupDiscoveryRejectsMisnamedRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	path := recordPath(t, leader.Process.Pid)
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, name := range []string{"0" + filepath.Base(path), strconv.Itoa(leader.Process.Pid+1) + ".pgid"} {
		t.Run(name, func(t *testing.T) {
			wrong := filepath.Join(filepath.Dir(path), name)
			require.NoError(t, os.WriteFile(wrong, original, 0600))
			defer os.Remove(wrong)
			listed, err := ListProcessGroups(t.Context())
			require.Error(t, err)
			require.Nil(t, listed)
			if name[0] != '0' {
				group, err := LookupProcessGroup(leader.Process.Pid + 1)
				require.ErrorContains(t, err, "does not match its filename")
				require.Nil(t, group)
			}
			after, err := os.ReadFile(wrong)
			require.NoError(t, err)
			require.Equal(t, original, after, "discovery must preserve evidence")
			require.True(t, leader.group.Alive())
		})
	}
}
