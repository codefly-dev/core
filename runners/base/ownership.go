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

// Alive reports whether any process still belongs to the registered group.
func (group *TrackedProcessGroup) Alive() bool {
	return isProcessGroupAlive(group.record.PGID)
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
