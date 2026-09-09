//go:build !skip_infra

package dockerrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/sessionledger"
)

const sessionFingerprint = "3333333333333333333333333333333333333333333333333333333333333333"

func newLedger(t *testing.T) *sessionledger.Store {
	t.Helper()
	store, err := sessionledger.Open(t.TempDir())
	require.NoError(t, err)
	return store
}

func newSession(t *testing.T, store *sessionledger.Store, key string) *sessionledger.Handle {
	t.Helper()
	owner, err := sessionledger.ProcessWitness(os.Getpid())
	require.NoError(t, err)
	handle, err := store.Acquire(context.Background(), sessionledger.AcquireRequest{
		Key:         key,
		Mode:        sessionledger.ModeDisposable,
		Fingerprint: sessionFingerprint,
		Owner:       owner,
	}, nil)
	require.NoError(t, err)
	return handle
}

func newBackend(t *testing.T) *SessionBackend {
	t.Helper()
	ctx := context.Background()
	if !DockerEngineRunning(ctx) {
		t.Fatal("Docker is not running; bring it up or run with -tags skip_infra to exclude")
	}
	backend, err := NewSessionBackend(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

// startLedgeredContainer runs a real container bound to a session and records it
// in the ledger the way a runtime agent would: declare the name before creating
// anything, create it, then commit the identity Docker reports back.
func startLedgeredContainer(
	t *testing.T, handle *sessionledger.Handle, backend *SessionBackend, name string, data bool,
) (sessionledger.Ref, string) {
	t.Helper()
	ctx := context.Background()

	resource := ContainerResource(name, sessionledger.Created, data)
	ref, err := handle.Declare(resource)
	require.NoError(t, err)

	env, err := NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("alpine:latest"), name)
	require.NoError(t, err)
	env.WithInvocation(handle.InvocationID())
	env.WithPause()
	t.Cleanup(func() {
		_ = backend.Delete(context.Background(), resource)
		_ = env.Shutdown(context.Background())
	})
	require.NoError(t, env.Init(ctx))

	witness, err := backend.ContainerWitness(ctx, name)
	require.NoError(t, err)
	require.NoError(t, handle.Commit(ref, witness))
	return ref, resource.ID
}

func containerState(t *testing.T, backend *SessionBackend, id string) string {
	t.Helper()
	inspect, err := backend.client.ContainerInspect(context.Background(), id)
	require.NoError(t, err)
	return inspect.State.Status
}

func containerExists(t *testing.T, backend *SessionBackend, id string) bool {
	t.Helper()
	_, err := backend.client.ContainerInspect(context.Background(), id)
	return err == nil
}

func uniqueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("ledger-%s-%d", t.Name()[:min(len(t.Name()), 12)], time.Now().UnixNano())
}

func TestSessionBackendStopKeepsTheContainerAndItsData(t *testing.T) {
	backend := newBackend(t)
	store := newLedger(t)
	handle := newSession(t, store, "workspace.docker")
	_, id := startLedgeredContainer(t, handle, backend, uniqueName(t), true)

	report, err := handle.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{BackendDocker: backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.Succeeded, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonDataRetained, report.Resources[0].Outcome.Reason)

	require.True(t, containerExists(t, backend, id), "stop must retain the container's data")
	require.Equal(t, "exited", containerState(t, backend, id))
}

func TestSessionBackendResetDeletesOwnedContainers(t *testing.T) {
	backend := newBackend(t)
	store := newLedger(t)
	handle := newSession(t, store, "workspace.docker")
	_, id := startLedgeredContainer(t, handle, backend, uniqueName(t), true)

	report, err := handle.Release(context.Background(), sessionledger.LifecycleReset,
		sessionledger.Backends{BackendDocker: backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.ActionDelete, report.Resources[0].Action)
	require.Equal(t, sessionledger.Succeeded, report.Resources[0].Outcome.Result)
	require.False(t, containerExists(t, backend, id))
}

// A crashed invocation's containers are stopped by the next run's recovery pass;
// a second, still-live session's borrowed reference to the same container must
// not be what takes it down, and its own cleanup must leave it alone.
func TestBorrowedContainerSurvivesAnotherSessionsCleanup(t *testing.T) {
	backend := newBackend(t)
	store := newLedger(t)

	owner := newSession(t, store, "workspace.owner")
	_, id := startLedgeredContainer(t, owner, backend, uniqueName(t), true)

	borrower := newSession(t, store, "workspace.nested")
	borrowed := ContainerResource(id, sessionledger.Borrowed, true)
	// The owner already applied ContainerName; borrowing takes the recorded id.
	borrowed.ID = id
	ref, err := borrower.Declare(borrowed)
	require.NoError(t, err)
	witnessSource, err := backend.client.ContainerInspect(context.Background(), id)
	require.NoError(t, err)
	created, err := time.Parse(time.RFC3339Nano, witnessSource.Created)
	require.NoError(t, err)
	created = created.UTC()
	require.NoError(t, borrower.Commit(ref, sessionledger.Witness{CreatedAt: &created}))

	report, err := borrower.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{BackendDocker: backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Equal(t, sessionledger.Preserved, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonBorrowed, report.Resources[0].Outcome.Reason)
	require.Equal(t, "running", containerState(t, backend, id))

	_, err = owner.Release(context.Background(), sessionledger.LifecycleStop,
		sessionledger.Backends{BackendDocker: backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
}

// A container recreated under the same name by something else carries a
// different invocation label, and must not be claimed.
func TestSessionBackendRefusesAContainerItCannotProveItOwns(t *testing.T) {
	backend := newBackend(t)
	store := newLedger(t)
	handle := newSession(t, store, "workspace.docker")
	_, id := startLedgeredContainer(t, handle, backend, uniqueName(t), false)

	session := handle.Session()
	resource, ok := session.Resource(sessionledger.Ref{Backend: BackendDocker, ID: id})
	require.True(t, ok)

	_, err := backend.Claim(context.Background(), resource, "0123456789abcdef0123456789abcdef")
	require.ErrorIs(t, err, sessionledger.ErrNotOwned)
	require.Equal(t, "running", containerState(t, backend, id))

	observation, err := backend.Claim(context.Background(), resource, handle.InvocationID())
	require.NoError(t, err)
	require.True(t, observation.Live)
}

func TestSessionBackendClaimReportsAMissingContainerAsGone(t *testing.T) {
	backend := newBackend(t)
	_, err := backend.Claim(context.Background(),
		ContainerResource(uniqueName(t), sessionledger.Created, false), "0123456789abcdef0123456789abcdef")
	require.ErrorIs(t, err, sessionledger.ErrNotFound)
}

func TestSessionBackendRetainsVolumesOnStopAndRemovesThemOnReset(t *testing.T) {
	backend := newBackend(t)
	store := newLedger(t)
	ctx := context.Background()
	handle := newSession(t, store, "workspace.volume")

	name := uniqueName(t)
	resource := VolumeResource(name, sessionledger.Created)
	ref, err := handle.Declare(resource)
	require.NoError(t, err)
	created, err := backend.client.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: map[string]string{LabelCodeflyInvocation: handle.InvocationID()},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.client.VolumeRemove(context.Background(), name, true) })
	createdAt, err := time.Parse(time.RFC3339Nano, created.CreatedAt)
	require.NoError(t, err, "engine reported a volume creation time this adapter cannot read")
	createdAt = createdAt.UTC()
	require.NoError(t, handle.Commit(ref, sessionledger.Witness{CreatedAt: &createdAt}))

	report, err := handle.Release(ctx, sessionledger.LifecycleStop,
		sessionledger.Backends{BackendDocker: backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	// A volume never executes, so a stop finds nothing to stop and keeps it.
	require.Equal(t, sessionledger.Preserved, report.Resources[0].Outcome.Result)
	require.Equal(t, sessionledger.ReasonNotRunning, report.Resources[0].Outcome.Reason)
	_, err = backend.client.VolumeInspect(ctx, name)
	require.NoError(t, err, "a stop never removes a volume")

	// A later recovery pass over the same record, now asked to reset.
	session, err := store.Read(report.InvocationID)
	require.NoError(t, err)
	require.Equal(t, sessionledger.Retained, session.Resources[0].Disposition)

	require.NoError(t, backend.Delete(ctx, session.Resources[0]))
	_, err = backend.client.VolumeInspect(ctx, name)
	require.Error(t, err)
}

func TestRecoveryStopsContainersOfAnInvocationThatNeverCameBack(t *testing.T) {
	backend := newBackend(t)
	store := newLedger(t)
	handle := newSession(t, store, "workspace.crashed")
	_, id := startLedgeredContainer(t, handle, backend, uniqueName(t), true)

	// Drop the lease the way a SIGKILL does: the record survives, naming a
	// holder that is no longer running.
	dropLease(t, store, handle.InvocationID())

	report, err := sessionledger.Recover(context.Background(), store,
		sessionledger.Backends{BackendDocker: backend}, sessionledger.ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Recovered, 1)
	require.Equal(t, sessionledger.Succeeded, report.Recovered[0].Resources[0].Outcome.Result)
	require.Equal(t, "exited", containerState(t, backend, id))
	require.True(t, containerExists(t, backend, id), "recovery retains data it did not own the intent to delete")
}

func dropLease(t *testing.T, store *sessionledger.Store, invocation string) {
	t.Helper()
	session, err := store.Read(invocation)
	require.NoError(t, err)
	require.NotNil(t, session.Lease)
	// The recorded holder now names a process that is not the one that ran:
	// same shape as a record left by a SIGKILLed CLI.
	session.Lease.Holder.StartID++
	content, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(store.Root(), "sessions", invocation+".json")
	require.NoError(t, os.WriteFile(path, append(content, '\n'), 0o600))
}
