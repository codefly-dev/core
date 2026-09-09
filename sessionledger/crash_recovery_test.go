package sessionledger_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/sessionledger"
)

// The crash tests run a real second process that behaves like a CLI: it takes a
// session, spawns a real tracked process group, records it, and then never gets
// to clean up because it is SIGKILLed. Recovery then runs in this process, over
// the same on-disk ledger, against the real orphaned process group.

const (
	helperEnv       = "CODEFLY_SESSION_LEDGER_HELPER"
	helperLedgerEnv = "CODEFLY_SESSION_LEDGER_ROOT"

	// phaseInit crashes with the process group recorded but nothing else done —
	// the CLI died between spawning a dependency and it becoming ready.
	phaseInit = "init"
	// phaseReady crashes after the session reached readiness and recorded a
	// second resource.
	phaseReady = "ready"
)

type helperHandoff struct {
	Invocation string `json:"invocation"`
	PGIDs      []int  `json:"pgids"`
}

// TestMain lets the test binary re-execute itself as the crashing CLI.
func TestMain(m *testing.M) {
	if phase := os.Getenv(helperEnv); phase != "" {
		runCrashingSession(phase)
		return
	}
	os.Exit(m.Run())
}

func runCrashingSession(phase string) {
	store, err := sessionledger.Open(os.Getenv(helperLedgerEnv))
	if err != nil {
		panic(err)
	}
	owner, err := sessionledger.ProcessWitness(os.Getpid())
	if err != nil {
		panic(err)
	}
	handle, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.crash",
		Mode:        sessionledger.ModeDisposable,
		Fingerprint: testFingerprint,
		Owner:       owner,
	}, nil)
	if err != nil {
		panic(err)
	}

	group, err := base.StartTrackedProcessGroup(exec.Command("sleep", "600"))
	if err != nil {
		panic(err)
	}
	// A process group has no identifier before it exists, so it is adopted
	// with its witness in one write rather than declared and later committed.
	resource, witness := sessionledger.NativeProcessGroup(group)
	if _, err := handle.Adopt(resource, witness); err != nil {
		panic(err)
	}
	pgids := []int{group.PGID()}
	if phase == phaseReady {
		second, err := base.StartTrackedProcessGroup(exec.Command("sleep", "600"))
		if err != nil {
			panic(err)
		}
		secondResource, secondWitness := sessionledger.NativeProcessGroup(second)
		if _, err := handle.Adopt(secondResource, secondWitness); err != nil {
			panic(err)
		}
		pgids = append(pgids, second.PGID())
	}

	handoff, err := json.Marshal(helperHandoff{Invocation: handle.InvocationID(), PGIDs: pgids})
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(append(handoff, '\n')); err != nil {
		panic(err)
	}

	// Wait to be killed. Nothing after this line ever runs, which is the point:
	// no deferred Release, no cleanup, exactly like a SIGKILLed CLI.
	signal.Ignore(syscall.SIGTERM)
	select {}
}

func startCrashingSession(t *testing.T, phase, home, ledgerRoot string) helperHandoff {
	t.Helper()
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(),
		helperEnv+"="+phase,
		helperLedgerEnv+"="+ledgerRoot,
		"HOME="+home,
	)
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err, "crashing session did not report its resources")

	var handoff helperHandoff
	require.NoError(t, json.Unmarshal(line, &handoff))

	require.NoError(t, command.Process.Signal(syscall.SIGKILL))
	_ = command.Wait()
	return handoff
}

func processGroupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil
}

func requireGroupGone(t *testing.T, pgid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processGroupAlive(pgid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process group %d survived recovery", pgid)
}

func recoverAfterCrash(t *testing.T, phase string) (helperHandoff, *sessionledger.RecoveryReport) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ledgerRoot := t.TempDir()

	handoff := startCrashingSession(t, phase, home, ledgerRoot)
	for _, pgid := range handoff.PGIDs {
		require.True(t, processGroupAlive(pgid),
			"the killed session must have left its process group behind")
		t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	}

	store, err := sessionledger.Open(ledgerRoot)
	require.NoError(t, err)
	report, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{sessionledger.BackendNative: sessionledger.NativeBackend{}},
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	return handoff, report
}

func TestRecoveryStopsAProcessGroupOrphanedDuringInit(t *testing.T) {
	handoff, report := recoverAfterCrash(t, phaseInit)

	require.Len(t, report.Recovered, 1)
	require.Equal(t, handoff.Invocation, report.Recovered[0].InvocationID)
	require.Equal(t, sessionledger.LifecycleStop, report.Recovered[0].Lifecycle)
	require.Len(t, report.Recovered[0].Resources, 1)
	require.Equal(t, sessionledger.ActionStop, report.Recovered[0].Resources[0].Action)
	require.Equal(t, sessionledger.Succeeded, report.Recovered[0].Resources[0].Outcome.Result)
	require.Equal(t, strconv.Itoa(handoff.PGIDs[0]), report.Recovered[0].Resources[0].Ref.ID)

	requireGroupGone(t, handoff.PGIDs[0])
}

func TestRecoveryStopsAProcessGroupOrphanedAfterReady(t *testing.T) {
	handoff, report := recoverAfterCrash(t, phaseReady)

	require.Len(t, report.Recovered, 1)
	require.Len(t, handoff.PGIDs, 2)
	require.Len(t, report.Recovered[0].Resources, 2)
	for _, resource := range report.Recovered[0].Resources {
		require.Equal(t, sessionledger.Succeeded, resource.Outcome.Result)
	}
	for _, pgid := range handoff.PGIDs {
		requireGroupGone(t, pgid)
	}
}

func TestRecoveryLeavesAProcessGroupWhoseIdentityChanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ledgerRoot := t.TempDir()

	handoff := startCrashingSession(t, phaseInit, home, ledgerRoot)
	t.Cleanup(func() { _ = syscall.Kill(-handoff.PGIDs[0], syscall.SIGKILL) })

	// Rewrite the recorded witness so it names a different incarnation of the
	// same pgid — what a run whose process-group number was recycled looks
	// like. Nothing may be signalled on that evidence.
	store, err := sessionledger.Open(ledgerRoot)
	require.NoError(t, err)
	session, err := store.Read(handoff.Invocation)
	require.NoError(t, err)
	require.Len(t, session.Resources, 1)
	require.NotZero(t, session.Resources[0].Witness.StartID)
	session.Resources[0].Witness.StartID++
	writeSession(t, store, session)

	report, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{sessionledger.BackendNative: sessionledger.NativeBackend{}},
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Recovered, 1)
	require.Equal(t, sessionledger.Preserved, report.Recovered[0].Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonNotOwned, report.Recovered[0].Resources[0].Outcome.Reason)

	require.True(t, processGroupAlive(handoff.PGIDs[0]),
		"a mismatched ownership marker must never be signalled")
}

// writeSession rewrites a record in place, standing in for the state a run
// whose process-group number was recycled would leave behind.
func writeSession(t *testing.T, store *sessionledger.Store, session *sessionledger.Session) {
	t.Helper()
	content, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(store.Root(), "sessions", session.InvocationID+".json")
	require.NoError(t, os.WriteFile(path, append(content, '\n'), 0o600))
}

// A ledger record and the process-group registry are garbage-collected
// independently, so a stale ledger entry can outlive the registration that
// authenticates it and name a pgid another run has since been given. Without a
// witness there is nothing to tell the two apart, so the record must be refused
// rather than signalled — otherwise recovery terminates a live, unrelated run.
func TestRecoveryRefusesANativeRecordWithoutAWitness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := sessionledger.Open(t.TempDir())
	require.NoError(t, err)

	victim, err := base.StartTrackedProcessGroup(exec.Command("sleep", "600"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(-victim.PGID(), syscall.SIGKILL) })

	owner, err := sessionledger.ProcessWitness(os.Getpid())
	require.NoError(t, err)
	handle, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.stale",
		Mode:        sessionledger.ModeDisposable,
		Fingerprint: testFingerprint,
		Owner:       owner,
	}, nil)
	require.NoError(t, err)

	// The record names the victim's pgid and proves nothing about it — the
	// shape a Declare that never reached Commit leaves behind.
	stale, _ := sessionledger.NativeProcessGroup(victim)
	_, err = handle.Declare(stale)
	require.NoError(t, err)
	invocation := handle.InvocationID()
	killHolder(t, store, invocation)

	report, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{sessionledger.BackendNative: sessionledger.NativeBackend{}},
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Recovered, 1)
	require.Equal(t, sessionledger.Preserved, report.Recovered[0].Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonNotOwned, report.Recovered[0].Resources[0].Outcome.Reason)

	require.True(t, processGroupAlive(victim.PGID()),
		"a process group the ledger cannot prove it owns must never be signalled")
}

// Adopt closes the window the refusal above creates: a process group is
// recorded together with the witness that identifies it, in one write.
func TestAdoptRecordsAProcessGroupWithItsWitness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := sessionledger.Open(t.TempDir())
	require.NoError(t, err)

	group, err := base.StartTrackedProcessGroup(exec.Command("sleep", "600"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(-group.PGID(), syscall.SIGKILL) })

	owner, err := sessionledger.ProcessWitness(os.Getpid())
	require.NoError(t, err)
	handle, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.adopt",
		Mode:        sessionledger.ModeDisposable,
		Fingerprint: testFingerprint,
		Owner:       owner,
	}, nil)
	require.NoError(t, err)

	resource, witness := sessionledger.NativeProcessGroup(group)
	ref, err := handle.Adopt(resource, witness)
	require.NoError(t, err)

	session, err := store.Read(handle.InvocationID())
	require.NoError(t, err)
	recorded, ok := session.Resource(ref)
	require.True(t, ok)
	require.Equal(t, sessionledger.Running, recorded.Disposition)
	require.False(t, recorded.Witness.Zero(), "Adopt must leave no unproven window")
	require.True(t, recorded.VanishesOnStop, "a stopped process group no longer exists")

	observation, err := sessionledger.NativeBackend{}.Claim(context.Background(), recorded, "")
	require.NoError(t, err)
	require.True(t, observation.Live)
}
