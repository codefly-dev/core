package sessionledger

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/codefly-dev/core/runners/base"
)

// BackendNative is the adapter name for host process groups.
const BackendNative = "native"

// KindProcessGroup is the resource kind for a spawned process group.
const KindProcessGroup = "process.group"

// LiveProcess reports whether the process a witness describes is still running.
// It re-authenticates the full identity, so a PID that has been recycled by an
// unrelated process reads as dead — which is the difference between reaping a
// crashed run's leftovers and killing a stranger's shell.
//
// Only a proven absence counts as dead. An inspection that fails for any other
// reason — a process owned by another user that this one may not read, a
// transient failure reading the boot identity — leaves the answer unknown, and
// unknown must read as alive: "dead" is the branch that stops containers and
// terminates process groups, so guessing it is how a live run gets reaped.
func LiveProcess(witness Witness) bool {
	if witness.PID <= 1 {
		return false
	}
	identity, err := base.InspectProcess(witness.PID)
	return liveFromInspection(witness, identity, err)
}

// liveFromInspection is the whole liveness decision, separated from the syscall
// so the mapping from each inspection outcome can be pinned by a test.
func liveFromInspection(witness Witness, identity base.ProcessIdentity, err error) bool {
	if errors.Is(err, base.ErrProcessNotFound) {
		return false
	}
	if err != nil {
		return true
	}
	return witness.Matches(processWitness(identity))
}

// ProcessWitness is the identity to record for a live process.
func ProcessWitness(pid int) (Witness, error) {
	identity, err := base.InspectProcess(pid)
	if err != nil {
		return Witness{}, fmt.Errorf("cannot inspect process %d: %w", pid, err)
	}
	return processWitness(identity), nil
}

func processWitness(identity base.ProcessIdentity) Witness {
	return Witness{
		PID:        identity.PID,
		BootID:     identity.BootID,
		StartID:    identity.StartID,
		Executable: identity.Executable,
	}
}

// NativeBackend cleans up process groups registered by runners/base. It
// resolves a recorded pgid through that registry rather than signalling the
// number directly: the registry record carries the leader's boot and start
// identity plus a per-spawn authentication secret, so a group whose pgid has
// been recycled fails the claim instead of being killed.
//
// A pgid is not an ownership token. Unlike a container, a process group cannot
// be labelled with the invocation that created it, so the recorded witness is
// the only proof this adapter has — and it refuses to signal without one.
type NativeBackend struct{}

// NativeProcessGroup describes a spawned process group for the ledger, together
// with the witness that identifies this incarnation of the pgid.
//
// A process group has no identifier before it exists — the pgid is only known
// once the leader is running — so there is nothing to declare ahead of
// creation. Record it with Handle.Adopt, which publishes the resource and its
// witness in one durable write. runners/base has already persisted its own
// authenticated registry record by the time this is callable, so the
// write-ahead guarantee is not lost.
func NativeProcessGroup(group *base.TrackedProcessGroup) (Resource, Witness) {
	leader := group.Leader()
	return Resource{
		Kind:           KindProcessGroup,
		Backend:        BackendNative,
		ID:             strconv.Itoa(group.PGID()),
		Ownership:      Created,
		VanishesOnStop: true,
	}, Witness{
		PID:        leader.PID,
		BootID:     leader.BootID,
		StartID:    leader.StartID,
		Executable: leader.Executable,
	}
}

func (NativeBackend) group(resource Resource) (*base.TrackedProcessGroup, error) {
	pgid, err := strconv.Atoi(resource.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: process-group id %q is not a number", ErrInvalid, resource.ID)
	}
	group, err := base.LookupProcessGroup(pgid)
	if errors.Is(err, base.ErrProcessGroupNotRegistered) {
		// No registration means nothing here may be signalled. Whether or not
		// a process currently holds this pgid, it is not provably ours.
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return group, nil
}

// Claim resolves the recorded pgid to its registration and checks that the
// registered leader is still the one the ledger recorded.
//
// A record without a witness is refused rather than claimed. The ledger and the
// process-group registry are garbage-collected independently, so a ledger entry
// outlives the registration that authenticates it: once the registry record for
// a pgid is reaped, that number is free to be re-registered by an unrelated run.
// Claiming on the pgid alone would then resolve to that run's registration and
// terminate it — authenticated by its own token, and entirely the wrong target.
func (backend NativeBackend) Claim(_ context.Context, resource Resource, _ string) (Observation, error) {
	if resource.Witness.Zero() {
		return Observation{}, fmt.Errorf(
			"%w: process group %s was recorded without an identity witness", ErrNotOwned, resource.ID)
	}
	group, err := backend.group(resource)
	if err != nil {
		return Observation{}, err
	}
	leader := group.Leader()
	observed := Witness{
		PID:        leader.PID,
		BootID:     leader.BootID,
		StartID:    leader.StartID,
		Executable: leader.Executable,
	}
	if !resource.Witness.Matches(observed) {
		return Observation{}, ErrNotOwned
	}
	return Observation{Live: group.Alive(), Witness: observed}, nil
}

// Stop terminates the group: SIGTERM, grace, SIGKILL, delivered only to
// authenticated members. It re-checks the witness rather than trusting the
// preceding Claim, so a registration replaced in between is not signalled.
func (backend NativeBackend) Stop(ctx context.Context, resource Resource) error {
	if _, err := backend.Claim(ctx, resource, ""); err != nil {
		return err
	}
	group, err := backend.group(resource)
	if err != nil {
		return err
	}
	if err := group.Terminate(ctx); err != nil {
		return err
	}
	return group.RemoveIfDead()
}

// Delete is Stop: a process group holds no data of its own, so there is nothing
// left to remove once it is gone.
func (backend NativeBackend) Delete(ctx context.Context, resource Resource) error {
	return backend.Stop(ctx, resource)
}
