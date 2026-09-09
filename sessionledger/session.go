package sessionledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrSessionBusy is returned when a reusable session's warm state is held by a
// live process. Warm reuse is exclusive; the caller waits or uses its own key.
var ErrSessionBusy = errors.New("session-ledger warm state is held by a live invocation")

// ErrRecoveryRequired is returned when warm state still holds the lease of an
// invocation that died. Its resources have not been reconciled, so what the
// record names may not exist and may not be running. Run Recover — which knows
// the backends — and acquire again.
var ErrRecoveryRequired = errors.New("session-ledger warm state was left by a crashed invocation")

// IncompatibleReuseError says why warm state could not be reattached to. The
// warm state is left exactly as it was: an incompatible fingerprint is a reason
// to explain the mismatch, never a licence to delete someone's database.
type IncompatibleReuseError struct {
	// Key is the reuse scope that matched.
	Key string
	// Invocation is the warm session that was rejected.
	Invocation string
	// Want is the fingerprint the caller presented.
	Want string
	// Got is the fingerprint the warm session was created with.
	Got string
}

func (err *IncompatibleReuseError) Error() string {
	return fmt.Sprintf(
		"session-ledger warm state %s for key %q was built from plan %s, not %s: "+
			"stop or reset it explicitly to rebuild",
		err.Invocation, err.Key, err.Got, err.Want)
}

// AcquireRequest describes the session a caller wants.
type AcquireRequest struct {
	// Key is the reuse scope. Sessions sharing a key compete for the same warm
	// state, so it must name the logical session — workspace and service — and
	// nothing invocation-specific.
	Key string
	// Mode says whether this session's resources may outlive its process.
	Mode Mode
	// Fingerprint is the semantic plan digest. Warm state is only reattached to
	// when it was built from the same one.
	Fingerprint string
	// Owner identifies the process taking the session. It must be validated
	// process identity, not a bare PID, so a recycled PID cannot inherit
	// someone else's ownership.
	Owner Witness
}

func (request AcquireRequest) validate() error {
	if !keyPattern.MatchString(request.Key) {
		return fmt.Errorf("%w: key must be a bounded lowercase slug", ErrInvalid)
	}
	if !fingerprintPattern.MatchString(request.Fingerprint) {
		return fmt.Errorf("%w: fingerprint must be a sha256 hex digest", ErrInvalid)
	}
	switch request.Mode {
	case ModeDisposable, ModeReusable:
	default:
		return fmt.Errorf("%w: mode must be disposable or reusable", ErrInvalid)
	}
	if err := request.Owner.validate(); err != nil {
		return err
	}
	if request.Owner.Zero() {
		return fmt.Errorf("%w: owner identity is required", ErrInvalid)
	}
	return nil
}

// Handle is a held session. Every mutation goes through it, and every mutation
// is durable before it returns.
type Handle struct {
	mu      sync.Mutex
	store   *Store
	session *Session
	// reattached records that this handle took over warm state rather than
	// creating it, which the caller needs in order to skip creation.
	reattached bool
	released   bool
}

// Acquire takes a session for the given key, reattaching to compatible warm
// state when there is any.
//
// The whole decision is serialized per key by an advisory file lock, so a
// concurrent attach and reset cannot interleave and two invocations cannot both
// believe they own the same warm state.
//
// Liveness is what decides whether existing warm state is available: a lease
// held by a process that is still the recorded one blocks reuse; a lease whose
// holder is dead — the SIGKILL case — is reclaimed.
func (store *Store) Acquire(ctx context.Context, request AcquireRequest, alive LivenessProbe) (*Handle, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	if alive == nil {
		alive = LiveProcess
	}
	lock, err := lockPath(ctx, store.keyLockPath(request.Key))
	if err != nil {
		return nil, fmt.Errorf("cannot lock session key %q: %w", request.Key, err)
	}
	defer func() { _ = unlock(lock) }()

	if request.Mode == ModeReusable {
		warm, err := store.warmSession(request.Key)
		if err != nil {
			return nil, err
		}
		if warm != nil {
			return store.reattach(warm, request, alive)
		}
	}
	return store.create(request)
}

// warmSession returns the reusable session recorded for key. At most one exists:
// a reusable session is only ever created under the key lock, and only when no
// other one is recorded.
func (store *Store) warmSession(key string) (*Session, error) {
	sessions, err := store.List()
	if err != nil {
		return nil, err
	}
	for _, session := range sessions {
		if session.Key == key && session.Mode == ModeReusable {
			return session, nil
		}
	}
	return nil, nil
}

func (store *Store) reattach(warm *Session, request AcquireRequest, alive LivenessProbe) (*Handle, error) {
	if warm.Lease != nil {
		if alive(warm.Lease.Holder) {
			return nil, fmt.Errorf("%w: %s holds key %q", ErrSessionBusy, warm.InvocationID, request.Key)
		}
		// A lease left by a dead holder means nobody applied a lifecycle to
		// these resources. Adopting them here would tell the caller to reuse a
		// container that may have died with its invocation, and would skip the
		// reconciliation that a crash is exactly what calls for.
		return nil, fmt.Errorf("%w: %s holds key %q", ErrRecoveryRequired, warm.InvocationID, request.Key)
	}
	if warm.Fingerprint != request.Fingerprint {
		return nil, &IncompatibleReuseError{
			Key:        request.Key,
			Invocation: warm.InvocationID,
			Want:       request.Fingerprint,
			Got:        warm.Fingerprint,
		}
	}
	warm.Lease = &Lease{Holder: request.Owner, AcquiredAt: store.now()}
	if err := store.write(warm); err != nil {
		return nil, err
	}
	return &Handle{store: store, session: warm, reattached: true}, nil
}

func (store *Store) create(request AcquireRequest) (*Handle, error) {
	invocation, err := newInvocationID()
	if err != nil {
		return nil, err
	}
	now := store.now()
	session := &Session{
		Schema:       SchemaV1,
		InvocationID: invocation,
		Key:          request.Key,
		Mode:         request.Mode,
		Fingerprint:  request.Fingerprint,
		Owner:        request.Owner,
		Lease:        &Lease{Holder: request.Owner, AcquiredAt: now},
		CreatedAt:    now,
		UpdatedAt:    now,
		Resources:    []Resource{},
	}
	if err := store.write(session); err != nil {
		return nil, err
	}
	return &Handle{store: store, session: session}, nil
}

// Session returns a copy of the current record.
func (handle *Handle) Session() Session {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	snapshot := *handle.session
	snapshot.Resources = append([]Resource(nil), handle.session.Resources...)
	return snapshot
}

// InvocationID is the identity backends bind resources to.
func (handle *Handle) InvocationID() string {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	return handle.session.InvocationID
}

// Reattached reports whether this handle took over warm state instead of
// creating a session. A reattached caller must reuse the recorded resources
// rather than create new ones.
func (handle *Handle) Reattached() bool {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	return handle.reattached
}

// Declare records a resource this session is about to create, before it is
// created. The record is durable when Declare returns, so a crash during
// creation still leaves the identifier the resource will carry — which is all
// recovery needs to ask the backend whether it exists.
//
// Declaring the same ref twice is idempotent and preserves the recorded
// disposition, so a retried creation does not reset a committed resource.
func (handle *Handle) Declare(resource Resource) (Ref, error) {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.released {
		return Ref{}, errors.New("session-ledger handle is released")
	}
	if resource.Disposition == "" {
		resource.Disposition = Declared
	}
	if resource.Ownership == "" {
		resource.Ownership = Created
	}
	if err := resource.validate(); err != nil {
		return Ref{}, err
	}
	ref := resource.Ref()
	if index := handle.indexOf(ref); index >= 0 {
		existing := handle.session.Resources[index]
		if existing.Ownership != resource.Ownership || existing.Data != resource.Data ||
			existing.VanishesOnStop != resource.VanishesOnStop {
			return Ref{}, fmt.Errorf("%w: resource %s/%s is already declared with different ownership",
				ErrInvalid, ref.Backend, ref.ID)
		}
		return ref, nil
	}
	handle.session.Resources = append(handle.session.Resources, resource)
	if err := handle.store.write(handle.session); err != nil {
		handle.session.Resources = handle.session.Resources[:len(handle.session.Resources)-1]
		return Ref{}, err
	}
	return ref, nil
}

// Adopt records a resource that already exists, together with the witness that
// proves which instance it is, in a single durable write.
//
// Use it for a resource whose identifier only exists once the resource does — a
// process group's pgid — where there is nothing to reserve ahead of creation
// and a Declare/Commit pair would leave a window in which the record names a
// resource it cannot prove is ours.
func (handle *Handle) Adopt(resource Resource, witness Witness) (Ref, error) {
	if witness.Zero() {
		return Ref{}, fmt.Errorf("%w: adopting %s/%s needs an identity witness",
			ErrInvalid, resource.Backend, resource.ID)
	}
	resource.Witness = witness
	resource.Disposition = Running
	return handle.Declare(resource)
}

// Commit records that a declared resource now exists, along with the witness
// that proves which instance it is.
func (handle *Handle) Commit(ref Ref, witness Witness) error {
	if witness.Zero() {
		return fmt.Errorf("%w: committing %s/%s needs an identity witness",
			ErrInvalid, ref.Backend, ref.ID)
	}
	return handle.update(ref, func(resource *Resource) {
		resource.Witness = witness
		resource.Disposition = Running
	})
}

// Dispose records a disposition reached outside reconciliation — a stop the
// caller performed itself, state it decided to retain — together with its
// receipt.
func (handle *Handle) Dispose(ref Ref, disposition Disposition, outcome Outcome) error {
	if outcome.At.IsZero() {
		outcome.At = handle.store.now()
	}
	return handle.update(ref, func(resource *Resource) {
		resource.Disposition = disposition
		resource.Outcome = &outcome
	})
}

func (handle *Handle) update(ref Ref, mutate func(*Resource)) error {
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.released {
		return errors.New("session-ledger handle is released")
	}
	index := handle.indexOf(ref)
	if index < 0 {
		return fmt.Errorf("%w: resource %s/%s was never declared", ErrInvalid, ref.Backend, ref.ID)
	}
	previous := handle.session.Resources[index]
	mutate(&handle.session.Resources[index])
	if err := handle.store.write(handle.session); err != nil {
		handle.session.Resources[index] = previous
		return err
	}
	return nil
}

func (handle *Handle) indexOf(ref Ref) int {
	for index, resource := range handle.session.Resources {
		if resource.Ref() == ref {
			return index
		}
	}
	return -1
}

// Release ends this process's hold on the session and applies lifecycle to
// what it owns, using the given backends.
//
// A disposable session is forgotten once every resource is terminal; a reusable
// one released under LifecycleKeepRunning stays warm for the next Acquire. The
// returned report is the session's cleanup receipt.
func (handle *Handle) Release(
	ctx context.Context,
	lifecycle Lifecycle,
	backends Backends,
	options ReconcileOptions,
) (*SessionReport, error) {
	// The mutex is held across the whole reconcile, not just the lookup. apply
	// rewrites session.Resources by index and republishes the record; a
	// concurrent Declare appending to that same slice would reallocate it under
	// apply's feet and lose the declared resource from the record, which is a
	// resource nothing will ever clean up.
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.released {
		return nil, errors.New("session-ledger handle is already released")
	}
	session := handle.session

	if err := lifecycle.Admits(session); err != nil {
		return nil, err
	}
	lock, err := lockPath(ctx, handle.store.keyLockPath(session.Key))
	if err != nil {
		return nil, fmt.Errorf("cannot lock session key %q: %w", session.Key, err)
	}
	defer func() { _ = unlock(lock) }()

	report, err := handle.store.apply(ctx, session, lifecycle, backends, options)
	if err != nil {
		return report, err
	}

	handle.released = true
	session.Lease = nil
	if writeErr := handle.store.write(session); writeErr != nil {
		return report, writeErr
	}
	if session.Mode == ModeDisposable && allForgettable(session) {
		return report, handle.store.Forget(session.InvocationID)
	}
	return report, nil
}

// Reset disposes of the warm state recorded for key and forgets it, so a caller
// that cannot acquire it — an incompatible plan fingerprint, state left by a
// crash — has a way to clear it deliberately.
//
// It is the operation IncompatibleReuseError points at. Without it a changed
// fixture or artifact wedges warm reuse permanently: the only handle to a
// session is Acquire, and Acquire is what refused.
//
// Reset refuses while a live invocation holds the session, and refuses whole
// when the session holds borrowed data — the same rule Release applies, since
// state this session did not create is not this session's to delete.
func (store *Store) Reset(
	ctx context.Context,
	key string,
	backends Backends,
	options ReconcileOptions,
) (*SessionReport, error) {
	if !keyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: key must be a bounded lowercase slug", ErrInvalid)
	}
	options = options.resolved()
	lock, err := lockPath(ctx, store.keyLockPath(key))
	if err != nil {
		return nil, fmt.Errorf("cannot lock session key %q: %w", key, err)
	}
	defer func() { _ = unlock(lock) }()

	warm, err := store.warmSession(key)
	if err != nil {
		return nil, err
	}
	if warm == nil {
		return nil, fmt.Errorf("%w: no warm state for key %q", ErrSessionNotFound, key)
	}
	if warm.Lease != nil && options.Alive(warm.Lease.Holder) {
		return nil, fmt.Errorf("%w: %s holds key %q", ErrSessionBusy, warm.InvocationID, key)
	}
	report, applyErr := store.apply(ctx, warm, LifecycleReset, backends, options)
	if applyErr != nil {
		return report, applyErr
	}
	warm.Lease = nil
	if writeErr := store.write(warm); writeErr != nil {
		return report, writeErr
	}
	if !allForgettable(warm) {
		// Something survived the reset — preserved as unowned, or refused for
		// want of an adapter. The record stays so it keeps naming an owner.
		return report, nil
	}
	return report, store.Forget(warm.InvocationID)
}

// Explain renders a session's ownership receipt as readable lines. It is the
// inspection surface a CLI shows a developer asking what a run left behind.
func Explain(session *Session) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "session %s key=%s mode=%s fingerprint=%s\n",
		session.InvocationID, session.Key, session.Mode, session.Fingerprint)
	if session.Lease == nil {
		builder.WriteString("  lease: released\n")
	} else {
		fmt.Fprintf(&builder, "  lease: held by pid %d since %s\n",
			session.Lease.Holder.PID, session.Lease.AcquiredAt.Format("2006-01-02T15:04:05Z"))
	}
	for _, resource := range session.Resources {
		fmt.Fprintf(&builder, "  %s %s/%s ownership=%s disposition=%s",
			resource.Kind, resource.Backend, resource.ID, resource.Ownership, resource.Disposition)
		if resource.Data {
			builder.WriteString(" data=true")
		}
		if resource.Outcome != nil {
			fmt.Fprintf(&builder, " outcome=%s/%s", resource.Outcome.Action, resource.Outcome.Result)
			if resource.Outcome.Reason != "" {
				fmt.Fprintf(&builder, "(%s)", resource.Outcome.Reason)
			}
		}
		builder.WriteString("\n")
	}
	return builder.String()
}
