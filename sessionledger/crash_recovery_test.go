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

	// phaseInit crashes with the process group declared but not yet confirmed
	// — the CLI died between spawning a dependency and it becoming ready.
	phaseInit = "init"
	// phaseReady crashes after the group was confirmed running.
	phaseReady = "ready"
)

type helperHandoff struct {
	Invocation string `json:"invocation"`
	PGID       int    `json:"pgid"`
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
	resource, witness := sessionledger.NativeProcessGroup(group)
	ref, err := handle.Declare(resource)
	if err != nil {
		panic(err)
	}
	if phase == phaseReady {
		if err := handle.Commit(ref, witness); err != nil {
			panic(err)
		}
	}

	handoff, err := json.Marshal(helperHandoff{Invocation: handle.InvocationID(), PGID: group.PGID()})
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
	require.True(t, processGroupAlive(handoff.PGID),
		"the killed session must have left its process group behind")
	t.Cleanup(func() { _ = syscall.Kill(-handoff.PGID, syscall.SIGKILL) })

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
	require.Equal(t, strconv.Itoa(handoff.PGID), report.Recovered[0].Resources[0].Ref.ID)

	requireGroupGone(t, handoff.PGID)
}

func TestRecoveryStopsAProcessGroupOrphanedAfterReady(t *testing.T) {
	handoff, report := recoverAfterCrash(t, phaseReady)

	require.Len(t, report.Recovered, 1)
	require.Equal(t, sessionledger.Succeeded, report.Recovered[0].Resources[0].Outcome.Result)
	requireGroupGone(t, handoff.PGID)
}

func TestRecoveryLeavesAProcessGroupWhoseIdentityChanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ledgerRoot := t.TempDir()

	handoff := startCrashingSession(t, phaseReady, home, ledgerRoot)
	t.Cleanup(func() { _ = syscall.Kill(-handoff.PGID, syscall.SIGKILL) })

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

	require.True(t, processGroupAlive(handoff.PGID),
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
