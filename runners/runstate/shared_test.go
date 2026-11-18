package runstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func trackedPIDs(t *testing.T, dir string) []int {
	t.Helper()
	servers, err := Tracked(dir)
	require.NoError(t, err)
	var pids []int
	for _, server := range servers {
		pids = append(pids, server.PID)
	}
	return pids
}

// The agents of a run that share one server find it in the directory's
// records, whichever of them started it and whether or not that one is left.
func TestTrackedNamesTheServersStillRunning(t *testing.T) {
	dir, s := claimWithServer(t, persistent, "plain", "postgres", "run", "c001")
	require.Equal(t, []int{s.pid}, trackedPIDs(t, dir))

	abandon(t, dir)
	require.Equal(t, []int{s.pid}, trackedPIDs(t, dir), "a server outlives the agent that tracked it")

	rewriteHolders(t, dir, func(h *holder) {
		for i := range h.Tracked {
			h.Tracked[i].StartID++
		}
	})
	require.Empty(t, trackedPIDs(t, dir), "a recorded PID that names another process is no server of this directory")

	require.Empty(t, trackedPIDs(t, stateDir(t, t.TempDir(), "postgres", "run", "c002")))
}

// The last agent to stop using a shared server stops it, in a developer's
// directory as in a disposable one, and keeps its claim on the directory.
func TestStopTrackedStopsTheServerNoOtherClaimUses(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "c003")
	hold, err := Claim(dir, persistent)
	require.NoError(t, err)
	s := startServer(t, "plain")
	require.NoError(t, hold.Track(s.pid))

	pids, err := hold.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)
	require.Empty(t, trackedPIDs(t, dir))

	state, err := inspect(dir)
	require.NoError(t, err)
	require.True(t, state.held, "the agent still claims the directory: it can start again")

	pids, err = hold.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, pids, "it tracks nothing any more")
}

func TestStopTrackedLeavesTheServerAnotherLiveClaimUses(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "c004")
	first, err := Claim(dir, disposable)
	require.NoError(t, err)
	second, err := Claim(dir, disposable)
	require.NoError(t, err)
	s := startServer(t, "plain")
	require.NoError(t, first.Track(s.pid))
	require.NoError(t, second.Track(s.pid))

	pids, err := first.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	time.Sleep(200 * time.Millisecond)
	require.True(t, s.running(), "the other agent of the run still serves from it")
	require.Equal(t, []int{s.pid}, trackedPIDs(t, dir))

	pids, err = second.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)
}

// An agent that died without stopping uses nothing: its record does not keep
// the server up for the agents that are left.
func TestStopTrackedStopsTheServerOnlyADeadClaimStillTracks(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "c005")
	dead, err := Claim(dir, disposable)
	require.NoError(t, err)
	s := startServer(t, "plain")
	require.NoError(t, dead.Track(s.pid))
	abandon(t, dir)
	live, err := Claim(dir, disposable)
	require.NoError(t, err)
	require.NoError(t, live.Track(s.pid))

	pids, err := live.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)
}

// A record that cannot be read is a claim being written, or one that cannot be
// ruled out: it may use the server.
func TestStopTrackedLeavesAServerAnUnreadableRecordMayUse(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "c006")
	hold, err := Claim(dir, disposable)
	require.NoError(t, err)
	s := startServer(t, "plain")
	require.NoError(t, hold.Track(s.pid))
	require.NoError(t, os.WriteFile(filepath.Join(dir, markerDir, holdersDir, "1-torn.json"), []byte("{"), 0o600))

	pids, err := hold.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	time.Sleep(200 * time.Millisecond)
	require.True(t, s.running())
}

// The tracked PID answers, but as another birth: the server is gone and what
// has its number now was never tracked.
func TestStopTrackedNeverSignalsARecycledPID(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "postgres", "run", "c007")
	hold, err := Claim(dir, disposable)
	require.NoError(t, err)
	s := startServer(t, "plain")
	require.NoError(t, hold.Track(s.pid))
	hold.holder.Tracked[0].StartID++

	pids, err := hold.StopTracked(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	time.Sleep(200 * time.Millisecond)
	require.True(t, s.running(), "a process that was not tracked is never signalled")
}
