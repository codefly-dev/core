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

// vanishing models a resource that ceases to exist when stopped — a process
// group, as opposed to a container, which the directory backend models.
func vanishing(id string) sessionledger.Resource {
	resource := directory(id, sessionledger.Created, false)
	resource.VanishesOnStop = true
	return resource
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

	ref, err := handle.Declare(vanishing("worker"))
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

	removed, err := store.Expire(context.Background())
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

func TestAbsentProcessIsNotLive(t *testing.T) {
	absent := selfWitness(t)
	absent.PID = 0x7FFFFFF0
	require.False(t, sessionledger.LiveProcess(absent))
}

func TestRecoveryLeavesASessionWhoseHolderCannotBeInspected(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.opaque", sessionledger.ModeDisposable)
	t.Cleanup(func() {
		_, _ = handle.Release(context.Background(), sessionledger.LifecycleStop,
			sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	})

	ref, err := handle.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))

	// A probe that cannot tell — the shape of an EPERM inspecting another
	// user's process — must not licence recovery to touch the session.
	unknown := func(sessionledger.Witness) bool { return true }
	report, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{"test": backend},
		sessionledger.ReconcileOptions{Alive: unknown})
	require.NoError(t, err)
	require.Empty(t, report.Recovered)
	require.Equal(t, []string{handle.InvocationID()}, report.Skipped)

	observation, err := backend.Claim(context.Background(),
		directory("database", sessionledger.Created, true), handle.InvocationID())
	require.NoError(t, err)
	require.True(t, observation.Live, "an uninspectable holder's resources keep running")
}

// Release reconciles by index over the same slice Declare appends to. Holding
// the handle for the lookup only let a concurrent Declare reallocate that slice
// mid-reconcile, dropping the declared resource from the record — a resource
// nothing would ever clean up.
func TestReleaseAndDeclareAreSafeConcurrently(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.concurrent", sessionledger.ModeDisposable)

	for _, id := range []string{"one", "two", "three", "four"} {
		ref, err := handle.Declare(directory(id, sessionledger.Created, true))
		require.NoError(t, err)
		backend.create(t, id, handle.InvocationID())
		require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	}
	invocation := handle.InvocationID()

	var wg sync.WaitGroup
	wg.Add(2)
	var declareErr error
	go func() {
		defer wg.Done()
		_, _ = handle.Release(context.Background(), sessionledger.LifecycleStop,
			sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	}()
	go func() {
		defer wg.Done()
		_, declareErr = handle.Declare(directory("late", sessionledger.Created, true))
	}()
	wg.Wait()

	session, err := store.Read(invocation)
	require.NoError(t, err)
	recorded := map[string]bool{}
	for _, resource := range session.Resources {
		recorded[resource.ID] = true
	}
	for _, id := range []string{"one", "two", "three", "four"} {
		require.True(t, recorded[id], "reconcile dropped %s from the record", id)
	}
	// The late declare either won the handle and is recorded, or lost it and
	// was refused. It must never be silently absent from a record it joined.
	if declareErr == nil {
		require.True(t, recorded["late"], "a declare that succeeded must be in the record")
	}
}

// A stopped resource that still exists must keep its record. Whether stopping
// destroys something is a fact about the backend, not about the disposition: a
// process group is gone, a container is still there holding disk.
func TestAStoppedResourceThatStillExistsKeepsItsRecord(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.survives", sessionledger.ModeDisposable)

	ref, err := handle.Declare(directory("container", sessionledger.Created, false))
	require.NoError(t, err)
	backend.create(t, "container", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	invocation := handle.InvocationID()

	_, err = handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	require.True(t, backend.exists("container"), "a stop leaves the object behind")
	session, err := store.Read(invocation)
	require.NoError(t, err, "the record must still name the object that is still there")
	require.Equal(t, sessionledger.Stopped, session.Resources[0].Disposition)

	removed, err := store.WithRetention(0).Expire(context.Background())
	require.NoError(t, err)
	require.Zero(t, removed, "retention must not forget state that still exists")
}

func TestAStoppedProcessGroupIsForgottenBecauseItIsGone(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	handle := acquire(t, store, "workspace.vanishes", sessionledger.ModeDisposable)

	ref, err := handle.Declare(vanishing("worker"))
	require.NoError(t, err)
	backend.create(t, "worker", handle.InvocationID())
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	invocation := handle.InvocationID()

	_, err = handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{"test": backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	_, err = store.Read(invocation)
	require.ErrorIs(t, err, sessionledger.ErrSessionNotFound)
}

// IncompatibleReuseError tells the caller to reset the warm state. Reset is
// what makes that instruction followable — without it a changed fixture wedges
// the key permanently, because the only handle to a session is the Acquire
// that just refused.
func TestResetClearsWarmStateThatCannotBeAcquired(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	backends := sessionledger.Backends{"test": backend}
	changed := "2222222222222222222222222222222222222222222222222222222222222222"

	first := acquire(t, store, "workspace.wedged", sessionledger.ModeReusable)
	ref, err := first.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", first.InvocationID())
	require.NoError(t, first.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	_, err = first.Release(context.Background(), sessionledger.LifecycleKeepRunning, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	request := sessionledger.AcquireRequest{
		Key:         "workspace.wedged",
		Mode:        sessionledger.ModeReusable,
		Fingerprint: changed,
		Owner:       selfWitness(t),
	}
	var incompatible *sessionledger.IncompatibleReuseError
	_, err = store.Acquire(context.Background(), request, nil)
	require.ErrorAs(t, err, &incompatible)

	report, err := store.Reset(context.Background(), "workspace.wedged", backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.ActionDelete, report.Resources[0].Action)
	require.False(t, backend.exists("database"), "reset clears the state it owns")

	fresh, err := store.Acquire(context.Background(), request, nil)
	require.NoError(t, err, "the key must be usable again after a reset")
	require.False(t, fresh.Reattached())
	_, err = fresh.Release(context.Background(), sessionledger.LifecycleStop, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
}

func TestResetRefusesBorrowedDataAndALiveHolder(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	backends := sessionledger.Backends{"test": backend}

	held := acquire(t, store, "workspace.held", sessionledger.ModeReusable)
	_, err := store.Reset(context.Background(), "workspace.held", backends,
		sessionledger.ReconcileOptions{})
	require.ErrorIs(t, err, sessionledger.ErrSessionBusy)

	ref, err := held.Declare(directory("parent-db", sessionledger.Borrowed, true))
	require.NoError(t, err)
	backend.create(t, "parent-db", "someone-else")
	require.NoError(t, held.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	_, err = held.Release(context.Background(), sessionledger.LifecycleKeepRunning, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	_, err = store.Reset(context.Background(), "workspace.held", backends,
		sessionledger.ReconcileOptions{})
	require.ErrorIs(t, err, sessionledger.ErrBorrowedDataNotDisposable)
	require.True(t, backend.exists("parent-db"))
}

// Warm state still holding a dead invocation's lease has not been reconciled.
// Adopting it would hand the caller resources that may have died with their
// invocation and skip the recovery a crash is exactly what calls for.
func TestAcquireRefusesWarmStateLeftByACrash(t *testing.T) {
	store := newStore(t)
	backend := dirBackend{root: t.TempDir()}
	backends := sessionledger.Backends{"test": backend}

	crashed := acquire(t, store, "workspace.crashed", sessionledger.ModeReusable)
	ref, err := crashed.Declare(directory("database", sessionledger.Created, true))
	require.NoError(t, err)
	backend.create(t, "database", crashed.InvocationID())
	require.NoError(t, crashed.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	killHolder(t, store, crashed.InvocationID())

	request := sessionledger.AcquireRequest{
		Key:         "workspace.crashed",
		Mode:        sessionledger.ModeReusable,
		Fingerprint: testFingerprint,
		Owner:       selfWitness(t),
	}
	_, err = store.Acquire(context.Background(), request, nil)
	require.ErrorIs(t, err, sessionledger.ErrRecoveryRequired)
	require.True(t, backend.dataIntact("database"), "the refusal must not touch the state")

	_, err = sessionledger.Recover(context.Background(), store, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)

	reattached, err := store.Acquire(context.Background(), request, nil)
	require.NoError(t, err, "recovery must make the warm state acquirable again")
	require.True(t, reattached.Reattached())
	require.True(t, backend.dataIntact("database"))
	_, err = reattached.Release(context.Background(), sessionledger.LifecycleKeepRunning, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
}

// An undecodable record must be reported once and moved aside, not re-reported
// on every invocation forever — which would leave Recover permanently returning
// an error no caller can clear.
func TestAnUndecodableRecordIsQuarantinedOnceAndKept(t *testing.T) {
	store := newStore(t)
	path := filepath.Join(store.Root(), "sessions", "ffffffffffffffffffffffffffffffff.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	_, err := store.List()
	require.Error(t, err)

	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	quarantined, err := os.ReadFile(path + ".invalid")
	require.NoError(t, err, "the bytes must be kept for a human, not deleted")
	require.Equal(t, "{not json", string(quarantined))

	sessions, err := store.List()
	require.NoError(t, err, "a quarantined record must not be reported again")
	require.Empty(t, sessions)
}

// Expire reads a listing and then deletes; another process can take a lease in
// between, and forgetting a record someone is holding orphans what they create.
func TestExpireDoesNotForgetASessionAcquiredMeanwhile(t *testing.T) {
	store := newStore(t).WithRetention(0)
	backend := dirBackend{root: t.TempDir()}
	backends := sessionledger.Backends{"test": backend}

	first := acquire(t, store, "workspace.warm", sessionledger.ModeReusable)
	ref, err := first.Declare(vanishing("worker"))
	require.NoError(t, err)
	backend.create(t, "worker", first.InvocationID())
	require.NoError(t, first.Commit(ref, sessionledger.Witness{Digest: "abcdef"}))
	_, err = first.Release(context.Background(), sessionledger.LifecycleStop, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	invocation := first.InvocationID()

	// Released, forgettable and past retention: Expire's candidate. Acquiring
	// it first must win, because Expire re-reads under the key lock.
	reattached, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         "workspace.warm",
		Mode:        sessionledger.ModeReusable,
		Fingerprint: testFingerprint,
		Owner:       selfWitness(t),
	}, nil)
	require.NoError(t, err)
	require.Equal(t, invocation, reattached.InvocationID())

	removed, err := store.Expire(context.Background())
	require.NoError(t, err)
	require.Zero(t, removed, "a leased session must never be expired")
	_, err = store.Read(invocation)
	require.NoError(t, err)

	_, err = reattached.Release(context.Background(), sessionledger.LifecycleKeepRunning, backends,
		sessionledger.ReconcileOptions{})
	require.NoError(t, err)
}
