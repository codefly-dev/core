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

const (
	pgidRootDirName       = "runs"
	pgidRegistryNamespace = "authenticated-v1"
	pgidLockName          = ".reaper.lock"
	pgidLockRetry         = 25 * time.Millisecond
	sigtermGrace          = 3 * time.Second
	sigkillGrace          = time.Second
	maxRecordSize         = 16 << 10
	groupAuthBytes        = 32
	groupAuthEnv          = "CODEFLY_PROCESS_GROUP_AUTH"
)

var registryProcessLock = make(chan struct{}, 1)

var errProcessGroupIdentityChanged = errors.New("process group identity changed")

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
	if err == nil {
		err = group.persist()
	}
	if err != nil {
		_ = gate.Close()
		cleanupErr := abortUnregisteredProcessGroup(cmd, group, authentication)
		return nil, errors.Join(fmt.Errorf("register process group: %w", err), cleanupErr)
	}
	if err := gate.Release(); err != nil {
		cleanupErr := abortUnregisteredProcessGroup(cmd, group, authentication)
		removeErr := group.RemoveIfDead()
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

func abortUnregisteredProcessGroup(cmd *exec.Cmd, group *TrackedProcessGroup, authentication string) error {
	var failures []error
	if group != nil {
		if err := group.Signal(context.Background(), syscall.SIGKILL); err != nil &&
			!errors.Is(err, errProcessGroupIdentityChanged) {
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
					!errors.Is(err, errProcessGroupIdentityChanged) {
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
// remain active and are reported.
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

func authenticateProcessGroup(ctx context.Context, rec pgidRecord) ([]processIdentity, bool, error) {
	members, err := inspectProcessGroup(ctx, rec.PGID)
	if err != nil {
		return nil, false, err
	}
	if len(members) == 0 {
		if isProcessGroupAlive(rec.PGID) {
			return nil, false, errors.New("live process group had no inspectable members")
		}
		return nil, false, nil
	}
	for _, member := range members {
		if member.pid == rec.PGID {
			return members, member.matches(rec.Leader), nil
		}
	}

	var failures []error
	for _, member := range members {
		authenticated, err := processHasAuthentication(member, rec.Authentication)
		if errors.Is(err, errProcessNotFound) || errors.Is(err, errProcessGroupIdentityChanged) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("authenticate process-group member %d: %w", member.pid, err))
			continue
		}
		if authenticated {
			return members, true, nil
		}
	}
	if len(failures) > 0 {
		return nil, false, errors.Join(failures...)
	}
	return members, false, nil
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

func inspectProcessGroup(ctx context.Context, pgid int) ([]processIdentity, error) {
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	identities := make([]processIdentity, 0)
	for _, rawPID := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity, matched, err := inspectProcessGroupMember(rawPID, pgid)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		identities = append(identities, identity)
	}
	return identities, nil
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

func terminateAuthenticatedGroup(ctx context.Context, rec pgidRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := signalAuthenticatedGroup(ctx, rec, syscall.SIGTERM); err != nil {
		return err
	}
	if waitForGroupDeath(ctx, rec.PGID, sigtermGrace) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := signalAuthenticatedGroup(ctx, rec, syscall.SIGKILL); err != nil {
		return err
	}
	if waitForGroupDeath(ctx, rec.PGID, sigkillGrace) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("process group remained alive after SIGKILL")
}

func signalAuthenticatedGroup(ctx context.Context, rec pgidRecord, signal syscall.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	members, authenticated, err := authenticateProcessGroup(ctx, rec)
	if err != nil {
		return err
	}
	if !authenticated {
		return errProcessGroupIdentityChanged
	}
	return signalProcessIdentities(ctx, members, signal)
}

func signalProcessIdentities(ctx context.Context, identities []processIdentity, signal syscall.Signal) error {
	handles := make([]processSignalHandle, 0, len(identities))
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
			closeProcessSignalHandles(handles)
			return fmt.Errorf("open authenticated process %d: %w", identity.pid, err)
		}
		handles = append(handles, handle)
	}
	if len(handles) == 0 {
		return errProcessGroupIdentityChanged
	}

	var failures []error
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
