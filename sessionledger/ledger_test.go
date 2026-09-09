package sessionledger_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/sessionledger"
)

// dirBackend is a real adapter over real filesystem state: a resource is a
// directory, ownership is proven by an invocation marker written inside it,
// stopping writes a stop marker and keeps the contents, deleting removes it.
// It exercises the ledger contract against state that actually survives — the
// same property a database volume has — without needing a database.
type dirBackend struct{ root string }

const kindDirectory = "test.directory"

func (backend dirBackend) path(resource sessionledger.Resource) string {
	return filepath.Join(backend.root, resource.ID)
}

func (backend dirBackend) create(t *testing.T, id, invocation string) {
	t.Helper()
	dir := filepath.Join(backend.root, id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "invocation"), []byte(invocation), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data"), []byte("precious"), 0o600))
}

func (backend dirBackend) Claim(
	_ context.Context, resource sessionledger.Resource, invocation string,
) (sessionledger.Observation, error) {
	marker, err := os.ReadFile(filepath.Join(backend.path(resource), "invocation"))
	if errors.Is(err, os.ErrNotExist) {
		return sessionledger.Observation{}, sessionledger.ErrNotFound
	}
	if err != nil {
		return sessionledger.Observation{}, err
	}
	if string(marker) != invocation {
		return sessionledger.Observation{}, sessionledger.ErrNotOwned
	}
	_, err = os.Stat(filepath.Join(backend.path(resource), "stopped"))
	return sessionledger.Observation{
		Live:    errors.Is(err, os.ErrNotExist),
		Witness: sessionledger.Witness{Digest: "abcdef"},
	}, nil
}

func (backend dirBackend) Stop(_ context.Context, resource sessionledger.Resource) error {
	return os.WriteFile(filepath.Join(backend.path(resource), "stopped"), []byte("1"), 0o600)
}

func (backend dirBackend) Delete(_ context.Context, resource sessionledger.Resource) error {
	return os.RemoveAll(backend.path(resource))
}

func (backend dirBackend) exists(id string) bool {
	_, err := os.Stat(filepath.Join(backend.root, id))
	return err == nil
}

func (backend dirBackend) dataIntact(id string) bool {
	content, err := os.ReadFile(filepath.Join(backend.root, id, "data"))
	return err == nil && string(content) == "precious"
}

const testFingerprint = "1111111111111111111111111111111111111111111111111111111111111111"

func newStore(t *testing.T) *sessionledger.Store {
	t.Helper()
	store, err := sessionledger.Open(t.TempDir())
	require.NoError(t, err)
	return store
}

func selfWitness(t *testing.T) sessionledger.Witness {
	t.Helper()
	witness, err := sessionledger.ProcessWitness(os.Getpid())
	require.NoError(t, err)
	return witness
}

// killHolder rewrites a record so its lease names a process identity that is
// not the one running, which is how a SIGKILLed invocation's record reads.
func killHolder(t *testing.T, store *sessionledger.Store, invocation string) {
	t.Helper()
	session, err := store.Read(invocation)
	require.NoError(t, err)
	require.NotNil(t, session.Lease)
	session.Lease.Holder.StartID++
	content, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(store.Root(), "sessions", invocation+".json"),
		append(content, '\n'), 0o600))
}

func directory(id string, ownership sessionledger.Ownership, data bool) sessionledger.Resource {
	return sessionledger.Resource{
		Kind:      kindDirectory,
		Backend:   "test",
		ID:        id,
		Ownership: ownership,
		Data:      data,
	}
}

func acquire(t *testing.T, store *sessionledger.Store, key string, mode sessionledger.Mode) *sessionledger.Handle {
	t.Helper()
	handle, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         key,
		Mode:        mode,
		Fingerprint: testFingerprint,
		Owner:       selfWitness(t),
	}, nil)
	require.NoError(t, err)
	return handle
}

func TestStopRetainsDataAndEndsExecution(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	report, err := handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	require.Len(t, report.Resources, 1)
	require.Equal(t, sessionledger.ActionStop, report.Resources[0].Action)
	require.Equal(t, sessionledger.Succeeded, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonDataRetained, report.Resources[0].Outcome.Reason)

	require.True(t, backend.exists("database"), "stop must not remove data")
	require.True(t, backend.dataIntact("database"))
}

func TestResetDeletesOwnedDisposableState(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	report, err := handle.Release(context.Background(), sessionledger.LifecycleReset,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.ActionDelete, report.Resources[0].Action)
	require.False(t, backend.exists("database"))
}

func TestResetDeletesDataThatAnEarlierStopRetained(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	backends := sessionledger.Backends{"test": backend}

	first := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)
	ref, err := first.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", first.InvocationID())
	require.NoError(t, first.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	_, err = first.Release(context.Background(), sessionledger.LifecycleStop, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.True(t, backend.dataIntact("database"))

	again := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)
	require.True(t, again.Reattached())
	report, err := again.Release(context.Background(), sessionledger.LifecycleReset, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.ActionDelete, report.Resources[0].Action)
	require.Equal(t, sessionledger.Succeeded, report.Resources[0].Outcome.Result)
	require.False(t, backend.exists("database"), "an explicit reset clears retained data")
}

func TestResetIsRefusedWholeWhenBorrowedDataIsHeld(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	owned, err := handle.Declare(directory("scratch", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "scratch", handle.InvocationID())
	require.NoError(t, handle.Commit(owned, sessionledger.Witness{Digest: "abcdef"}))

	borrowed, err := handle.Declare(directory("parent-db", sessionledger.Borrowed, true))
	require.NoError(t, err)
	backend.create(t, "parent-db", "someone-else")
	require.NoError(t, handle.Commit(borrowed, sessionledger.Witness{Digest: "abcdef"}))

	_, err = handle.Release(context.Background(), sessionledger.LifecycleReset,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.ErrorIs(t, err, sessionledger.ErrBorrowedDataNotDisposable)

	require.True(t, backend.exists("scratch"), "a refused reset must not partially apply")
	require.True(t, backend.exists("parent-db"))
}

func TestBorrowedResourceSurvivesAnotherInvocationsCleanup(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}

	owner := acquire(t, store, "workspace.owner", sessionledger.ModeReusable)
	ownedRef, err := owner.Declare(directory("shared-db", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "shared-db", owner.InvocationID())
	require.NoError(t, owner.Commit(ownedRef, sessionledger.Witness{Digest: "abcdef"}))

	borrower := acquire(t, store, "workspace.nested", sessionledger.ModeDisposable)
	borrowedRef, err := borrower.Declare(directory("shared-db", sessionledger.Borrowed, true))
	require.NoError(t, err)
	require.NoError(t, borrower.Commit(borrowedRef, sessionledger.Witness{Digest: "abcdef"}))

	report, err := borrower.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.ActionKeep, report.Resources[0].Action)
	require.Equal(t, sessionledger.Preserved, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonBorrowed, report.Resources[0].Outcome.Reason)

	require.True(t, backend.exists("shared-db"))
	require.True(t, backend.dataIntact("shared-db"))
	claimed, err := backend.Claim(context.Background(), directory("shared-db", sessionledger.Created, true),
		owner.InvocationID())
	require.NoError(t, err)
	require.True(t, claimed.Live, "the owner's resource must still be running")
}

func TestUnprovableOwnershipIsPreservedNotDeleted(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	// Someone else's state now occupies the identifier this session recorded.
	backend.create(t, "database", "another-invocation")
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	report, err := handle.Release(context.Background(), sessionledger.LifecycleReset,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.Preserved, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonNotOwned, report.Resources[0].Outcome.Reason)
	require.True(t, backend.exists("database"))
}

func TestMissingAdapterRefusesRatherThanGuesses(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	report, err := handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.Refused, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonNoAdapter, report.Resources[0].Outcome.Reason)
	require.True(t, backend.exists("database"))

	// The record survives so a later run with the adapter can finish the job.
	session, err := store.Read(report.InvocationID)
	require.NoError(t, err)
	resource, ok := session.Resource(ref)
	require.True(t, ok)
	require.Equal(t, sessionledger.Running, resource.Disposition)
}

func TestWarmReuseRequiresAMatchingFingerprint(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}

	first := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)
	ref, err := first.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", first.InvocationID())
	require.NoError(t, first.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	_, err = first.Release(context.Background(), sessionledger.LifecycleKeepRunning,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	same := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)
	require.True(t, same.Reattached())
	require.Equal(t, first.InvocationID(), same.InvocationID())
	require.Len(t, same.Session().Resources, 1)
	_, err = same.Release(context.Background(), sessionledger.LifecycleKeepRunning,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	changed := "2222222222222222222222222222222222222222222222222222222222222222"
	_, err = store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.warm",
		Mode:        sessionledger.ModeReusable,
		Fingerprint: changed,
		Owner:       selfWitness(t),
	}, nil)
	var incompatible *sessionledger.IncompatibleReuseError
	require.ErrorAs(t, err, &incompatible)
	require.Equal(t, testFingerprint, incompatible.Got)
	require.Equal(t, changed, incompatible.Want)

	require.True(t, backend.exists("database"), "rejected reuse must not delete warm state")
	require.True(t, backend.dataIntact("database"))
}

func TestWarmStateHeldByALiveInvocationIsBusy(t *testing.T) {
	store := newStore(t)
	held := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)
	t.Cleanup(func() {
		_, _ = held.Release(context.Background(), sessionledger.LifecycleKeepRunning, nil,
			sessionledger.ReconcileOptions{})
	})

	_, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.warm",
		Mode:        sessionledger.ModeReusable,
		Fingerprint: testFingerprint,
		Owner:       selfWitness(t),
	}, nil)
	require.ErrorIs(t, err, sessionledger.ErrSessionBusy)
}

func TestConcurrentAcquireIsSerialized(t *testing.T) {
	store := newStore(t)
	const attempts = 8

	var wg sync.WaitGroup
	results := make([]error, attempts)
	handles := make([]*sessionledger.Handle, attempts)
	for index := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
				Key:         "workspace.contended",
				Mode:        sessionledger.ModeReusable,
				Fingerprint: testFingerprint,
				Owner:       selfWitness(t),
			}, nil)
			handles[index], results[index] = handle, err
		}()
	}
	wg.Wait()

	granted := 0
	for index, err := range results {
		if err == nil {
			granted++
			_, releaseErr := handles[index].Release(context.Background(),
				sessionledger.LifecycleKeepRunning, nil, sessionledger.ReconcileOptions{})
			require.NoError(t, releaseErr)
			continue
		}
		require.ErrorIs(t, err, sessionledger.ErrSessionBusy)
	}
	require.Equal(t, 1, granted, "exactly one concurrent acquire may hold the warm session")

	sessions, err := store.List()
	require.NoError(t, err)
	require.Len(t, sessions, 1, "contended acquires must not each create a session")
}

func TestReceiptRecordsEveryOwnershipAndDisposition(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.receipt", sessionledger.ModeDisposable)

	process, err := handle.Declare(directory("worker", sessionledger.Created, false))
	require.NoError(t, err)
	backend.create(t, "worker", handle.InvocationID())
	require.NoError(t, handle.Commit(process, sessionledger.Witness{Digest: "abcdef"}))

	data, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(data, sessionledger.Witness{Digest: "abcdef"}))

	borrowed, err := handle.Declare(directory("parent-db", sessionledger.Borrowed, true))
	require.NoError(t, err)
	backend.create(t, "parent-db", "someone-else")
	require.NoError(t, handle.Commit(borrowed, sessionledger.Witness{Digest: "abcdef"}))

	gone, err := handle.Declare(directory("already-gone", sessionledger.Created, false))
	require.NoError(t, err)
	require.NoError(t, handle.Commit(gone, sessionledger.Witness{Digest: "abcdef"}))

	before := handle.Session()
	_, err = handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	// A disposable session is only forgotten once nothing is outstanding; here
	// the retained data keeps the receipt readable.
	session, err := store.Read(before.InvocationID)
	require.NoError(t, err)

	dispositions := map[string]sessionledger.Disposition{}
	for _, resource := range session.Resources {
		dispositions[resource.ID] = resource.Disposition
		require.NotNil(t, resource.Outcome, "every resource carries a receipt")
	}
	require.Equal(t, sessionledger.Stopped, dispositions["worker"])
	require.Equal(t, sessionledger.Retained, dispositions["database"])
	require.Equal(t, sessionledger.Retained, dispositions["parent-db"])
	require.Equal(t, sessionledger.Deleted, dispositions["already-gone"])

	explained := sessionledger.Explain(session)
	require.Contains(t, explained, "ownership=borrowed")
	require.Contains(t, explained, "disposition=retained")
}

func TestRecoveryOfACrashedSessionIsIdempotent(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	invocation := handle.InvocationID()

	// The holder is no longer the process that took the session — what a
	// SIGKILLed run leaves behind.
	killHolder(t, store, invocation)

	first, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, first.Recovered, 1)
	require.Equal(t, sessionledger.Succeeded, first.Recovered[0].Resources[0].Outcome.Result)
	require.True(t, backend.dataIntact("database"), "recovery stops execution and keeps data")

	second, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Empty(t, second.Recovered, "a second pass has nothing left to recover")
	require.Equal(t, []string{invocation}, second.Skipped)
	require.True(t, backend.dataIntact("database"))

	session, err := store.Read(invocation)
	require.NoError(t, err)
	require.Equal(t, sessionledger.Retained, session.Resources[0].Disposition)
}

func TestAResourceDisposedByTheCallerIsNotTouchedAgain(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("worker", sessionledger.Created, false))
	require.NoError(t, err)
	backend.create(t, "worker", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	require.NoError(t, handle.Dispose(ref, sessionledger.Deleted, sessionledger.Outcome{
		Action: sessionledger.ActionDelete, Result: sessionledger.Succeeded,
	}))

	report, err := handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.ReasonAlreadyTerminal, report.Resources[0].Outcome.Reason)
}

func TestLiveInvocationsResourcesAreNeverTouched(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.live", sessionledger.ModeDisposable)
	t.Cleanup(func() {
		_, _ = handle.Release(context.Background(), sessionledger.LifecycleStop,
			sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	})

	ref, err := handle.Declare(directory("worker", sessionledger.Created, false))
	require.NoError(t, err)
	backend.create(t, "worker", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	report, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{handle.InvocationID()}, report.Skipped)
	require.Empty(t, report.Recovered)

	observation, err := backend.Claim(context.Background(), directory("worker", sessionledger.Created, false),
		handle.InvocationID())
	require.NoError(t, err)
	require.True(t, observation.Live)
}

func TestExpiryKeepsRecordsThatStillNameResources(t *testing.T) {
	store := newStore(t).WithRetention(0)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeReusable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	invocation := handle.InvocationID()
	_, err = handle.Release(context.Background(), sessionledger.LifecycleKeepRunning,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	removed, err := store.Expire()
	require.NoError(t, err)
	require.Zero(t, removed)
	_, err = store.Read(invocation)
	require.NoError(t, err)
}

func TestSecretsCannotBeWrittenIntoTheLedger(t *testing.T) {
	const sentinel = "postgres://user:hunter2@localhost:5432/db"
	store := newStore(t)

	_, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         sentinel,
		Mode:        sessionledger.ModeDisposable,
		Fingerprint: testFingerprint,
		Owner:       selfWitness(t),
	}, nil)
	require.ErrorIs(t, err, sessionledger.ErrInvalid)

	_, err = store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.svc",
		Mode:        sessionledger.ModeDisposable,
		Fingerprint: sentinel,
		Owner:       selfWitness(t),
	}, nil)
	require.ErrorIs(t, err, sessionledger.ErrInvalid)

	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)
	_, err = handle.Declare(sessionledger.Resource{
		Kind: kindDirectory, Backend: "test", ID: "db",
		Ownership: sessionledger.Created,
		Witness:   sessionledger.Witness{Digest: sentinel},
	})
	require.ErrorIs(t, err, sessionledger.ErrInvalid)

	_, err = handle.Declare(sessionledger.Resource{
		Kind: kindDirectory, Backend: "test", ID: sentinel, Ownership: sessionledger.Created,
	})
	require.ErrorIs(t, err, sessionledger.ErrInvalid)

	records, err := os.ReadDir(filepath.Join(store.Root(), "sessions"))
	require.NoError(t, err)
	require.NotEmpty(t, records)
	for _, record := range records {
		content, readErr := os.ReadFile(filepath.Join(store.Root(), "sessions", record.Name()))
		require.NoError(t, readErr)
		require.NotContains(t, string(content), "hunter2")
	}
}

func TestRecordedProcessIdentityRejectsAReusedPID(t *testing.T) {
	live := selfWitness(t)
	require.True(t, sessionledger.LiveProcess(live))

	recycled := live
	recycled.StartID = live.StartID + 1
	require.False(t, sessionledger.LiveProcess(recycled),
		"a PID whose start identity differs is a different process")

	renamed := live
	renamed.Executable = "not-the-test-binary"
	require.False(t, sessionledger.LiveProcess(renamed))
}

func TestSchemaIsNamespacedOnDisk(t *testing.T) {
	root := t.TempDir()
	store, err := sessionledger.Open(root)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(store.Root(), filepath.Join("session-ledger", "v1")),
		"records live under their schema, so another contract's records are never parsed")

	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)
	content, err := os.ReadFile(filepath.Join(store.Root(), "sessions", handle.InvocationID()+".json"))
	require.NoError(t, err)
	require.Contains(t, string(content), sessionledger.SchemaV1)
}

func TestDeclareIsDurableBeforeTheResourceExists(t *testing.T) {
	store := newStore(t)
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)

	// Nothing has been created yet; the record already names what will be.
	session, err := store.Read(handle.InvocationID())
	require.NoError(t, err)
	resource, ok := session.Resource(ref)
	require.True(t, ok)
	require.Equal(t, sessionledger.Declared, resource.Disposition)
	require.True(t, resource.Witness.Zero())
}

func TestPerResourceTimeoutBoundsAStuckBackend(t *testing.T) {
	store := newStore(t)
	handle := acquire(t, store, "workspace.svc", sessionledger.ModeDisposable)
	ref, err := handle.Declare(directory("worker", sessionledger.Created, false))
	require.NoError(t, err)
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	report, err := handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": blockingBackend{}},
		sessionledger.ReconcileOptions{PerResourceTimeout: 100 * time.Millisecond})
	require.Error(t, err)
	require.Equal(t, sessionledger.Failed, report.Resources[0].Outcome.Result)
}

// blockingBackend never answers, standing in for an unresponsive engine.
type blockingBackend struct{}

func (blockingBackend) Claim(
	ctx context.Context, _ sessionledger.Resource, _ string,
) (sessionledger.Observation, error) {
	<-ctx.Done()
	return sessionledger.Observation{}, ctx.Err()
}

func (blockingBackend) Stop(context.Context, sessionledger.Resource) error   { return nil }
func (blockingBackend) Delete(context.Context, sessionledger.Resource) error { return nil }
