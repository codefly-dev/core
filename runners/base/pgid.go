package base

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/codefly-dev/core/wool"
)

// forwardLines reads lines from r and writes each line — WITH its trailing
// newline — as a single Write to w. This preserves log-prefix boundaries
// (wool's per-Write prefix still applies per line) AND keeps newline
// separators intact (JSON-lines, structured logs, anything newline-
// delimited works). Scanner buffer is raised to 1MiB from the 64KiB
// default so large test output or error stacks don't silently truncate.
// Shared by NativeProc.Forward and NixProc.forward.
func forwardLines(r io.Reader, w io.Writer) {
	if w == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// Re-append the newline so the downstream writer sees the real
		// separator — losing it was the JSON-lines bug.
		if _, err := w.Write(append(line, '\n')); err != nil {
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
// Fix: every successful start persists `<pgid>.pgid` under ~/.codefly/runs/.
// Stop() removes the file on clean exit. At startup, `codefly run service`
// calls ReapStaleProcessGroups, which kills any still-alive groups whose file
// is on disk. The file's lingering presence is the authoritative "spawned and
// not cleanly stopped" signal.

const pgidDirName = "runs"

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

// WritePgidFile is the exported entry point used by spawners outside this
// package (agent manager, MCP mutation tools) that use Setpgid:true and
// need their groups reaped on ungraceful parent death. NativeProc calls
// the unexported wrapper.
func WritePgidFile(pgid int, cwd string, argv []string) error {
	return writePgidFile(pgid, cwd, argv)
}

// RemovePgidFile drops the tracking file for a pgid after graceful stop.
func RemovePgidFile(pgid int) error {
	return removePgidFile(pgid)
}

func writePgidFile(pgid int, cwd string, argv []string) error {
	path, err := pgidFilePath(pgid)
	if err != nil {
		return err
	}
	// Record the owning CLI's PID so the sweep only reaps groups whose
	// owner is dead. Without this, a second `codefly run` would reap the
	// first one's still-live children.
	content := fmt.Sprintf("pgid=%d\nparent=%d\nstarted=%d\ncwd=%s\ncmd=%s\n",
		pgid, os.Getpid(), time.Now().Unix(), cwd, strings.Join(argv, " "))
	return os.WriteFile(path, []byte(content), 0o644)
}

func removePgidFile(pgid int) error {
	path, err := pgidFilePath(pgid)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// isProcessGroupAlive probes whether any process still belongs to pgid.
// kill(-pgid, 0) returns ESRCH when the group is empty, EPERM if we lack
// permission (still indicates presence), nil if alive.
func isProcessGroupAlive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

func waitForGroupDeath(pgid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessGroupAlive(pgid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type pgidRecord struct {
	pgid   int
	parent int // PID of the CLI that spawned the group; 0 if the file predates parent tracking
}

func parsePgidRecord(path string) (pgidRecord, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pgidRecord{}, false
	}
	var rec pgidRecord
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "pgid="); ok {
			pgid, err := strconv.Atoi(rest)
			if err != nil || pgid <= 1 {
				return pgidRecord{}, false
			}
			rec.pgid = pgid
			continue
		}
		if rest, ok := strings.CutPrefix(line, "parent="); ok {
			if p, err := strconv.Atoi(rest); err == nil && p > 0 {
				rec.parent = p
			}
		}
	}
	if rec.pgid == 0 {
		return pgidRecord{}, false
	}
	return rec, true
}

// isProcessAlive tests a single PID via Signal(0). Same pattern as
// daemon.IsRunning but scoped here to keep base self-contained.
func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// ReapStaleProcessGroups scans ~/.codefly/runs/*.pgid and tree-kills any
// process group that is still alive — these are orphans left behind by a
// prior ungraceful CLI exit (SIGKILL of the parent, terminal force-closed,
// etc). The pgid file is removed either way. Best-effort: a single failed
// reap does not short-circuit the sweep.
//
// Call this once at the top of `codefly run service`, before any new
// children are spawned. Idempotent and safe when no files exist.
//
// Convergence: when a parent agent and its NativeProc grandchild both have
// files, directory order can cause the grandchild to be visited while its
// parent is still live (so we skip it), then the parent gets reaped a few
// iterations later. We loop up to maxSweepPasses so a single call resolves
// the whole orphan tree without relying on a subsequent `codefly run`.
func ReapStaleProcessGroups(ctx context.Context) error {
	const maxSweepPasses = 4
	w := wool.Get(ctx).In("base.ReapStaleProcessGroups")
	dir, err := pgidStateDir()
	if err != nil {
		return err
	}
	totalReaped := 0
	for range maxSweepPasses {
		reaped, err := sweepOnce(ctx, dir)
		if err != nil {
			return err
		}
		totalReaped += reaped
		if reaped == 0 {
			break
		}
	}
	if totalReaped > 0 {
		w.Info("reaped stale process groups", wool.Field("count", totalReaped))
	}
	return nil
}

func sweepOnce(ctx context.Context, dir string) (int, error) {
	w := wool.Get(ctx).In("base.sweepOnce")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("cannot read pgid dir: %w", err)
	}

	const sigtermGrace = 3 * time.Second
	const sigkillGrace = time.Second
	reaped := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pgid") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		rec, ok := parsePgidRecord(path)
		if !ok {
			_ = os.Remove(path)
			continue
		}

		// Skip groups still owned by a live CLI (concurrent `codefly run`
		// or live MCP detach). Files without a parent field predate the
		// tracking change — treat those as orphans since they can only
		// exist if the writer crashed before the upgrade anyway.
		if rec.parent > 0 && isProcessAlive(rec.parent) {
			continue
		}

		if !isProcessGroupAlive(rec.pgid) {
			_ = os.Remove(path)
			continue
		}

		w.Warn("reaping orphaned process group from prior run",
			wool.Field("pgid", rec.pgid),
			wool.Field("parent", rec.parent),
			wool.Field("file", entry.Name()))
		_ = syscall.Kill(-rec.pgid, syscall.SIGTERM)
		waitForGroupDeath(rec.pgid, sigtermGrace)
		if isProcessGroupAlive(rec.pgid) {
			_ = syscall.Kill(-rec.pgid, syscall.SIGKILL)
			waitForGroupDeath(rec.pgid, sigkillGrace)
		}
		_ = os.Remove(path)
		reaped++
	}
	return reaped, nil
}
