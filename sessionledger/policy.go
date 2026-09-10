package sessionledger

import (
	"errors"
	"fmt"
)

// Lifecycle is what a caller wants done with a whole session's resources.
type Lifecycle string

const (
	// LifecycleStop stops execution and retains data. It is the end-of-run
	// default and the only lifecycle crash recovery applies on its own: a dead
	// invocation's intent is unknowable, and stopping execution while keeping
	// its data is the choice that cannot destroy work.
	LifecycleStop Lifecycle = "stop"
	// LifecycleKeepRunning leaves everything as it is. It is explicit reuse:
	// the session is released warm and a later Acquire with the same
	// fingerprint reattaches to it.
	LifecycleKeepRunning Lifecycle = "keep-running"
	// LifecycleReset deletes owned disposable state. It is refused for a
	// session holding borrowed data rather than partially applied.
	LifecycleReset Lifecycle = "reset"
)

// Action is what a lifecycle asks a backend to do to one resource.
type Action string

const (
	// ActionKeep leaves the resource running.
	ActionKeep Action = "keep"
	// ActionStop ends execution and preserves any data.
	ActionStop Action = "stop"
	// ActionDelete removes the resource and its data.
	ActionDelete Action = "delete"
)

// Reason classes recorded in an Outcome. They are stable strings, safe for
// logs and for a caller deciding what to tell a developer.
const (
	// ReasonBorrowed means the resource belongs to someone else.
	ReasonBorrowed = "borrowed"
	// ReasonDataRetained means a stop deliberately kept the resource's data.
	ReasonDataRetained = "data-retained"
	// ReasonKeepRunning means explicit reuse asked for nothing to happen.
	ReasonKeepRunning = "keep-running"
	// ReasonNoAdapter means no backend adapter could speak for the resource.
	ReasonNoAdapter = "no-adapter"
	// ReasonNotOwned means the backend found something under this identifier
	// that it could not prove belongs to the recorded invocation.
	ReasonNotOwned = "not-owned"
	// ReasonAlreadyTerminal means a previous reconciliation already finished
	// with this resource.
	ReasonAlreadyTerminal = "already-terminal"
	// ReasonNotRunning means the resource is present but was not executing, so
	// there was nothing to stop. A named volume is never anything else.
	ReasonNotRunning = "not-running"
	// ReasonBackendError means the backend was asked and returned an error.
	ReasonBackendError = "backend-error"
)

// ErrBorrowedDataNotDisposable is returned when a reset is asked of a session
// that holds borrowed data. Resetting would either destroy state this session
// does not own or silently skip part of what reset promises; refusing is the
// only honest answer.
var ErrBorrowedDataNotDisposable = errors.New("reset requires owned disposable state")

// Admits reports whether the lifecycle can be applied to this session at all.
// It fails whole, before any backend is called, so a refused reset leaves every
// resource exactly as it was.
func (lifecycle Lifecycle) Admits(session *Session) error {
	switch lifecycle {
	case LifecycleStop, LifecycleKeepRunning:
		return nil
	case LifecycleReset:
		for _, resource := range session.Resources {
			if resource.Ownership == Borrowed && resource.Data {
				return fmt.Errorf("%w: resource %s/%s is borrowed data",
					ErrBorrowedDataNotDisposable, resource.Backend, resource.ID)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: lifecycle %q is not defined", ErrInvalid, lifecycle)
	}
}

// Plan decides the action for one resource, and the reason class to record
// when that action is to leave the resource alone.
//
// Borrowed resources are never stopped and never deleted, under any lifecycle.
func (lifecycle Lifecycle) Plan(resource Resource) (Action, string) {
	if resource.Ownership == Borrowed {
		return ActionKeep, ReasonBorrowed
	}
	switch lifecycle {
	case LifecycleKeepRunning:
		return ActionKeep, ReasonKeepRunning
	case LifecycleReset:
		return ActionDelete, ""
	default:
		if resource.Data {
			return ActionStop, ReasonDataRetained
		}
		return ActionStop, ""
	}
}

// dispositionAfter maps a completed action to the disposition it leaves behind.
// An attempt that did not establish what the resource is — a failure, a missing
// adapter, a claim we could not prove — leaves the recorded disposition alone,
// so the next pass looks again rather than believing something that was never
// checked.
func dispositionAfter(action Action, resource Resource, outcome Outcome) Disposition {
	switch outcome.Result {
	case NotFound:
		return Deleted
	case Failed, Refused:
		return resource.Disposition
	case Preserved:
		if outcome.Reason == ReasonNotOwned || outcome.Reason == ReasonAlreadyTerminal {
			return resource.Disposition
		}
	}
	switch action {
	case ActionDelete:
		return Deleted
	case ActionStop:
		if resource.Data {
			return Retained
		}
		return Stopped
	default:
		if resource.Ownership == Borrowed {
			return Retained
		}
		return resource.Disposition
	}
}
