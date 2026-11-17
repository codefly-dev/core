package runstate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// serverEnv makes this test binary stand in for a native server: a process
// that runs until it is stopped. "stubborn" ignores the request to stop.
const serverEnv = "RUNSTATE_TEST_SERVER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(serverEnv); mode != "" {
		if mode == "stubborn" {
			signal.Ignore(syscall.SIGTERM)
		}
		// Bounded, so that a failed test cannot leave it behind for good.
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// server is a child process of this test standing in for a native server.
type server struct {
	pid  int
	done chan struct{}
}

func startServer(t *testing.T, mode string) *server {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), serverEnv+"="+mode)
	require.NoError(t, cmd.Start())
	s := &server{pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-s.done
	})
	if mode == "stubborn" {
		// Let it install its signal disposition before anything signals it.
		time.Sleep(300 * time.Millisecond)
	}
	return s
}

func (s *server) running() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *server) requireStopped(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("server %d still runs", s.pid)
	}
}

// rewriteHolders applies change to every holder record of dir.
func rewriteHolders(t *testing.T, dir string, change func(*holder)) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, markerDir, holdersDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var h holder
		require.NoError(t, json.Unmarshal(data, &h))
		change(&h)
		data, err = json.Marshal(h)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o600))
	}
}

// abandon rewrites every holder of dir as an agent that no longer exists and
// leaves what it tracked alone: a killed run whose server outlived it.
func abandon(t *testing.T, dir string) {
	t.Helper()
	rewriteHolders(t, dir, func(h *holder) {
		h.Agent.StartID++
		if h.Run != nil {
			h.Run.StartID++
		}
	})
}

// claimWithServer is a state directory claimed for run by an agent that
// started a server in it.
func claimWithServer(t *testing.T, run Run, mode string, parts ...string) (string, *server) {
	t.Helper()
	return claimWithServerUnder(t, t.TempDir(), run, mode, parts...)
}

func claimWithServerUnder(t *testing.T, root string, run Run, mode string, parts ...string) (string, *server) {
	t.Helper()
	dir := stateDir(t, root, parts...)
	hold, err := Claim(dir, run)
	require.NoError(t, err)
	s := startServer(t, mode)
	require.NoError(t, hold.Track(s.pid))
	return dir, s
}

var quick = StopOptions{Grace: 5 * time.Second}

// A killed run's agent is gone and its server is not: nothing would ever stop
// it, and the directory would be kept for good.
func TestStopAbandonedStopsTheServerOfAKilledDisposableRun(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a001")
	abandon(t, dir)

	removed, err := Collect(dir)
	require.NoError(t, err)
	require.False(t, removed, "collecting alone never stops a server")
	require.True(t, s.running())

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)

	removed, err = Collect(dir)
	require.NoError(t, err)
	require.True(t, removed)
	require.NoDirExists(t, dir)
}

// `kill -9` of the CLI leaves the agent running too, reparented: it is
// orphaned, holds nothing, and its server is abandoned all the same.
func TestStopAbandonedStopsTheServerOfAnOrphanedAgent(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a002")
	orphan(t, dir)

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)
}

func TestStopAbandonedKillsAServerThatIgnoresTheRequest(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "stubborn", "redis", "a003")
	abandon(t, dir)

	pids, err := StopAbandoned(context.Background(), dir, StopOptions{Grace: 300 * time.Millisecond})
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)
}

// A cancelled caller stops waiting; it does not get to kill.
func TestStopAbandonedDoesNotKillOnceCancelled(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "stubborn", "redis", "a004")
	abandon(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	pids, err := StopAbandoned(ctx, dir, StopOptions{Grace: time.Minute})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, pids)
	require.True(t, s.running())
}

// An agent that crashed inside a run that goes on is restarted by it: the
// server it left is that run's, not an abandoned one.
func TestStopAbandonedNeverStopsTheServerOfARunThatStillLives(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a011")
	rewriteHolders(t, dir, func(h *holder) {
		require.NotNil(t, h.Run, "a claim records the run that launched the agent")
		h.Agent.StartID++
	})

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	require.True(t, s.running())

	swept, err := Sweep(filepath.Dir(dir), SweepOptions{StopAbandoned: &quick})
	require.NoError(t, err)
	require.Empty(t, swept)
	require.True(t, s.running())
}

// An agent whose launcher was already gone when it claimed (it was reparented
// to PID 1 before its Init) records no run. No run can be supervising it, so
// once it is dead its server is abandoned like any killed run's.
func TestStopAbandonedStopsTheServerOfADeadAgentThatRecordedNoRun(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a012")
	rewriteHolders(t, dir, func(h *holder) {
		h.Run = nil
		h.Agent.StartID++
	})

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Equal(t, []int{s.pid}, pids)
	s.requireStopped(t)

	removed, err := Collect(dir)
	require.NoError(t, err)
	require.True(t, removed)
}

// While that agent lives it claims the directory, run or no run.
func TestStopAbandonedNeverStopsTheServerOfALiveAgentThatRecordedNoRun(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a013")
	rewriteHolders(t, dir, func(h *holder) { h.Run = nil })

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)

	swept, err := Sweep(filepath.Dir(dir), SweepOptions{StopAbandoned: &quick})
	require.NoError(t, err)
	require.Empty(t, swept)
	require.True(t, s.running())
}

// With no run recorded and the agent dead, the only thing between a sweep and
// a signal is the tracked birth: a recorded PID that now names another process
// receives nothing, and the directory, which nothing uses, is collectable.
func TestStopAbandonedNeverSignalsARecycledPIDOfAnAgentThatRecordedNoRun(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a014")
	rewriteHolders(t, dir, func(h *holder) {
		h.Run = nil
		h.Agent.StartID++
		for i := range h.Tracked {
			h.Tracked[i].StartID++
		}
	})

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)

	swept, err := Sweep(filepath.Dir(dir), SweepOptions{StopAbandoned: &quick})
	require.NoError(t, err)
	require.Equal(t, []string{dir}, dirs(swept))
	require.Empty(t, swept[0].Stopped)

	time.Sleep(200 * time.Millisecond)
	require.True(t, s.running(), "a process that was not tracked is never signalled")
}

func TestStopAbandonedNeverTouchesPersistentState(t *testing.T) {
	dir, s := claimWithServer(t, persistent, "plain", "postgres", "run", "a005")
	abandon(t, dir)

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	require.True(t, s.running(), "a developer's database server is theirs to stop")
}

func TestStopAbandonedNeverTouchesStateInUse(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "postgres", "run", "a006")
	// A second, dead agent of the same run tracked the same server.
	second, err := Claim(dir, disposable)
	require.NoError(t, err)
	require.NoError(t, second.Track(s.pid))
	data, err := os.ReadFile(second.path)
	require.NoError(t, err)
	var h holder
	require.NoError(t, json.Unmarshal(data, &h))
	h.Agent.StartID++
	data, err = json.Marshal(h)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(second.path, data, 0o600))

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	require.True(t, s.running(), "one live claim keeps the whole directory")
}

// A record that cannot be read is a holder that cannot be ruled out.
func TestStopAbandonedNeverActsOnAnUnreadableRecord(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "redis", "a007")
	abandon(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, markerDir, holdersDir, "1-torn.json"), []byte("{"), 0o600))

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)
	require.True(t, s.running())
}

func TestStopAbandonedNeverTouchesUnclaimedState(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "redis", "a008")
	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)
}

// The recorded PID answers, but as another birth: the run's server is gone and
// whatever has its number now was never tracked.
func TestStopAbandonedNeverSignalsARecycledPID(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "clickhouse", "a009")
	rewriteHolders(t, dir, func(h *holder) {
		h.Agent.StartID++
		for i := range h.Tracked {
			h.Tracked[i].StartID++
		}
	})

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.NoError(t, err)
	require.Empty(t, pids)

	// And when the number is reused between the reading and the signal.
	recycled, err := markOf(s.pid)
	require.NoError(t, err)
	recycled.StartID++
	pids, err = stop(context.Background(), dir, []mark{recycled}, quick)
	require.NoError(t, err)
	require.Empty(t, pids)

	time.Sleep(200 * time.Millisecond)
	require.True(t, s.running(), "a process that was not tracked is never signalled")
}

func TestStopAbandonedNeverSignalsItsOwnProcess(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "redis", "a010")
	hold, err := Claim(dir, disposable)
	require.NoError(t, err)
	require.NoError(t, hold.Track(os.Getpid()))
	abandon(t, dir)

	pids, err := StopAbandoned(context.Background(), dir, quick)
	require.Error(t, err)
	require.Empty(t, pids)
}

// A sweep stops nothing unless its caller asks, and then only in its scope.
func TestSweepStopsAbandonedServersOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	other := Run{Workspace: "/work/other", NamingScope: "invzzzzzzzz", Disposable: true}
	abandoned, s := claimWithServerUnder(t, root, disposable, "plain", "clickhouse", "b001")
	abandon(t, abandoned)
	elsewhere, sElsewhere := claimWithServerUnder(t, root, other, "plain", "clickhouse", "b002")
	abandon(t, elsewhere)
	kept, sKept := claimWithServerUnder(t, root, persistent, "plain", "postgres", "run", "b003")
	abandon(t, kept)
	live, sLive := claimWithServerUnder(t, root, disposable, "plain", "redis", "b004")
	ended := stateDir(t, root, "redis", "b005")
	_, err := Claim(ended, disposable)
	require.NoError(t, err)
	killHolders(t, ended)

	inShop := func(run Run) bool { return run.Workspace == "/work/shop" }

	swept, err := Sweep(root, SweepOptions{Include: inShop, DryRun: true})
	require.NoError(t, err)
	require.Equal(t, []string{ended}, dirs(swept), "a server left running keeps its directory")

	preview, err := Sweep(root, SweepOptions{Include: inShop, DryRun: true, StopAbandoned: &quick})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{abandoned, ended}, dirs(preview))
	for _, p := range preview {
		if p.Dir == abandoned {
			require.Equal(t, []int{s.pid}, p.Stopped, "a dry run names what it would stop")
		}
	}
	require.True(t, s.running(), "a dry run stops nothing")

	swept, err = Sweep(root, SweepOptions{Include: inShop, StopAbandoned: &quick})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{abandoned, ended}, dirs(swept))
	for _, p := range swept {
		if p.Dir == abandoned {
			require.Equal(t, []int{s.pid}, p.Stopped)
		}
	}
	s.requireStopped(t)
	require.NoDirExists(t, abandoned)
	for _, dir := range []string{elsewhere, kept, live} {
		require.DirExists(t, dir)
	}
	for _, left := range []*server{sElsewhere, sKept, sLive} {
		require.True(t, left.running())
	}
}
