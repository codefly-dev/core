package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrProcessGroupNotRegistered is returned when no authenticated record exists
// for a process group. It proves absence of a registration, not of a process.
var ErrProcessGroupNotRegistered = errors.New("process group is not registered")

// ErrProcessNotFound reports that a PID names no live process.
var ErrProcessNotFound = errProcessNotFound

// ProcessIdentity is a process's full identity: the PID plus the boot and
// start-time facts that distinguish it from a later process that happens to
// reuse the same number. Callers persisting ownership must record all of it —
// a PID on its own cannot be re-authenticated after a crash.
type ProcessIdentity struct {
	PID        int
	PGID       int
	BootID     string
	StartID    uint64
	Executable string
}

// InspectProcess returns the full identity of a live process. It wraps
// ErrProcessNotFound when the PID names nothing.
func InspectProcess(pid int) (ProcessIdentity, error) {
	identity, err := inspectProcess(pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	return ProcessIdentity{
		PID:        identity.pid,
		PGID:       identity.pgid,
		BootID:     identity.bootID,
		StartID:    identity.startID,
		Executable: identity.executable,
	}, nil
}

// LookupProcessGroup returns the registered group for pgid, or
// ErrProcessGroupNotRegistered. The returned handle carries the record's
// authentication, so signalling through it reaches only processes that prove
// membership of the group that was registered — never whatever currently
// happens to hold the number.
func LookupProcessGroup(pgid int) (*TrackedProcessGroup, error) {
	dir, err := pgidStateDir()
	if err != nil {
		return nil, err
	}
	record, _, err := readPgidRecord(filepath.Join(dir, fmt.Sprintf("%d.pgid", pgid)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrProcessGroupNotRegistered
		}
		return nil, fmt.Errorf("read process-group record: %w", err)
	}
	return &TrackedProcessGroup{record: record}, nil
}

// PGID is the registered process group's identifier.
func (group *TrackedProcessGroup) PGID() int {
	return group.record.PGID
}

// Leader is the recorded identity of the process group's leader.
func (group *TrackedProcessGroup) Leader() ProcessIdentity {
	return ProcessIdentity{
		PID:        group.record.Leader.PID,
		PGID:       group.record.PGID,
		BootID:     group.record.Leader.BootID,
		StartID:    group.record.Leader.StartID,
		Executable: group.record.Leader.Executable,
	}
}

// Alive checks only PGID liveness, not registered ownership. Use
// InspectOwnership to classify managed processes.
func (group *TrackedProcessGroup) Alive() bool {
	return isProcessGroupAlive(group.record.PGID)
}

// ProcessGroupOwnership is a read-only observation, not authority to signal a
// PID. Members are populated only when the registered group authenticates.
// Authenticated=false means ownership was not proven, not that the group died.
type ProcessGroupOwnership struct {
	Authenticated bool
	OwnerAlive    bool
	Members       []ProcessIdentity
}

// InspectOwnership uses the same membership and owner checks as registry
// recovery, including credentials for surviving leaderless groups. Errors mean
// inspection is incomplete; they must not be interpreted as an absent owner.
// Signals must still go through the group's identity-checking methods.
func (group *TrackedProcessGroup) InspectOwnership(ctx context.Context) (*ProcessGroupOwnership, error) {
	if group == nil {
		return nil, ErrProcessGroupNotRegistered
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	members, authenticated, err := authenticateProcessGroup(ctx, group.record)
	if err != nil {
		return nil, err
	}
	ownerAlive, err := recordedOwnerAlive(group.record.Owner)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ownership := &ProcessGroupOwnership{Authenticated: authenticated, OwnerAlive: ownerAlive}
	if authenticated {
		for _, member := range members {
			ownership.Members = append(ownership.Members, ProcessIdentity{
				PID: member.pid, PGID: member.pgid, BootID: member.bootID,
				StartID: member.startID, Executable: member.executable,
			})
		}
	}
	return ownership, nil
}

// Terminate ends the registered group: SIGTERM, a grace period, then SIGKILL,
// delivered only to processes that authenticate as members of this
// registration.
func (group *TrackedProcessGroup) Terminate(ctx context.Context) error {
	if group == nil {
		return ErrProcessGroupNotRegistered
	}
	return terminateAuthenticatedGroup(ctx, group.record)
}
