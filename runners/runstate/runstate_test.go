package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
)

var (
	disposable = Run{Workspace: "/work/shop", NamingScope: "invabcdefgh", Disposable: true}
	persistent = Run{Workspace: "/work/shop"}
)

// stateDir is a native state directory with data in it, as an agent leaves one.
func stateDir(t *testing.T, root string, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{root}, parts...)...)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "data"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "rows"), []byte("rows"), 0o600))
	return dir
}

// killHolders rewrites every holder of dir as a process that no longer exists:
// what a run that was killed, rather than stopped, leaves behind.
func killHolders(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, markerDir, holdersDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var h holder
		require.NoError(t, json.Unmarshal(data, &h))
		h.Agent.StartID++
		for i := range h.Tracked {
			h.Tracked[i].StartID++
		}
		data, err = json.Marshal(h)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o600))
	}
}

func TestCollectRemovesDisposableStateOnceReleased(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "aaaa")
	hold, err := Claim(dir, disposable)
	require.NoError(t, err)

	removed, err := Collect(dir)
	require.NoError(t, err)
	require.False(t, removed, "a held directory belongs to a live run")
	require.DirExists(t, dir)

	require.NoError(t, hold.Release())
	removed, err = Collect(dir)
	require.NoError(t, err)
	require.True(t, removed)
	require.NoDirExists(t, dir)
}

func TestCollectNeverRemovesPersistentState(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "bbbb")
	hold, err := Claim(dir, persistent)
	require.NoError(t, err)
	require.NoError(t, hold.Release())

	removed, err := Collect(dir)
	require.NoError(t, err)
	require.False(t, removed)
	require.FileExists(t, filepath.Join(dir, "data", "rows"))
}

func TestCollectNeverRemovesUnmarkedState(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "redis", "cccc")
	removed, err := Collect(dir)
	require.NoError(t, err)
	require.False(t, removed, "a directory nobody declared disposable is somebody's data")
	require.FileExists(t, filepath.Join(dir, "data", "rows"))
}

// A scope a developer names can be run again; once any run claims a directory
// as persistent it stays persistent, whatever a later claim says.
func TestPersistentClaimWinsOverDisposable(t *testing.T) {
	for _, order := range [][]Run{{persistent, disposable}, {disposable, persistent}} {
		dir := stateDir(t, t.TempDir(), "postgres", "run", "dddd")
		for _, run := range order {
			hold, err := Claim(dir, run)
			require.NoError(t, err)
			require.NoError(t, hold.Release())
		}
		removed, err := Collect(dir)
		require.NoError(t, err)
		require.False(t, removed)
		require.DirExists(t, dir)
	}
}

func TestCollectRemovesDisposableStateOfAKilledRun(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "eeee")
	_, err := Claim(dir, disposable)
	require.NoError(t, err)
	killHolders(t, dir)

	removed, err := Collect(dir)
	require.NoError(t, err)
	require.True(t, removed)
	require.NoDirExists(t, dir)
}

// A server detached from its agent (a postmaster) outlives it. While it runs
// the directory is in use, whoever else is gone.
func TestTrackedProcessKeepsStateOfADeadAgent(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "ffff")
	hold, err := Claim(dir, disposable)
	require.NoError(t, err)
	killHolders(t, dir)
	require.NoError(t, hold.Track(os.Getpid()))
	// Track rewrote this agent's own (live) record: kill the agent again and
	// keep the tracked process alive.
	data, err := os.ReadFile(hold.path)
	require.NoError(t, err)
	var h holder
	require.NoError(t, json.Unmarshal(data, &h))
	h.Agent.StartID++
	data, err = json.Marshal(h)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(hold.path, data, 0o600))

	removed, err := Collect(dir)
	require.NoError(t, err)
	require.False(t, removed)
	require.DirExists(t, dir)
}

func TestSweepRemovesOnlyDeadDisposableStateInScope(t *testing.T) {
	root := t.TempDir()
	claim := func(dir string, run Run, dead bool) {
		t.Helper()
		_, err := Claim(dir, run)
		require.NoError(t, err)
		if dead {
			killHolders(t, dir)
		}
	}
	other := Run{Workspace: "/work/other", NamingScope: "invzzzzzzzz", Disposable: true}

	deadNested := stateDir(t, root, "postgres", "run", "1111")
	claim(deadNested, disposable, true)
	deadFlat := stateDir(t, root, "redis", "2222")
	claim(deadFlat, disposable, true)
	live := stateDir(t, root, "redis", "3333")
	claim(live, disposable, false)
	kept := stateDir(t, root, "postgres", "run", "4444")
	claim(kept, persistent, true)
	elsewhere := stateDir(t, root, "clickhouse", "5555")
	claim(elsewhere, other, true)
	unmarked := stateDir(t, root, "clickhouse", "6666")
	notState := stateDir(t, root, "trivy", "db")

	preview, err := Sweep(root, SweepOptions{Workspace: "/work/shop", DryRun: true})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{deadNested, deadFlat}, dirs(preview))
	require.DirExists(t, deadNested, "a dry run removes nothing")

	swept, err := Sweep(root, SweepOptions{Workspace: "/work/shop"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{deadNested, deadFlat}, dirs(swept))
	require.Equal(t, disposable, swept[0].Run)
	require.NoDirExists(t, deadNested)
	require.NoDirExists(t, deadFlat)
	for _, dir := range []string{live, kept, elsewhere, unmarked, notState} {
		require.DirExists(t, dir)
	}

	swept, err = Sweep(root, SweepOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{elsewhere}, dirs(swept), "no workspace means every workspace")
	require.DirExists(t, live)
	require.DirExists(t, kept)
}

func TestSweepOfAMissingRootIsEmpty(t *testing.T) {
	swept, err := Sweep(filepath.Join(t.TempDir(), "absent"), SweepOptions{})
	require.NoError(t, err)
	require.Empty(t, swept)
}

func dirs(swept []Swept) []string {
	out := make([]string, 0, len(swept))
	for _, s := range swept {
		out = append(out, s.Dir)
	}
	return out
}

// The plain run of a workspace has no scope: its directory is the developer's
// database, which no flag makes disposable.
func TestRunOfAnEnvironmentIsDisposableOnlyUnderAScope(t *testing.T) {
	require.Equal(t, disposable, For("/work/shop", &basev0.Environment{NamingScope: "invabcdefgh", Disposable: true}))
	require.Equal(t, Run{Workspace: "/work/shop", NamingScope: "mine"}, For("/work/shop", &basev0.Environment{NamingScope: "mine"}))
	require.Equal(t, persistent, For("/work/shop", &basev0.Environment{Disposable: true}))
	require.Equal(t, persistent, For("/work/shop", nil))
}
