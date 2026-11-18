package runstate

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func requireOpen(t *testing.T, gone <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-gone:
		t.Fatal(message)
	case <-time.After(3 * ownerPoll):
	}
}

func requireClosed(t *testing.T, gone <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

// A run is its owner's: whoever depends on a process is told when that
// process ends, however it ended.
func TestGoneReportsTheEndOfTheProcess(t *testing.T) {
	s := startServer(t, "plain")
	owner, err := BirthOf(s.pid)
	require.NoError(t, err)
	require.True(t, owner.Alive())

	gone := owner.Gone(context.Background())
	requireOpen(t, gone, "a running owner was reported gone")

	require.NoError(t, syscall.Kill(s.pid, syscall.SIGKILL))
	requireClosed(t, gone, "the owner was killed and nobody was told")
	require.False(t, owner.Alive())
}

// A later process running under the owner's PID is not the owner: the owner is
// gone, and the process that took its number is left alone.
func TestGoneIsNotFooledByALaterProcessWithThePID(t *testing.T) {
	s := startServer(t, "plain")
	owner, err := BirthOf(s.pid)
	require.NoError(t, err)
	owner.StartID++

	requireClosed(t, owner.Gone(context.Background()), "another process under the owner's PID passed for the owner")
	require.True(t, s.running(), "the process that reused the PID was touched")
}

// Nobody is told about an owner once they stopped asking.
func TestGoneStopsWatchingWithItsContext(t *testing.T) {
	s := startServer(t, "plain")
	owner, err := BirthOf(s.pid)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	gone := owner.Gone(ctx)
	cancel()
	require.NoError(t, syscall.Kill(s.pid, syscall.SIGKILL))
	s.requireStopped(t)
	requireOpen(t, gone, "a watch that was cancelled reported the owner gone")
}

// The process that starts a run names itself in the run's environment, and the
// run reads exactly that birth back.
func TestTheOwnerTravelsInTheEnvironment(t *testing.T) {
	owner, err := BirthOf(os.Getpid())
	require.NoError(t, err)

	inherited := []string{"PATH=/bin", OwnerEnvironment + "=4242:2:stale"}
	environment := OwnedBy(inherited, owner)
	require.Contains(t, environment, "PATH=/bin")
	read, owned, err := Owner(environment)
	require.NoError(t, err)
	require.True(t, owned)
	require.Equal(t, owner, read, "an inherited declaration must not survive a new one")

	_, owned, err = Owner(Unowned(inherited))
	require.NoError(t, err)
	require.False(t, owned, "a run nobody declared an owner for has none")

	// A declaration that cannot be read is not "no owner": the run would
	// outlive the process that said it owns it.
	for _, value := range []string{"", "12", "12:34", "x:34:boot", "12:x:boot", "0:34:boot", "12:34:"} {
		_, _, err = Owner([]string{OwnerEnvironment + "=" + value})
		require.Error(t, err, "owner %q", value)
	}
}

// An agent whose run was killed stops what it started for that run and takes
// the run's disposable state with it: nothing else ever would.
func TestAbandonClaimedStopsAndRemovesWhatAKilledDisposableRunLeft(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "redis", "c001")
	orphan(t, dir)

	removed, err := AbandonClaimed(context.Background(), quick)
	require.NoError(t, err)
	require.Equal(t, []string{dir}, removed)
	s.requireStopped(t)
	require.NoDirExists(t, dir)
}

// A developer's database is not the agent's to stop for good or to remove,
// whoever is gone.
func TestAbandonClaimedNeverTouchesPersistentState(t *testing.T) {
	dir, s := claimWithServer(t, persistent, "plain", "redis", "c002")
	orphan(t, dir)

	removed, err := AbandonClaimed(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.True(t, s.running())
	require.DirExists(t, dir)
}

// While the run lives its state is in use, whatever the agent believes.
func TestAbandonClaimedNeverTouchesTheStateOfARunThatStillLives(t *testing.T) {
	dir, s := claimWithServer(t, disposable, "plain", "redis", "c003")

	removed, err := AbandonClaimed(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.True(t, s.running())
	require.DirExists(t, dir)
}

// A claim the agent withdrew is no longer its to abandon: the directory is
// left to whoever collects it.
func TestAbandonClaimedForgetsAReleasedClaim(t *testing.T) {
	dir := stateDir(t, t.TempDir(), "redis", "c004")
	hold, err := Claim(dir, disposable)
	require.NoError(t, err)
	require.NoError(t, hold.Release())

	removed, err := AbandonClaimed(context.Background(), quick)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.DirExists(t, dir)
}
