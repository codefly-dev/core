package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrProcessGroupNotRegistered is returned when no authenticated record exists
// for a process group. It proves absence of a registration, not of a process.
var ErrProcessGroupNotRegistered = errors.New("process group is not registered")

// ErrProcessNotFound reports that a PID names no live process.
var ErrProcessNotFound = errProcessNotFound

// ProcessIdentity is a process's full identity: the PID plus the boot and
// start-time facts that distinguish it from a later process that happens to
// reuse the same number. Persist PID, BootID and StartID to re-authenticate a
// birth after a crash; a PID alone is insufficient. PGID and Executable describe
// current execution state, which can change within that birth. Before signaling,
// re-inspect, compare the persisted birth, and supply the current full identity.
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

// LookupProcessGroup reads authenticated-v2 and authenticated-v1 registrations.
// It refuses ambiguous duplicate registrations. It returns the group for pgid, or
// ErrProcessGroupNotRegistered. The returned handle carries the record's
// authentication, so signalling through it reaches only processes that prove
// membership of the group that was registered — never whatever currently
// happens to hold the number.
func LookupProcessGroup(pgid int) (*TrackedProcessGroup, error) {
	var found *TrackedProcessGroup
	for _, namespace := range []string{pgidRegistryNamespace, pgidPreviousNamespace} {
		dir, err := pgidNamespaceDir(namespace)
		if err != nil {
			return nil, err
		}
		record, _, err := readPgidRecord(filepath.Join(dir, fmt.Sprintf("%d.pgid", pgid)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read process-group record: %w", err)
		}
		if found != nil {
			return nil, errors.New("process group has registrations in multiple namespaces")
		}
		found = &TrackedProcessGroup{record: record, namespace: namespace}
	}
	if found == nil {
		return nil, ErrProcessGroupNotRegistered
	}
	return found, nil
}

// ListProcessGroups returns registrations from the supported owner namespaces.
// This is discovery, not proof of liveness or authority to signal. Inspect each
// group's ownership and use its identity-checking methods for any signals.
// Unreadable, malformed or ambiguous registrations fail the enumeration; no
// partial list is returned and no records are modified.
func ListProcessGroups(ctx context.Context) ([]*TrackedProcessGroup, error) {
	var groups []*TrackedProcessGroup
	seen := make(map[int]bool)
	for _, namespace := range []string{pgidRegistryNamespace, pgidPreviousNamespace} {
		dir, err := pgidNamespaceDir(namespace)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pgid") {
				continue
			}
			pgid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".pgid"))
			if err != nil || pgid <= 1 || entry.Name() != fmt.Sprintf("%d.pgid", pgid) {
				return nil, fmt.Errorf("invalid process-group record name %q", entry.Name())
			}
			if seen[pgid] {
				continue
			}
			group, err := LookupProcessGroup(pgid)
			if errors.Is(err, ErrProcessGroupNotRegistered) {
				// Concurrent confirmed cleanup removed the registration.
				continue
			}
			if err != nil {
				return nil, err
			}
			seen[pgid] = true
			groups = append(groups, group)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return groups, nil
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
	// LaunchWorkspace is the marker captured at spawn, available only after
	// authentication. Empty means no recorded attribution (including v1).
	// It supplies scope metadata, never authority to signal.
	LaunchWorkspace string
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
		ownership.LaunchWorkspace = group.record.LaunchWorkspace
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

// SignalProcess delivers a signal only to the recorded process birth. Unlike
// TrackedProcessGroup.Signal, it addresses one process and requires no group
// registration, for owners of detached processes. The kernel handle binds
// delivery to the identity, including when the PID is reused during the call.
// An absent or replaced birth returns ErrProcessNotFound.
func SignalProcess(ctx context.Context, identity ProcessIdentity, signal syscall.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if identity.PID <= 1 || identity.PGID <= 0 || identity.BootID == "" || identity.StartID == 0 {
		return fmt.Errorf("signal process: a complete process identity is required")
	}
	handle, err := openProcessSignalHandle(processIdentity{
		pid: identity.PID, pgid: identity.PGID, bootID: identity.BootID,
		startID: identity.StartID, executable: identity.Executable,
	})
	if errors.Is(err, errProcessGroupIdentityChanged) {
		return ErrProcessNotFound
	}
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := handle.Signal(signal); errors.Is(err, syscall.ESRCH) {
		return ErrProcessNotFound
	} else {
		return err
	}
}
