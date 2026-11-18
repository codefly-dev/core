package runstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestListSaysWhatEveryRecordedDirectoryIs: a persistent run's directory is
// never collected, so ordered-stopped runs leave one each for good (178
// Postgres ones on one machine). List is how a person sees them: every
// directory a run is recorded on, whose it is, whether anything still uses
// it, and when a run last claimed it. It removes nothing.
func TestListSaysWhatEveryRecordedDirectoryIs(t *testing.T) {
	root := t.TempDir()
	stopped := stateDir(t, root, "postgres", "run", "1111")
	_, err := Claim(stopped, persistent)
	require.NoError(t, err)
	endRun(t, stopped)
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	for _, path := range []string{
		filepath.Join(stopped, markerDir),
		filepath.Join(stopped, markerDir, runFile),
		filepath.Join(stopped, markerDir, holdersDir),
	} {
		require.NoError(t, os.Chtimes(path, old, old))
	}
	holders, err := filepath.Glob(filepath.Join(stopped, markerDir, holdersDir, "*.json"))
	require.NoError(t, err)
	for _, path := range holders {
		require.NoError(t, os.Chtimes(path, old, old))
	}

	running := stateDir(t, root, "redis", "2222")
	_, err = Claim(running, persistent)
	require.NoError(t, err)
	thrown := stateDir(t, root, "redis", "3333")
	_, err = Claim(thrown, disposable)
	require.NoError(t, err)
	endRun(t, thrown)
	elsewhere := stateDir(t, root, "clickhouse", "4444")
	_, err = Claim(elsewhere, Run{Workspace: "/work/other"})
	require.NoError(t, err)
	stateDir(t, root, "clickhouse", "5555") // no run recorded on it
	stateDir(t, root, "trivy", "db")

	states, err := List(root, func(run Run) bool { return run.Workspace == "/work/shop" })
	require.NoError(t, err)
	byDir := map[string]State{}
	for _, state := range states {
		byDir[state.Dir] = state
	}
	require.Len(t, byDir, 3, "the recorded directories of the workspace, and nothing else: %v", states)

	require.Equal(t, persistent, byDir[stopped].Run)
	require.False(t, byDir[stopped].InUse, "a stopped run's directory is not in use")
	require.True(t, byDir[stopped].LastClaimed.Equal(old), "last claimed %s, want %s", byDir[stopped].LastClaimed, old)
	require.True(t, byDir[running].InUse, "a directory an agent still claims is in use")
	require.WithinDuration(t, time.Now(), byDir[running].LastClaimed, time.Minute)
	require.True(t, byDir[thrown].Run.Disposable)

	for _, state := range states {
		require.DirExists(t, state.Dir, "List removes nothing")
	}
	all, err := List(root, nil)
	require.NoError(t, err)
	require.Len(t, all, 4, "no filter means every recorded directory")
}

// TestDiscardRemovesOnlyAPersistentDirectoryNothingUses: Discard is for the
// directory a person named. It is not a second Collect: what is still in use,
// what no run is recorded on, and what is not a state directory are refused.
func TestDiscardRemovesOnlyAPersistentDirectoryNothingUses(t *testing.T) {
	root := t.TempDir()

	running := stateDir(t, root, "postgres", "run", "1111")
	hold, err := Claim(running, persistent)
	require.NoError(t, err)
	require.ErrorIs(t, Discard(running), ErrStateInUse)
	require.DirExists(t, running)

	require.NoError(t, hold.Release())
	require.NoError(t, Discard(running))
	require.NoDirExists(t, running)

	unrecorded := stateDir(t, root, "postgres", "run", "2222")
	require.ErrorIs(t, Discard(unrecorded), ErrStateNotRecorded)
	require.DirExists(t, unrecorded)
	require.ErrorIs(t, Discard(filepath.Join(root, "postgres")), ErrStateNotRecorded)
	require.DirExists(t, filepath.Join(root, "postgres"))

	served := stateDir(t, root, "redis", "3333")
	serving, err := Claim(served, persistent)
	require.NoError(t, err)
	require.NoError(t, serving.Track(os.Getpid()))
	orphan(t, served)
	require.ErrorIs(t, Discard(served), ErrStateInUse, "a server still running in it keeps the directory")
	require.DirExists(t, served)

	stopped := stateDir(t, root, "redis", "4444")
	_, err = Claim(stopped, persistent)
	require.NoError(t, err)
	killHolders(t, stopped)
	require.ErrorIs(t, Discard(stopped), ErrStateInUse, "the run that used it still lives, and restarts its agents")
	orphan(t, stopped)
	require.NoError(t, Discard(stopped))
	require.NoDirExists(t, stopped)

	require.NoError(t, Discard(filepath.Join(root, "redis", "absent")), "a directory already gone is gone")
}

// endRun rewrites every holder of dir as an agent and a run that are both
// gone: what a run that was stopped in order, and never destroyed, leaves.
func endRun(t *testing.T, dir string) {
	t.Helper()
	killHolders(t, dir)
	orphan(t, dir)
}
