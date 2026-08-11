package base

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// Every successful start persists `<pgid>.pgid` under ~/.codefly/runs/.
// Stop() removes the file on clean exit. At startup, process-owning hosts call
// ReapStaleProcessGroups to authenticate and terminate groups whose recorded
// owner no longer exists.

const (
	pgidDirName   = "runs"
	pgidLockName  = ".reaper.lock"
	pgidLockRetry = 25 * time.Millisecond
	sigtermGrace  = 3 * time.Second
	sigkillGrace  = time.Second
	maxRecordSize = 16 << 10
)

var registryProcessLock = make(chan struct{}, 1)

type recordedProcessIdentity struct {
	PID        int    `json:"pid"`
	BootID     string `json:"boot_id"`
	StartID    uint64 `json:"start_id"`
	Executable string `json:"executable"`
}

type processBoundary struct {
	BootID  string `json:"boot_id"`
	StartID uint64 `json:"start_id"`
}

type pgidRecord struct {
	PGID       int                     `json:"pgid"`
	Leader     recordedProcessIdentity `json:"leader"`
	Owner      recordedProcessIdentity `json:"owner"`
	Registered processBoundary         `json:"registered"`
}

type recordSnapshot struct {
	info os.FileInfo
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
	dir := filepath.Join(home, ".codefly", pgidDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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

// WritePgidFile is the exported entry point used by spawners outside this
// package that use Setpgid:true and need their groups reaped on ungraceful
// parent death.
func WritePgidFile(pgid int, cwd string, argv []string) error {
	return writePgidFile(pgid, cwd, argv)
}

// RemovePgidFile drops the tracking file for a pgid after graceful stop.
func RemovePgidFile(pgid int) error {
	return removePgidFile(pgid)
}

func writePgidFile(pgid int, _ string, _ []string) (returnErr error) {
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

	leader, err := inspectProcess(pgid)
	if err != nil {
		return fmt.Errorf("inspect process-group leader %d: %w", pgid, err)
	}
	if leader.pgid != pgid {
		return fmt.Errorf("process %d belongs to process group %d", pgid, leader.pgid)
	}
	owner, err := inspectProcess(os.Getpid())
	if err != nil {
		return fmt.Errorf("inspect process-group owner %d: %w", os.Getpid(), err)
	}
	registered, err := currentProcessBoundary()
	if err != nil {
		return fmt.Errorf("inspect process registry clock: %w", err)
	}
	if leader.bootID != registered.BootID || owner.bootID != registered.BootID {
		return errors.New("process identities do not belong to the current boot")
	}

	rec := pgidRecord{
		PGID:       pgid,
		Leader:     leader.recorded(),
		Owner:      owner.recorded(),
		Registered: registered,
	}
	content, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode process-group record: %w", err)
	}
	content = append(content, '\n')
	path := filepath.Join(dir, fmt.Sprintf("%d.pgid", pgid))
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
	return nil
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

func removePgidFile(pgid int) (returnErr error) {
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
	path := filepath.Join(dir, fmt.Sprintf("%d.pgid", pgid))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// removePgidFileAfterExit forgets a naturally completed process group only
// after the kernel confirms the entire group is empty. If a leader exits while
// descendants remain, the record deliberately survives so the orphan sweep
// can still reap that process tree after its owner exits.
func removePgidFileAfterExit(pgid int) error {
	if pgid <= 1 || isProcessGroupAlive(pgid) {
		return nil
	}
	return removePgidFile(pgid)
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
	if len(data) > maxRecordSize {
		return pgidRecord{}, recordSnapshot{}, errors.New("record is too large")
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

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var rec pgidRecord
	if err := decoder.Decode(&rec); err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	if err := rec.validate(); err != nil {
		return pgidRecord{}, recordSnapshot{}, err
	}
	return rec, recordSnapshot{info: current}, nil
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
	if rec.Registered.BootID == "" || rec.Registered.StartID == 0 {
		return errors.New("invalid process-group registration boundary")
	}
	if rec.Leader.BootID != rec.Registered.BootID || rec.Owner.BootID != rec.Registered.BootID {
		return errors.New("process-group record crosses boot identities")
	}
	if rec.Leader.StartID > rec.Registered.StartID || rec.Owner.StartID > rec.Registered.StartID {
		return errors.New("process-group identity starts after registration")
	}
	return nil
}

func (identity recordedProcessIdentity) validate() error {
	if identity.PID <= 1 || identity.BootID == "" || identity.StartID == 0 || identity.Executable == "" {
		return errors.New("identity is incomplete")
	}
	if filepath.Base(identity.Executable) != identity.Executable {
		return errors.New("executable identity contains a path")
	}
	return nil
}

func recordIsUnchanged(path string, snapshot recordSnapshot) error {
	current, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !sameRecordFile(snapshot.info, current) {
		return errors.New("record changed during reconciliation")
	}
	return nil
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

// ReapStaleProcessGroups reconciles authenticated records in
// ~/.codefly/runs. Live owners are preserved; groups with dead or reused
// owners are terminated. Invalid records are retained and reported.
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
		return false, fmt.Errorf("read process-group record %s: %w", name, err)
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

	authenticated, err := authenticateProcessGroup(ctx, rec)
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
	authenticated, err = authenticateProcessGroup(ctx, rec)
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

func authenticateProcessGroup(ctx context.Context, rec pgidRecord) (bool, error) {
	leader, err := inspectProcess(rec.PGID)
	if err == nil {
		return leader.pgid == rec.PGID && leader.matches(rec.Leader), nil
	}
	if !errors.Is(err, errProcessNotFound) {
		return false, err
	}

	members, err := inspectProcessGroup(ctx, rec.PGID)
	if err != nil {
		return false, err
	}
	if len(members) == 0 {
		if isProcessGroupAlive(rec.PGID) {
			return false, errors.New("live leaderless group had no inspectable members")
		}
		return false, nil
	}
	for _, member := range members {
		if member.bootID == rec.Registered.BootID &&
			member.startID >= rec.Leader.StartID &&
			member.startID <= rec.Registered.StartID {
			return true, nil
		}
	}
	return false, nil
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
		pid := int(rawPID)
		actualGroup, err := syscall.Getpgid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil || actualGroup != pgid {
			continue
		}
		identity, err := inspectProcess(pid)
		if errors.Is(err, errProcessNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect process-group member %d: %w", pid, err)
		}
		if identity.pgid == pgid {
			identities = append(identities, identity)
		}
	}
	return identities, nil
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
	authenticated, err := authenticateProcessGroup(ctx, rec)
	if err != nil {
		return err
	}
	if !authenticated {
		return errors.New("process group identity changed before SIGTERM")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syscall.Kill(-rec.PGID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if waitForGroupDeath(ctx, rec.PGID, sigtermGrace) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	authenticated, err = authenticateProcessGroup(ctx, rec)
	if err != nil {
		return err
	}
	if !authenticated {
		return errors.New("process group identity changed after SIGTERM")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syscall.Kill(-rec.PGID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
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

func removeRecord(path string, snapshot recordSnapshot) error {
	if err := recordIsUnchanged(path, snapshot); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
