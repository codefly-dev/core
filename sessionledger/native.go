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
func LiveProcess(witness Witness) bool {
	if witness.PID <= 1 {
		return false
	}
	identity, err := base.InspectProcess(witness.PID)
	if err != nil {
		return false
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
type NativeBackend struct{}

// NativeProcessGroup describes a spawned process group for the ledger.
func NativeProcessGroup(group *base.TrackedProcessGroup) (Resource, Witness) {
	leader := group.Leader()
	return Resource{
		Kind:      KindProcessGroup,
		Backend:   BackendNative,
		ID:        strconv.Itoa(group.PGID()),
		Ownership: Created,
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
func (backend NativeBackend) Claim(_ context.Context, resource Resource, _ string) (Observation, error) {
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
	if !resource.Witness.Zero() && !resource.Witness.Matches(observed) {
		return Observation{}, ErrNotOwned
	}
	return Observation{Live: group.Alive(), Witness: observed}, nil
}

// Stop terminates the group: SIGTERM, grace, SIGKILL, delivered only to
// authenticated members.
func (backend NativeBackend) Stop(ctx context.Context, resource Resource) error {
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
