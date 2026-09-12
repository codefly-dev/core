package base

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/codefly-dev/core/wool"
	"github.com/gofrs/flock"
	"github.com/shirou/gopsutil/v3/process"
)

// forwardLines reads r line by line and writes each line — WITH its trailing
// newline — as a single Write to w. This preserves log-prefix boundaries
// (wool's per-Write prefix still applies per line) AND keeps newline
// separators intact (JSON-lines, structured logs, anything newline-
// delimited works). ReadBytes grows its buffer to hold a whole line no
// matter how large, so a single oversized event (base64 blob, minified
// stack) is forwarded intact — unlike a bufio.Scanner, which caps its token
// at a fixed size and, once exceeded, silently drops that line and the rest
// of the stream. On a write failure the remaining input is drained so the
// child never blocks on a full pipe before the forwarder closes its read-end.
// Shared by NativeProc.Forward and NixProc.forward.
func forwardLines(r io.Reader, w io.Writer) {
	if w == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(line); werr != nil {
				_, _ = io.Copy(io.Discard, br)
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// Orphan-process-group reaping.
//
// NativeProc spawns every child with Setpgid=true, making pid == pgid. Stop()
// tree-kills the group via `kill(-pgid, SIGTERM/SIGKILL)`. That works on the
// graceful path; on SIGKILL of the CLI it never runs and the whole tree is
// reparented to PID 1. The in-memory pgid dies with the Go process, so nobody
// knows which groups to reap on the next invocation.
//
// Every successful start persists `<pgid>.pgid` under the authenticated
// registry namespace in ~/.codefly/runs/. Independently released agents may
// carry different record contracts, so record formats never share a directory.
// Stop() removes the file on clean exit. At startup, process-owning hosts call
// ReapStaleProcessGroups to authenticate and terminate groups whose recorded
// owner no longer exists.
//
// An older codefly wrote a plaintext record (`pgid=`/`parent=`/`started=`/
// `cwd=`/`cmd=`) loose in ~/.codefly/runs/ rather than under a namespace.
// Those records name process groups this reaper would otherwise strand across
// an upgrade, so ReapStaleProcessGroups also reconciles them — corroborating a
// live leader's wall-clock start time against the record's spawn second in
// place of the authentication token those records predate. Namespaced contracts
// live in subdirectories and are never scanned; only loose files matching the
// exact legacy schema are reconciled. That schema predates namespacing and was
// shared by every agent on this runner, so a group is only ever terminated once
// it is proven orphaned — a recognized record that names a live, owner-managed
// group is preserved regardless of which agent wrote it.

const (
	pgidRootDirName       = "runs"
	pgidRegistryNamespace = "authenticated-v1"
	pgidLockName          = ".reaper.lock"
	pgidLockRetry         = 25 * time.Millisecond
	sigtermGrace          = 3 * time.Second
	sigkillGrace          = time.Second
	killSweeps            = 2
	// killSweepInterval is how long the first SIGKILL sweep waits before
	// re-sweeping. A member that forked between enumeration and delivery is
	// already running when the sweep lands, so the re-sweep only has to
	// outlast scheduling, not the full SIGKILL grace — which keeps the total
	// kill phase at sigkillGrace plus this interval.
	killSweepInterval = 100 * time.Millisecond
	maxRecordSize     = 16 << 10
	groupAuthBytes    = 32
	groupAuthEnv      = "CODEFLY_PROCESS_GROUP_AUTH"
	// legacyStartCorroborationSkew bounds how far a legacy record's recorded
	// spawn second may trail the leader's wall-clock start second before the
	// two stop corroborating. The record's `started` is stamped just after the
	// child forks, so the genuine leader's start second is at or a hair below
	// it; a recycled pgid's leader starts strictly later and fails this gate.
	legacyStartCorroborationSkew int64 = 5
)

var registryProcessLock = make(chan struct{}, 1)

var errProcessGroupIdentityChanged = errors.New("process group identity changed")

// errProcessGroupNotSignalable reports that nothing in the group could be
// signalled on this pass — it is empty, or everything in it is a zombie
// holding the pgid until it is reaped, or every member vanished between
// enumeration and delivery. It says nothing about identity: callers that are
// escalating must keep going and let group liveness decide, never treat it as
// proof the group is foreign.
var errProcessGroupNotSignalable = errors.New("process group has no signalable members")

type recordedProcessIdentity struct {
	PID        int    `json:"pid"`
	BootID     string `json:"boot_id"`
	StartID    uint64 `json:"start_id"`
	Executable string `json:"executable"`
}

type pgidRecord struct {
	PGID           int                     `json:"pgid"`
	Leader         recordedProcessIdentity `json:"leader"`
	Owner          recordedProcessIdentity `json:"owner"`
	Authentication string                  `json:"authentication"`
}

type recordSnapshot struct {
	info os.FileInfo
}

// TrackedProcessGroup is the identity-bearing handle returned for a process
// group whose private registry record was durably published.
type TrackedProcessGroup struct {
	record pgidRecord
}

type processSignalHandle interface {
	Signal(syscall.Signal) error
	Close() error
}

func sanitizeExecutableIdentity(executable string) string {
	if executable == "" {
		return ""
	}
	return filepath.Base(executable)
}

func (identity processIdentity) recorded() recordedProcessIdentity {
	return recordedProcessIdentity{
		PID:        identity.pid,
		BootID:     identity.bootID,
		StartID:    identity.startID,
		Executable: identity.executable,
	}
}

func (identity processIdentity) matches(recorded recordedProcessIdentity) bool {
	return identity.pid == recorded.PID && identity.bootID == recorded.BootID && identity.startID == recorded.StartID
}

func pgidStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	// The namespace is part of the current record contract. Codefly agents are
	// released independently; an agent with another contract must neither parse
	// nor quarantine these records, and this reaper must never inspect theirs.
	dir := filepath.Join(home, ".codefly", pgidRootDirName, pgidRegistryNamespace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create pgid dir: %w", err)
	}
	return dir, nil
}

func pgidFilePath(pgid int) (string, error) {
	dir, err := pgidStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%d.pgid", pgid)), nil
}

func acquireRegistryLock(ctx context.Context, dir string) (*flock.Flock, error) {
	select {
	case registryProcessLock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	lock := flock.New(filepath.Join(dir, pgidLockName), flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, pgidLockRetry)
	if err != nil {
		<-registryProcessLock
		return nil, err
	}
	if !locked {
		<-registryProcessLock
		return nil, errors.New("lock was not acquired")
	}
	return lock, nil
}

func releaseRegistryLock(lock *flock.Flock) error {
	err := lock.Unlock()
	closeErr := lock.Close()
	<-registryProcessLock
	return errors.Join(err, closeErr)
}

// StartTrackedProcessGroup starts cmd as a new process-group leader and
// returns only after its authenticated registry record is published.
func StartTrackedProcessGroup(cmd *exec.Cmd) (*TrackedProcessGroup, error) {
	return startProcessGroup(cmd, true)
}

// OwnedProcessGroup is a process group this process started and is the only
// process that can clean up. It is a distinct type from TrackedProcessGroup
// on purpose: a tracked group is authenticated against a registry record that
// outlives its owner and must prove itself by credential, whereas an owned
// group is authenticated against a leader identity captured at start. Sharing
// one type would let a caller pick the weaker proof for a tracked group by
// calling the wrong method, and would let a tracked group be torn down without
// its registry record ever being removed.
type OwnedProcessGroup struct {
	record pgidRecord
}

// StartOwnedProcessGroup starts cmd as a process-group leader whose identity is
// captured before the child can fork, without publishing a registry record.
// Nothing is left on disk, so ReapStaleProcessGroups never sees the group. Use
// it for groups the caller may deliberately release to outlive this process;
// use StartTrackedProcessGroup when the group must stay reapable across an
// unexpected death of the owner.
func StartOwnedProcessGroup(cmd *exec.Cmd) (*OwnedProcessGroup, error) {
	group, err := startProcessGroup(cmd, false)
	if err != nil {
		return nil, err
	}
	return &OwnedProcessGroup{record: group.record}, nil
}

// PGID reports the process-group id this handle owns.
func (group *OwnedProcessGroup) PGID() int {
	if group == nil {
		return 0
	}
	return group.record.PGID
}

// Terminate ends every member of this group with a bounded SIGTERM → SIGKILL
// escalation, and returns once the group is empty or the escalation is spent.
// termGrace is how long members get to honour SIGTERM before the kill phase
// starts; callers on an interactive path pass a short one.
//
// The unit of cleanup is the group, never the leader: liveness is observed on
// the process group itself, so a descendant that ignores SIGTERM is still
// escalated to SIGKILL after the grace period even when the leader exited
// promptly and was already reaped. Every pass re-authenticates the group (see
// authenticateOwnedProcessGroup) and signals member incarnations pinned at
// enumeration time rather than a bare -pgid, so a recycled pgid is never
// signalled. A member that cannot be reached is reported but never cancels
// delivery to the rest.
//
// A group that is gone is not an error — including a pgid that now names a
// provably different group, which means ours emptied. Everything else — a
// permission failure, a member that could not be verified, a group that
// outlived SIGKILL — is returned so callers can report it.
//
// Terminate only ends processes. Docker containers a member created belong to
// the Docker daemon, not to this group, and no signal here removes them.
func (group *OwnedProcessGroup) Terminate(ctx context.Context, termGrace time.Duration) error {
	if group == nil {
		return nil
	}
	if !isProcessGroupAlive(group.record.PGID) {
		return nil
	}
	err := terminateGroup(ctx, group.record, authenticateOwnedProcessGroup, termGrace)
	if errors.Is(err, errProcessGroupIdentityChanged) {
		// The pgid names a group that is provably not the one we started, so
		// ours emptied and nothing of ours was signalled or survives.
		return nil
	}
	return err
}

func startProcessGroup(cmd *exec.Cmd, register bool) (*TrackedProcessGroup, error) {
	if cmd == nil {
		return nil, errors.New("process-group command is nil")
	}
	if cmd.Process != nil {
		return nil, errors.New("process-group command is already started")
	}
	authentication, err := mintProcessGroupAuthentication()
	if err != nil {
		return nil, fmt.Errorf("mint process-group authentication: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	gate, executable, err := startProcessGroupGate(cmd, authentication)
	if err != nil {
		return nil, err
	}

	group, err := captureProcessGroup(cmd.Process.Pid, authentication, executable)
	if err == nil && register {
		err = group.persist()
	}
	if err != nil {
		_ = gate.Close()
		cleanupErr := abortUnregisteredProcessGroup(cmd, group, authentication)
		return nil, errors.Join(fmt.Errorf("register process group: %w", err), cleanupErr)
	}
	if err := gate.Release(); err != nil {
		cleanupErr := abortUnregisteredProcessGroup(cmd, group, authentication)
		var removeErr error
		if register {
			removeErr = group.RemoveIfDead()
		}
		return nil, errors.Join(fmt.Errorf("release process-group start gate: %w", err), cleanupErr, removeErr)
	}
	return group, nil
}

type processGroupGate struct {
	reader *os.File
	writer *os.File
}

func startProcessGroupGate(cmd *exec.Cmd, authentication string) (*processGroupGate, string, error) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		return nil, "", fmt.Errorf("locate process-group start gate: %w", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, "", fmt.Errorf("create process-group start gate: %w", err)
	}
	gate := &processGroupGate{reader: reader, writer: writer}
	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	if originalPath == "" || len(originalArgs) == 0 {
		_ = gate.Close()
		return nil, "", errors.New("process-group command has no executable")
	}
	originalEnv := cmd.Env
	originalExtraFiles := cmd.ExtraFiles
	gateFD := 3 + len(originalExtraFiles)
	gateFDValue := strconv.Itoa(gateFD)
	cmd.Path = shell
	cmd.Args = append([]string{"sh", "-c", "IFS= read -r codefly_gate < \"$CODEFLY_PROCESS_GROUP_GATE\" || exit 125; eval \"exec ${CODEFLY_PROCESS_GROUP_GATE_FD}<&-\"; unset CODEFLY_PROCESS_GROUP_GATE CODEFLY_PROCESS_GROUP_GATE_FD; exec \"$@\"", "codefly-process-group-gate", originalPath}, originalArgs[1:]...)
	cmd.Env = append(cmd.Environ(),
		groupAuthEnv+"="+authentication,
		"CODEFLY_PROCESS_GROUP_GATE=/dev/fd/"+gateFDValue,
		"CODEFLY_PROCESS_GROUP_GATE_FD="+gateFDValue)
	cmd.ExtraFiles = append(append([]*os.File(nil), originalExtraFiles...), reader)
	err = cmd.Start()
	cmd.Path = originalPath
	cmd.Args = originalArgs
	cmd.Env = originalEnv
	cmd.ExtraFiles = originalExtraFiles
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	gate.reader = nil
	if err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	return gate, sanitizeExecutableIdentity(originalPath), nil
}

func (gate *processGroupGate) Release() error {
	if gate == nil || gate.writer == nil {
		return nil
	}
	_, writeErr := gate.writer.Write([]byte{'\n'})
	closeErr := gate.writer.Close()
	gate.writer = nil
	return errors.Join(writeErr, closeErr)
}

func (gate *processGroupGate) Close() error {
	if gate == nil {
		return nil
	}
	var failures []error
	if gate.reader != nil {
		failures = append(failures, gate.reader.Close())
		gate.reader = nil
	}
	if gate.writer != nil {
		failures = append(failures, gate.writer.Close())
		gate.writer = nil
	}
	return errors.Join(failures...)
}

func mintProcessGroupAuthentication() (string, error) {
	value := make([]byte, groupAuthBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func captureProcessGroup(pgid int, authentication, executable string) (*TrackedProcessGroup, error) {
	leader, err := inspectProcess(pgid)
	if err != nil {
		return nil, fmt.Errorf("inspect process-group leader %d: %w", pgid, err)
	}
	if leader.pgid != pgid {
		return nil, fmt.Errorf("process %d belongs to process group %d", pgid, leader.pgid)
	}
	if executable == "" {
		return nil, errors.New("process-group executable identity is empty")
	}
	leader.executable = executable
	owner, err := inspectProcess(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("inspect process-group owner %d: %w", os.Getpid(), err)
	}
	if leader.bootID != owner.bootID {
		return nil, errors.New("process identities do not belong to the current boot")
	}
	return &TrackedProcessGroup{record: pgidRecord{
		PGID:           pgid,
		Leader:         leader.recorded(),
		Owner:          owner.recorded(),
		Authentication: authentication,
	}}, nil
}

func (group *TrackedProcessGroup) persist() (returnErr error) {
	rec := group.record
	dir, err := pgidStateDir()
	if err != nil {
		return err
	}
	lock, err := acquireRegistryLock(context.Background(), dir)
	if err != nil {
		return fmt.Errorf("lock process-group registry: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, releaseRegistryLock(lock))
	}()
	path := filepath.Join(dir, fmt.Sprintf("%d.pgid", rec.PGID))
	if _, err := os.Lstat(path); err == nil {
		existing, _, readErr := readPgidRecord(path)
		if readErr != nil {
			return fmt.Errorf("existing process-group record is invalid: %w", readErr)
		}
		if !sameProcessGroupRegistration(existing, rec) {
			return errors.New("process-group record already belongs to another identity")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect process-group record: %w", err)
	}

	content, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode process-group record: %w", err)
	}
	content = append(content, '\n')
	file, err := os.CreateTemp(dir, ".pgid-*.tmp")
	if err != nil {
		return fmt.Errorf("create process-group record: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("make process-group record private: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write process-group record: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync process-group record: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close process-group record: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish process-group record: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open process-group registry for sync: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("sync process-group registry: %w", err)
	}
	return nil
}

func sameProcessGroupRegistration(first, second pgidRecord) bool {
	return first.PGID == second.PGID &&
		first.Leader == second.Leader &&
		first.Owner == second.Owner &&
		first.Authentication == second.Authentication
}

// Signal delivers signal only to process instances authenticated as members
// of this registered group.
func (group *TrackedProcessGroup) Signal(ctx context.Context, signal syscall.Signal) error {
	if group == nil {
		return errors.New("process group is not registered")
	}
	return signalAuthenticatedGroup(ctx, group.record, signal)
}

// Terminate stops every authenticated member and waits for the process group
// to become empty. The registration remains until RemoveIfDead confirms that
// termination completed.
func (group *TrackedProcessGroup) Terminate(ctx context.Context) error {
	if group == nil {
		return errors.New("process group is not registered")
	}
	if !isProcessGroupAlive(group.record.PGID) {
		return nil
	}
	err := terminateAuthenticatedGroup(ctx, group.record)
	if errors.Is(err, errProcessGroupIdentityChanged) && !isProcessGroupAlive(group.record.PGID) {
		return nil
	}
	return err
}

func abortUnregisteredProcessGroup(cmd *exec.Cmd, group *TrackedProcessGroup, authentication string) error {
	var failures []error
	if group != nil {
		if err := group.Signal(context.Background(), syscall.SIGKILL); err != nil &&
			!errors.Is(err, errProcessGroupIdentityChanged) && !errors.Is(err, errProcessGroupNotSignalable) {
			failures = append(failures, fmt.Errorf("terminate unregistered process group: %w", err))
		}
	} else if cmd.Process != nil {
		members, err := inspectProcessGroup(context.Background(), cmd.Process.Pid)
		if err != nil {
			failures = append(failures, fmt.Errorf("inspect unregistered process group: %w", err))
		} else {
			credentialed := make([]processIdentity, 0, len(members))
			for _, member := range members {
				authenticated, authErr := processHasAuthentication(member, authentication)
				if authErr == nil && authenticated {
					credentialed = append(credentialed, member)
				}
			}
			if len(credentialed) > 0 {
				if err := signalProcessIdentities(context.Background(), credentialed, syscall.SIGKILL); err != nil &&
					!errors.Is(err, errProcessGroupIdentityChanged) && !errors.Is(err, errProcessGroupNotSignalable) {
					failures = append(failures, fmt.Errorf("terminate credentialed processes: %w", err))
				}
			}
		}
	}
	if cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			failures = append(failures, fmt.Errorf("terminate process-group leader: %w", err))
		}
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			failures = append(failures, fmt.Errorf("wait for unregistered process group: %w", err))
		}
	}
	return errors.Join(failures...)
}

// CommandSummary is safe for logs and process metadata: it identifies the
// executable and argument count without persisting tokens, passwords, or other
// values commonly passed on argv.
func CommandSummary(argv []string) string {
	if len(argv) == 0 {
		return "<empty>"
	}
	return fmt.Sprintf("%s <%d args>", filepath.Base(argv[0]), len(argv)-1)
}

// RemoveIfDead removes this registration only when it still names the same
// record and the process group is empty.
func (group *TrackedProcessGroup) RemoveIfDead() (returnErr error) {
	if group == nil {
		return nil
	}
	dir, err := pgidStateDir()
	if err != nil {
		return err
	}
	lock, err := acquireRegistryLock(context.Background(), dir)
	if err != nil {
		return fmt.Errorf("lock process-group registry: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, releaseRegistryLock(lock))
	}()
	path := filepath.Join(dir, fmt.Sprintf("%d.pgid", group.record.PGID))
	rec, snapshot, err := readPgidRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read process-group record before removal: %w", err)
	}
	if !sameProcessGroupRegistration(rec, group.record) || isProcessGroupAlive(rec.PGID) {
		return nil
	}
	return removeRecord(path, snapshot)
}

// isProcessGroupAlive probes whether any process still belongs to pgid.
func isProcessGroupAlive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitForGroupDeath(ctx context.Context, pgid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !isProcessGroupAlive(pgid) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return !isProcessGroupAlive(pgid)
		case <-ticker.C:
		}
	}
}

func readPgidRecord(path string) (pgidRecord, recordSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	if !before.Mode().IsRegular() {
		return pgidRecord{}, recordSnapshot{}, errors.New("record is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecordSize+1))
	if err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	current, err := os.Stat(path)
	if err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	if !sameRecordFile(before, after) || !sameRecordFile(after, current) {
		return pgidRecord{}, recordSnapshot{}, errors.New("record changed while being read")
	}
	snapshot := recordSnapshot{info: current}
	if len(data) > maxRecordSize {
		return pgidRecord{}, snapshot, errors.New("record is too large")
	}

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var rec pgidRecord
	if err := decoder.Decode(&rec); err != nil {
		return pgidRecord{}, snapshot, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return pgidRecord{}, snapshot, err
	}
	if err := rec.validate(); err != nil {
		return pgidRecord{}, snapshot, err
	}
	return rec, snapshot, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("record contains multiple JSON values")
		}
		return err
	}
	return nil
}

func sameRecordFile(first, second os.FileInfo) bool {
	return os.SameFile(first, second) &&
		first.Size() == second.Size() &&
		first.ModTime().Equal(second.ModTime())
}

func (rec pgidRecord) validate() error {
	if rec.PGID <= 1 || rec.Leader.PID != rec.PGID {
		return errors.New("invalid process-group leader")
	}
	if err := rec.Leader.validate(); err != nil {
		return fmt.Errorf("invalid process-group leader identity: %w", err)
	}
	if err := rec.Owner.validate(); err != nil {
		return fmt.Errorf("invalid process-group owner identity: %w", err)
	}
	if rec.Leader.BootID != rec.Owner.BootID {
		return errors.New("process-group record crosses boot identities")
	}
	decoded, err := hex.DecodeString(rec.Authentication)
	if err != nil || len(decoded) != groupAuthBytes {
		return errors.New("invalid process-group authentication")
	}
	return nil
}

func (identity recordedProcessIdentity) validate() error {
	if identity.PID < 1 || identity.BootID == "" || identity.StartID == 0 || identity.Executable == "" {
		return errors.New("identity is incomplete")
	}
	if filepath.Base(identity.Executable) != identity.Executable {
		return errors.New("executable identity contains a path")
	}
	return nil
}

func recordIsUnchanged(path string, snapshot recordSnapshot) error {
	if snapshot.info == nil {
		return errors.New("record has no stable file identity")
	}
	current, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !sameRecordFile(snapshot.info, current) {
		return errors.New("record changed during reconciliation")
	}
	return nil
}

// quarantineInvalidRecord atomically removes stable malformed state from the
// active registry while retaining the exact bytes for operator forensics. It
// never interprets the record and therefore can never authorize a signal.
func quarantineInvalidRecord(path string, snapshot recordSnapshot) (string, error) {
	if err := recordIsUnchanged(path, snapshot); err != nil {
		return "", err
	}
	quarantinePath := path + ".invalid"
	if err := os.Rename(path, quarantinePath); err != nil {
		return "", err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return quarantinePath, err
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return quarantinePath, err
	}
	return quarantinePath, nil
}

// IsProcessAlive tests a single PID via Signal(0). Exported so the docker
// runner package can reuse the liveness check.
func IsProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// ReapStaleProcessGroups reconciles authenticated records in the current
// contract namespace. Live owners are preserved; groups with dead or reused
// owners are terminated. Stable malformed records are quarantined without
// signaling any process; records that cannot prove a stable file identity
// remain active and are reported. Codefly's own legacy plaintext records,
// written loose in the registry root by an older release, are reconciled too
// so their groups cannot leak across an upgrade.
func ReapStaleProcessGroups(ctx context.Context) (returnErr error) {
	w := wool.Get(ctx).In("base.ReapStaleProcessGroups")
	dir, err := pgidStateDir()
	if err != nil {
		return err
	}
	lock, err := acquireRegistryLock(ctx, dir)
	if err != nil {
		return fmt.Errorf("lock process-group registry: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, releaseRegistryLock(lock))
	}()

	totalReaped := 0
	var failures []error
	for {
		reaped, passErr := sweepOnce(ctx, dir)
		if passErr != nil {
			failures = append(failures, passErr)
		}
		totalReaped += reaped
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if reaped == 0 {
			break
		}
	}
	if ctx.Err() == nil {
		legacyReaped, legacyErr := sweepLegacyRecords(ctx, filepath.Dir(dir))
		if legacyErr != nil {
			failures = append(failures, legacyErr)
		}
		totalReaped += legacyReaped
	}
	if totalReaped > 0 {
		w.Info("reaped stale process groups", wool.Field("count", totalReaped))
	}
	return errors.Join(failures...)
}

func sweepOnce(ctx context.Context, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("cannot read pgid dir: %w", err)
	}

	reaped := 0
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pgid") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		didReap, reconcileErr := reconcilePgidRecord(ctx, path, entry.Name())
		if didReap {
			reaped++
		}
		if reconcileErr != nil {
			failures = append(failures, reconcileErr)
		}
	}
	return reaped, errors.Join(failures...)
}

func reconcilePgidRecord(ctx context.Context, path, name string) (bool, error) {
	w := wool.Get(ctx).In("base.reconcilePgidRecord")
	rec, snapshot, err := readPgidRecord(path)
	if err != nil {
		if snapshot.info == nil {
			return false, fmt.Errorf("read process-group record %s: %w", name, err)
		}
		quarantinePath, quarantineErr := quarantineInvalidRecord(path, snapshot)
		if quarantineErr != nil {
			return false, errors.Join(
				fmt.Errorf("read process-group record %s: %w", name, err),
				fmt.Errorf("quarantine invalid process-group record %s: %w", name, quarantineErr),
			)
		}
		w.Warn("quarantined invalid process-group record without signaling",
			wool.Field("file", name),
			wool.Field("quarantine", filepath.Base(quarantinePath)),
			wool.Field("reason", err.Error()))
		return false, nil
	}
	fields := []*wool.LogField{
		wool.Field("pgid", rec.PGID),
		wool.Field("owner", rec.Owner.PID),
		wool.Field("executable", rec.Leader.Executable),
		wool.Field("file", name),
	}

	if !isProcessGroupAlive(rec.PGID) {
		if err := removeRecord(path, snapshot); err != nil {
			return false, fmt.Errorf("remove dead process-group record %s: %w", name, err)
		}
		return false, nil
	}

	_, authenticated, err := authenticateProcessGroup(ctx, rec)
	if err != nil {
		return false, fmt.Errorf("authenticate process group %d from %s: %w", rec.PGID, name, err)
	}
	if !authenticated {
		if err := removeRecord(path, snapshot); err != nil {
			return false, fmt.Errorf("remove rejected process-group record %s: %w", name, err)
		}
		w.Warn("rejected process-group record without signaling a reused group", fields...)
		return false, nil
	}

	ownerAlive, err := recordedOwnerAlive(rec.Owner)
	if err != nil {
		return false, fmt.Errorf("authenticate process-group owner %d from %s: %w", rec.Owner.PID, name, err)
	}
	if ownerAlive {
		return false, nil
	}
	if err := recordIsUnchanged(path, snapshot); err != nil {
		return false, fmt.Errorf("verify process-group record %s before signaling: %w", name, err)
	}
	_, authenticated, err = authenticateProcessGroup(ctx, rec)
	if err != nil {
		return false, fmt.Errorf("reauthenticate process group %d from %s: %w", rec.PGID, name, err)
	}
	if !authenticated {
		if err := removeRecord(path, snapshot); err != nil {
			return false, fmt.Errorf("remove rejected process-group record %s: %w", name, err)
		}
		return false, nil
	}

	w.Warn("reaping orphaned process group from prior run", fields...)
	if err := terminateAuthenticatedGroup(ctx, rec); err != nil {
		return false, fmt.Errorf("reap process group %d from %s: %w", rec.PGID, name, err)
	}
	if err := removeRecord(path, snapshot); err != nil {
		return true, fmt.Errorf("remove reaped process-group record %s: %w", name, err)
	}
	return true, nil
}

type legacyProcessRecord struct {
	pgid    int
	parent  int
	started int64
	command string
}

// sweepLegacyRecords reconciles the plaintext records an older codefly wrote
// loose in the registry root before records moved under a namespace. Namespaced
// foreign contracts live in subdirectories and are never scanned here; among
// loose files, only those that name a pgid and parse as the exact legacy schema
// are touched. That schema was shared by every agent built on this runner
// before namespacing, so a matched record may have been written by a different
// agent binary — reaping the orphaned group it names is still correct, and any
// file that is not this codefly's legacy format is left byte-for-byte intact.
func sweepLegacyRecords(ctx context.Context, root string) (int, error) {
	w := wool.Get(ctx).In("base.sweepLegacyRecords")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("cannot read legacy pgid dir: %w", err)
	}
	reaped := 0
	var stranded []int
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if entry.IsDir() {
			continue
		}
		pgid, ok := legacyRecordPGID(entry.Name())
		if !ok {
			continue
		}
		path := filepath.Join(root, entry.Name())
		outcome, reconcileErr := reconcileLegacyRecord(ctx, path, entry.Name(), pgid)
		switch outcome {
		case legacyOutcomeReaped:
			reaped++
		case legacyOutcomeStranded:
			stranded = append(stranded, pgid)
		}
		if reconcileErr != nil {
			failures = append(failures, reconcileErr)
		}
	}
	if len(stranded) > 0 {
		w.Warn("legacy process groups are still alive but cannot be authenticated for termination",
			wool.Field("count", len(stranded)),
			wool.Field("pgids", stranded))
	}
	return reaped, errors.Join(failures...)
}

func legacyRecordPGID(name string) (int, bool) {
	stem, ok := strings.CutSuffix(strings.TrimSuffix(name, ".invalid"), ".pgid")
	if !ok {
		return 0, false
	}
	pgid, err := strconv.Atoi(stem)
	if err != nil || pgid <= 1 {
		return 0, false
	}
	return pgid, true
}

func parseLegacyProcessRecord(path string) (legacyProcessRecord, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxRecordSize {
		return legacyProcessRecord{}, false
	}
	var rec legacyProcessRecord
	var sawPGID, sawParent, sawStarted, sawCWD, sawCommand bool
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return legacyProcessRecord{}, false
		}
		switch key {
		case "pgid":
			pgid, err := strconv.Atoi(value)
			if err != nil || pgid <= 1 {
				return legacyProcessRecord{}, false
			}
			rec.pgid, sawPGID = pgid, true
		case "parent":
			parent, err := strconv.Atoi(value)
			if err != nil || parent < 0 {
				return legacyProcessRecord{}, false
			}
			rec.parent, sawParent = parent, true
		case "started":
			started, err := strconv.ParseInt(value, 10, 64)
			if err != nil || started <= 0 {
				return legacyProcessRecord{}, false
			}
			rec.started, sawStarted = started, true
		case "cwd":
			sawCWD = true
		case "cmd":
			rec.command, sawCommand = value, true
		default:
			return legacyProcessRecord{}, false
		}
	}
	if !(sawPGID && sawParent && sawStarted && sawCWD && sawCommand) {
		return legacyProcessRecord{}, false
	}
	return rec, true
}

type legacyOutcome int

const (
	// legacyOutcomeSettled covers records that named a dead group (dropped),
	// a live and managed group (preserved), or nothing this codefly owns.
	legacyOutcomeSettled legacyOutcome = iota
	// legacyOutcomeReaped: an orphaned group was authenticated and terminated.
	legacyOutcomeReaped
	// legacyOutcomeStranded: a live group that cannot be authenticated for
	// termination (its leader has exited, or its pgid was reused). The caller
	// surfaces these in aggregate so an operator sees a leak the reaper cannot
	// safely clear.
	legacyOutcomeStranded
)

func reconcileLegacyRecord(ctx context.Context, path, name string, pgid int) (legacyOutcome, error) {
	w := wool.Get(ctx).In("base.reconcileLegacyRecord")
	rec, ok := parseLegacyProcessRecord(path)
	if !ok || rec.pgid != pgid {
		return legacyOutcomeSettled, nil
	}
	if !isProcessGroupAlive(rec.pgid) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return legacyOutcomeSettled, fmt.Errorf("remove dead legacy process-group record %s: %w", name, err)
		}
		return legacyOutcomeSettled, nil
	}
	fields := []*wool.LogField{
		wool.Field("pgid", rec.pgid),
		wool.Field("owner", rec.parent),
		wool.Field("command", rec.command),
		wool.Field("file", name),
	}
	ownerAlive, err := legacyOwnerAlive(rec.parent, rec.started)
	if err != nil {
		return legacyOutcomeSettled, fmt.Errorf("read legacy process-group owner %d start time from %s: %w", rec.parent, name, err)
	}
	if ownerAlive {
		w.Debug("preserving live legacy process group with a live owner", fields...)
		return legacyOutcomeSettled, nil
	}
	leader, err := inspectProcess(rec.pgid)
	if errors.Is(err, errProcessNotFound) {
		w.Debug("legacy process group is alive but leaderless; cannot authenticate for termination", fields...)
		return legacyOutcomeStranded, nil
	}
	if err != nil {
		return legacyOutcomeSettled, fmt.Errorf("inspect legacy process-group leader %d from %s: %w", rec.pgid, name, err)
	}
	corroborated, err := legacyLeaderCorroborates(leader, rec.started)
	if err != nil {
		return legacyOutcomeSettled, fmt.Errorf("read legacy process-group leader %d start time from %s: %w", rec.pgid, name, err)
	}
	if !corroborated {
		w.Debug("legacy process-group id was reused; refusing to signal an unauthenticated group", fields...)
		return legacyOutcomeStranded, nil
	}

	w.Warn("reaping orphaned legacy process group from a prior codefly", fields...)
	if err := terminateLegacyGroup(ctx, rec.pgid, rec.started); err != nil {
		if errors.Is(err, errProcessGroupIdentityChanged) {
			w.Debug("legacy process group changed identity before termination; leaving it unsignaled", fields...)
			return legacyOutcomeStranded, nil
		}
		return legacyOutcomeSettled, fmt.Errorf("reap legacy process group %d from %s: %w", rec.pgid, name, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return legacyOutcomeReaped, fmt.Errorf("remove reaped legacy process-group record %s: %w", name, err)
	}
	return legacyOutcomeReaped, nil
}

// legacyOwnerAlive reports whether the recorded owner is still the process that
// spawned the group. A genuine owner forked the leader and stamped `started`
// just after, so it cannot be younger than the record. A live PID that
// post-dates the record is a recycled PID, not the original owner — the group
// is orphaned and eligible for reaping. The legacy format carries no owner boot
// or start identity, so this second-granularity start comparison is the only
// available discriminator; it errs toward preserving (treating an ambiguous
// same-second PID as the live owner) rather than risking a live managed group.
func legacyOwnerAlive(parent int, started int64) (bool, error) {
	if parent <= 0 {
		return false, nil
	}
	startSecond, err := processStartUnixSeconds(parent)
	if err != nil {
		if errors.Is(err, process.ErrorProcessNotRunning) {
			return false, nil
		}
		return false, err
	}
	return startSecond <= started, nil
}

// legacyLeaderCorroborates authenticates a legacy record — which predates the
// authentication token — by matching the live leader's wall-clock start second
// against the record's spawn second. A recycled pgid's leader always starts
// after the record was written and fails this gate. leader must be the process
// whose PID equals the recorded pgid; a group whose leader has exited (only
// descendants survive) cannot be corroborated and is never signaled.
func legacyLeaderCorroborates(leader processIdentity, started int64) (bool, error) {
	if leader.pgid != leader.pid {
		return false, nil
	}
	startSecond, err := processStartUnixSeconds(leader.pid)
	if err != nil {
		if errors.Is(err, process.ErrorProcessNotRunning) {
			return false, nil
		}
		return false, err
	}
	return startSecond <= started && started-startSecond <= legacyStartCorroborationSkew, nil
}

func processStartUnixSeconds(pid int) (int64, error) {
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, err
	}
	createdMillis, err := proc.CreateTime()
	if err != nil {
		return 0, err
	}
	return createdMillis / 1000, nil
}

func terminateLegacyGroup(ctx context.Context, pgid int, started int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// "Nothing signalable" is not a failure to escalate on: the group may be
	// momentarily all zombies, or every member may have exited between
	// enumeration and delivery. Liveness decides, exactly as in terminateGroup.
	if err := signalLegacyGroup(ctx, pgid, started, syscall.SIGTERM); err != nil &&
		!errors.Is(err, errProcessGroupNotSignalable) {
		return err
	}
	if waitForGroupDeath(ctx, pgid, sigtermGrace) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := signalLegacyGroup(ctx, pgid, started, syscall.SIGKILL); err != nil &&
		!errors.Is(err, errProcessGroupNotSignalable) {
		return err
	}
	if waitForGroupDeath(ctx, pgid, sigkillGrace) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("legacy process group remained alive after SIGKILL")
}

// signalLegacyGroup re-authenticates the group's leader by its wall-clock start
// second, then signals every current member through an audit-token handle
// rather than by pgid. Signaling by pgid is unsafe for a leaderless group: once
// the leader exits and its PID (== the pgid) is reaped, that PID can be recycled
// as a new, unrelated group leader while the original descendants keep the pgid
// alive, and `kill(-pgid)` would then hit the unrelated tree. Requiring a live,
// corroborated leader on every pass, and delivering only to member incarnations
// pinned at enumeration time, matches the authenticated path and closes that
// window; a group whose leader has exited is reported stranded, never signaled.
func signalLegacyGroup(ctx context.Context, pgid int, started int64, signal syscall.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	members, err := inspectProcessGroup(ctx, pgid)
	if err != nil {
		return err
	}
	leaderPresent := false
	for _, member := range members {
		if member.pid != pgid {
			continue
		}
		corroborated, err := legacyLeaderCorroborates(member, started)
		if err != nil {
			return err
		}
		if !corroborated {
			return errProcessGroupIdentityChanged
		}
		leaderPresent = true
		break
	}
	if !leaderPresent {
		return errProcessGroupIdentityChanged
	}
	return signalProcessIdentities(ctx, members, signal)
}

func authenticateProcessGroup(ctx context.Context, rec pgidRecord) ([]processIdentity, bool, error) {
	members, inspectErr := inspectProcessGroup(ctx, rec.PGID)
	if len(members) == 0 {
		return nil, false, errors.Join(errProcessGroupNotSignalable, inspectErr)
	}
	for _, member := range members {
		if member.pid == rec.PGID {
			if member.matches(rec.Leader) {
				return members, true, inspectErr
			}
			return nil, false, nil
		}
	}

	credentialed, err := anyMemberIsCredentialed(members, rec.Authentication)
	if err != nil {
		return nil, false, errors.Join(inspectErr, err)
	}
	if credentialed {
		return members, true, inspectErr
	}
	return members, false, nil
}

// anyMemberIsCredentialed reports whether at least one member carries the
// group's start credential. Members that vanish or change identity mid-check
// are skipped; a member we cannot read at all is a failure to decide, not a
// negative answer, so it is returned rather than silently counted as "no".
func anyMemberIsCredentialed(members []processIdentity, authentication string) (bool, error) {
	var failures []error
	for _, member := range members {
		credentialed, err := processHasAuthentication(member, authentication)
		if errors.Is(err, errProcessNotFound) || errors.Is(err, errProcessGroupIdentityChanged) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("authenticate process-group member %d: %w", member.pid, err))
			continue
		}
		if credentialed {
			return true, nil
		}
	}
	return false, errors.Join(failures...)
}

func processHasAuthentication(expected processIdentity, authentication string) (bool, error) {
	value, err := readProcessGroupAuthentication(expected.pid)
	if err != nil {
		return false, err
	}
	current, err := inspectProcess(expected.pid)
	if err != nil {
		return false, err
	}
	if current.pgid != expected.pgid || !current.matches(expected.recorded()) {
		return false, errProcessGroupIdentityChanged
	}
	return value == authentication, nil
}

// inspectProcessGroup enumerates the group's inspectable members, returning
// them alongside a diagnostic for the ones it could not inspect.
//
// A member that cannot be inspected must never hide the members that can:
// cleanup has to be able to signal the rest of the tree. A process caught
// mid-exec fails inspection because its two identity reads disagree, and a
// member whose credentials changed can fail it outright — aborting the whole
// enumeration there would leave the entire group unsignalled.
func inspectProcessGroup(ctx context.Context, pgid int) ([]processIdentity, error) {
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	identities := make([]processIdentity, 0)
	var failures []error
	for _, rawPID := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity, matched, err := inspectProcessGroupMember(rawPID, pgid)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !matched {
			continue
		}
		identities = append(identities, identity)
	}
	return identities, errors.Join(failures...)
}

func inspectProcessGroupMember(rawPID int32, pgid int) (processIdentity, bool, error) {
	// ARCHITECTURE: A process enumerator is observational evidence, not an
	// authority. gopsutil can transiently surface PID 0 while Darwin's process
	// table changes. Getpgid(0) means the current process rather than an invalid
	// process, so reject every non-positive observation before any syscall can
	// accidentally authenticate Codefly itself as a member of the target group.
	if rawPID <= 0 {
		return processIdentity{}, false, nil
	}
	pid := int(rawPID)
	actualGroup, err := syscall.Getpgid(pid)
	if errors.Is(err, syscall.ESRCH) {
		return processIdentity{}, false, nil
	}
	if err != nil || actualGroup != pgid {
		return processIdentity{}, false, nil
	}
	identity, err := inspectProcess(pid)
	if errors.Is(err, errProcessNotFound) {
		return processIdentity{}, false, nil
	}
	if err != nil {
		return processIdentity{}, false, fmt.Errorf("inspect process-group member %d: %w", pid, err)
	}
	if identity.pgid != pgid {
		return processIdentity{}, false, nil
	}
	return identity, true, nil
}

func recordedOwnerAlive(owner recordedProcessIdentity) (bool, error) {
	identity, err := inspectProcess(owner.PID)
	if errors.Is(err, errProcessNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return identity.bootID == owner.BootID && identity.startID == owner.StartID, nil
}

// groupAuthenticator proves that a live process group is still the one a
// record names, and returns the members to signal.
type groupAuthenticator func(context.Context, pgidRecord) ([]processIdentity, bool, error)

func terminateAuthenticatedGroup(ctx context.Context, rec pgidRecord) error {
	return terminateGroup(ctx, rec, authenticateProcessGroup, sigtermGrace)
}

// terminateGroup escalates SIGTERM to SIGKILL over the group, observing
// liveness on the group itself rather than on its leader, so members that
// outlive a leader which exited on SIGTERM are still escalated.
//
// Only a proven-foreign group stops the escalation. A pass that could not
// reach every member, or that found nothing signalable because the group is
// momentarily all zombies, is recorded and the escalation continues: group
// liveness decides when we are done, never one member's error. Diagnostics are
// reported only if the group is still alive when the budget is spent.
func terminateGroup(ctx context.Context, rec pgidRecord, authenticate groupAuthenticator, termGrace time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var diagnostics []error
	record := func(err error) bool {
		if errors.Is(err, errProcessGroupIdentityChanged) {
			return false
		}
		if err != nil && !errors.Is(err, errProcessGroupNotSignalable) {
			diagnostics = append(diagnostics, err)
		}
		return true
	}

	if err := signalGroup(ctx, rec, authenticate, syscall.SIGTERM); !record(err) {
		return err
	}
	if waitForGroupDeath(ctx, rec.PGID, termGrace) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(append(diagnostics, err)...)
	}
	// Members are signalled as incarnations pinned at enumeration time, so a
	// member that forks between the enumeration and the delivery leaves a
	// child no pass has covered. That child's parent is dead by then and
	// cannot spawn another, so one short re-sweep converges. The last sweep
	// carries the full SIGKILL grace, which keeps the kill phase's cost at
	// sigkillGrace plus one killSweepInterval.
	for sweep := range killSweeps {
		if !isProcessGroupAlive(rec.PGID) {
			return nil
		}
		if err := signalGroup(ctx, rec, authenticate, syscall.SIGKILL); !record(err) {
			return err
		}
		grace := killSweepInterval
		if sweep == killSweeps-1 {
			grace = sigkillGrace
		}
		if waitForGroupDeath(ctx, rec.PGID, grace) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(diagnostics, err)...)
		}
	}
	return errors.Join(append(diagnostics, errors.New("process group remained alive after SIGKILL"))...)
}

func signalAuthenticatedGroup(ctx context.Context, rec pgidRecord, signal syscall.Signal) error {
	return signalGroup(ctx, rec, authenticateProcessGroup, signal)
}

// signalGroup delivers signal to the group's members once the group has been
// authenticated. It separates the three outcomes an escalating caller must
// tell apart: errProcessGroupIdentityChanged means the group is provably not
// ours and must never be signalled again, errProcessGroupNotSignalable means
// there was nothing to signal on this pass, and any other error is a partial
// failure that leaves the rest of the group signalled.
func signalGroup(ctx context.Context, rec pgidRecord, authenticate groupAuthenticator, signal syscall.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	members, authenticated, diagnostics := authenticate(ctx, rec)
	switch {
	case errors.Is(diagnostics, errProcessGroupNotSignalable):
		return diagnostics
	case !authenticated && diagnostics != nil:
		// We could not decide whether the group is ours. Report that rather
		// than claiming either answer.
		return diagnostics
	case !authenticated:
		return errProcessGroupIdentityChanged
	}
	return errors.Join(signalProcessIdentities(ctx, members, signal), diagnostics)
}

// authenticateOwnedProcessGroup authenticates a group whose leader this
// process started and has held ever since, which is a stronger position than
// the reaper's: the pgid was pinned by the leader from the moment it forked,
// and afterwards by whichever descendants keep the group non-empty.
//
// A member occupying the pgid must therefore be the recorded leader; anything
// else means the group emptied and the pid was reused, and nothing is
// signalled. For a leaderless group the start credential decides wherever the
// platform lets us read another process's environment — on Linux it does, and
// requiring it there closes the reuse window outright. Darwin's kern.procargs2
// returns argv without the environment to a non-root caller (verified against
// a same-user direct child), so there the credential cannot be read at all and
// the group is accepted on the process-group invariant alone: a group id is
// only ever created by a leader holding that pid, so a foreign group could
// occupy it only by ours emptying, the pid being recycled, and the new leader
// dying too. See processEnvironmentReadable.
func authenticateOwnedProcessGroup(ctx context.Context, rec pgidRecord) ([]processIdentity, bool, error) {
	members, inspectErr := inspectProcessGroup(ctx, rec.PGID)
	if len(members) == 0 {
		return nil, false, errors.Join(errProcessGroupNotSignalable, inspectErr)
	}
	for _, member := range members {
		if member.pid == rec.PGID {
			if member.matches(rec.Leader) {
				return members, true, inspectErr
			}
			return nil, false, nil
		}
	}
	if !processEnvironmentReadable() {
		return members, true, inspectErr
	}
	credentialed, err := anyMemberIsCredentialed(members, rec.Authentication)
	if err != nil {
		return nil, false, errors.Join(inspectErr, err)
	}
	if credentialed {
		return members, true, inspectErr
	}
	return nil, false, nil
}

func signalProcessIdentities(ctx context.Context, identities []processIdentity, signal syscall.Signal) error {
	handles := make([]processSignalHandle, 0, len(identities))
	var openFailures []error
	for _, identity := range identities {
		if err := ctx.Err(); err != nil {
			closeProcessSignalHandles(handles)
			return err
		}
		handle, err := openProcessSignalHandle(identity)
		if errors.Is(err, errProcessNotFound) || errors.Is(err, errProcessGroupIdentityChanged) {
			continue
		}
		if err != nil {
			// Report the member we cannot reach, but keep opening the rest:
			// one unreachable process must not spare the whole tree.
			openFailures = append(openFailures, fmt.Errorf("open authenticated process %d: %w", identity.pid, err))
			continue
		}
		handles = append(handles, handle)
	}
	if len(handles) == 0 {
		return errors.Join(append(openFailures, errProcessGroupNotSignalable)...)
	}

	failures := openFailures
	for _, handle := range handles {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if err := handle.Signal(signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			failures = append(failures, err)
		}
	}
	failures = append(failures, closeProcessSignalHandles(handles))
	return errors.Join(failures...)
}

func closeProcessSignalHandles(handles []processSignalHandle) error {
	var failures []error
	for _, handle := range handles {
		failures = append(failures, handle.Close())
	}
	return errors.Join(failures...)
}

func removeRecord(path string, snapshot recordSnapshot) error {
	if err := recordIsUnchanged(path, snapshot); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
