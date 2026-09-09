package sessionledger

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by a Backend when nothing exists under a resource's
// identifier. It means the resource is gone, not that cleanup failed.
var ErrNotFound = errors.New("session-ledger resource does not exist")

// ErrNotOwned is returned by a Backend when something exists under a resource's
// identifier but the backend cannot prove it is the recorded invocation's.
// A resource that returns this is preserved: a name collision, a recycled PID,
// or a developer's own container must never be mistaken for ours.
var ErrNotOwned = errors.New("session-ledger resource is not provably owned by this invocation")

// Observation is what a Backend found when it claimed a resource.
type Observation struct {
	// Live says whether the resource is currently executing. A stopped
	// container that still holds its volume is present but not live.
	Live bool
	// Witness is the identity observed now. Reconciliation compares it against
	// the recorded witness before acting.
	Witness Witness
}

// Backend cleans up one kind of resource. It is the only component entitled to
// decide that a resource belongs to an invocation, because only it can read the
// backend's own state.
type Backend interface {
	// Claim proves that the resource named by ref exists and belongs to
	// invocation. It returns ErrNotFound when nothing is there, and ErrNotOwned
	// when something is but its ownership cannot be established.
	Claim(ctx context.Context, resource Resource, invocation string) (Observation, error)
	// Stop ends execution and preserves any data the resource holds.
	Stop(ctx context.Context, resource Resource) error
	// Delete removes the resource and its data.
	Delete(ctx context.Context, resource Resource) error
}

// Backends maps a resource's declared backend name to its adapter.
type Backends map[string]Backend

// LivenessProbe reports whether the process a witness describes is still
// running — the same process, not merely the same PID.
type LivenessProbe func(Witness) bool

// ReconcileOptions bounds a reconciliation.
type ReconcileOptions struct {
	// PerResourceTimeout caps how long one backend call may take, so a single
	// unresponsive container cannot stall recovery of everything else.
	// Defaults to DefaultPerResourceTimeout.
	PerResourceTimeout time.Duration
	// Alive decides whether a session's lease holder is still running.
	// Defaults to LiveProcess.
	Alive LivenessProbe
}

// DefaultPerResourceTimeout bounds one backend call during reconciliation.
const DefaultPerResourceTimeout = 30 * time.Second

func (options ReconcileOptions) resolved() ReconcileOptions {
	if options.PerResourceTimeout <= 0 {
		options.PerResourceTimeout = DefaultPerResourceTimeout
	}
	if options.Alive == nil {
		options.Alive = LiveProcess
	}
	return options
}

// ResourceReport is one resource's line in a cleanup receipt.
type ResourceReport struct {
	Ref     Ref
	Action  Action
	Outcome Outcome
}

// SessionReport is one session's cleanup receipt.
type SessionReport struct {
	InvocationID string
	Lifecycle    Lifecycle
	Resources    []ResourceReport
}

// RecoveryReport is what one recovery pass did.
type RecoveryReport struct {
	// Recovered is a receipt per session whose owner was found dead.
	Recovered []*SessionReport
	// Skipped names sessions left alone because their holder is still live.
	Skipped []string
	// Expired counts records dropped because nothing they named was still on
	// the machine and they were older than the retention window.
	Expired int
}

// Recover reconciles every session still holding a lease whose holder is no
// longer running. A released session already had its lifecycle applied by
// whoever released it and is never touched.
//
// This is the crash path: a CLI killed during Init or after Ready leaves a
// session record and a live lease behind, and the next invocation is the one
// that finds it. Only LifecycleStop is applied — a dead invocation's intent for
// its data is unknowable, so execution is ended and data kept. Sessions with a
// live holder are skipped, so a concurrent run's resources are never touched.
func Recover(
	ctx context.Context,
	store *Store,
	backends Backends,
	options ReconcileOptions,
) (*RecoveryReport, error) {
	options = options.resolved()
	sessions, listErr := store.List()
	report := &RecoveryReport{}
	var failures []error
	if listErr != nil {
		failures = append(failures, listErr)
	}
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return report, errors.Join(failures...)
		}
		// A released session already had its lifecycle applied by whoever
		// released it — warm state kept on purpose, or a run that finished.
		// Recovery is for the sessions nobody got to release.
		if session.Lease == nil || options.Alive(session.Lease.Holder) {
			report.Skipped = append(report.Skipped, session.InvocationID)
			continue
		}
		sessionReport, err := store.recoverOne(ctx, session, backends, options)
		if sessionReport != nil {
			report.Recovered = append(report.Recovered, sessionReport)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	expired, expireErr := store.Expire(ctx)
	report.Expired = expired
	if expireErr != nil {
		failures = append(failures, expireErr)
	}
	return report, errors.Join(failures...)
}

func (store *Store) recoverOne(
	ctx context.Context,
	session *Session,
	backends Backends,
	options ReconcileOptions,
) (*SessionReport, error) {
	lock, err := lockPath(ctx, store.keyLockPath(session.Key))
	if err != nil {
		return nil, fmt.Errorf("cannot lock session key %q: %w", session.Key, err)
	}
	defer func() { _ = unlock(lock) }()

	// Re-read under the lock: another process may have finished with this
	// session between List and here, and acting on the stale copy would
	// re-signal resources it already disposed of.
	current, err := store.Read(session.InvocationID)
	if errors.Is(err, ErrSessionNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if current.Lease == nil || options.Alive(current.Lease.Holder) {
		return nil, nil
	}
	report, applyErr := store.apply(ctx, current, LifecycleStop, backends, options)
	current.Lease = nil
	if writeErr := store.write(current); writeErr != nil {
		return report, errors.Join(applyErr, writeErr)
	}
	if current.Mode == ModeDisposable && allForgettable(current) {
		return report, errors.Join(applyErr, store.Forget(current.InvocationID))
	}
	return report, applyErr
}

// apply runs the lifecycle over a session's resources and records each outcome
// durably before moving to the next one, so an interrupted reconciliation never
// repeats work it already finished.
func (store *Store) apply(
	ctx context.Context,
	session *Session,
	lifecycle Lifecycle,
	backends Backends,
	options ReconcileOptions,
) (*SessionReport, error) {
	options = options.resolved()
	if err := lifecycle.Admits(session); err != nil {
		return nil, err
	}
	report := &SessionReport{InvocationID: session.InvocationID, Lifecycle: lifecycle}
	var failures []error
	for index := range session.Resources {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		resource := session.Resources[index]
		action, outcome := store.applyOne(ctx, session.InvocationID, resource, lifecycle, backends, options)
		session.Resources[index].Disposition = dispositionAfter(action, resource, outcome)
		session.Resources[index].Outcome = &outcome
		if err := store.write(session); err != nil {
			failures = append(failures, err)
			break
		}
		report.Resources = append(report.Resources, ResourceReport{
			Ref: resource.Ref(), Action: action, Outcome: outcome,
		})
		if outcome.Result == Failed {
			failures = append(failures, fmt.Errorf("cannot %s %s/%s", action, resource.Backend, resource.ID))
		}
	}
	return report, errors.Join(failures...)
}

func (store *Store) applyOne(
	ctx context.Context,
	invocation string,
	resource Resource,
	lifecycle Lifecycle,
	backends Backends,
	options ReconcileOptions,
) (Action, Outcome) {
	action, reason := lifecycle.Plan(resource)
	receipt := func(result Result, reason string) Outcome {
		return Outcome{Action: action, Result: result, Reason: reason, At: store.now()}
	}
	if resource.Terminal() {
		return action, receipt(Preserved, ReasonAlreadyTerminal)
	}
	if action == ActionKeep {
		return action, receipt(Preserved, reason)
	}
	backend, known := backends[resource.Backend]
	if !known {
		return action, receipt(Refused, ReasonNoAdapter)
	}

	callCtx, cancel := context.WithTimeout(ctx, options.PerResourceTimeout)
	defer cancel()

	observation, err := backend.Claim(callCtx, resource, invocation)
	switch {
	case errors.Is(err, ErrNotFound):
		return action, receipt(NotFound, "")
	case errors.Is(err, ErrNotOwned):
		return action, receipt(Preserved, ReasonNotOwned)
	case err != nil:
		return action, receipt(Failed, ReasonBackendError)
	}
	// A recorded witness that no longer matches what the backend observes means
	// the identifier was reused by something else. Same rule as an unprovable
	// claim: leave it alone.
	if !resource.Witness.Zero() && !resource.Witness.Matches(observation.Witness) {
		return action, receipt(Preserved, ReasonNotOwned)
	}

	if action == ActionDelete {
		if err := backend.Delete(callCtx, resource); err != nil {
			if errors.Is(err, ErrNotFound) {
				return action, receipt(NotFound, "")
			}
			return action, receipt(Failed, ReasonBackendError)
		}
		return action, receipt(Succeeded, "")
	}
	if !observation.Live {
		// Present, but not executing: a stopped container, a named volume.
		// There is nothing to stop and nothing to report as missing.
		return action, receipt(Preserved, ReasonNotRunning)
	}
	if err := backend.Stop(callCtx, resource); err != nil {
		if errors.Is(err, ErrNotFound) {
			return action, receipt(NotFound, reason)
		}
		return action, receipt(Failed, ReasonBackendError)
	}
	return action, receipt(Succeeded, reason)
}
