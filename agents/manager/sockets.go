package manager

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// agentSocketDir is the parent of private per-spawn UDS directories (see
// loader.go). Each directory is named codefly-uds-<owner-pid>-<random>, where
// owner-pid is the codefly process that spawned the agent.
func agentSocketDir() string {
	return os.TempDir()
}

var socketSweepOnce sync.Once

// sweepStaleAgentSocketsOnce runs SweepStaleAgentSockets at most once per
// process — the "future cleanup hook" the loader left a TODO for. Called lazily
// on the first agent Load so a codefly invocation tidies sockets left behind by
// previously-crashed CLIs without anyone having to remember to.
func sweepStaleAgentSocketsOnce() {
	socketSweepOnce.Do(func() { _ = SweepStaleAgentSockets() })
}

// SweepStaleAgentSockets removes private UDS directories whose owning codefly
// process (encoded in the directory name) is no longer alive. Directories owned
// by a live process — including this one — are left untouched. Returns the
// number removed.
// Best-effort: filesystem/parse errors are skipped, never fatal.
func SweepStaleAgentSockets() int {
	dir := agentSocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	self := os.Getpid()
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "codefly-uds-") {
			continue
		}
		// codefly-uds-<pid>-<random>
		core := strings.TrimPrefix(name, "codefly-uds-")
		dash := strings.IndexByte(core, '-')
		if dash <= 0 {
			continue
		}
		pid, convErr := strconv.Atoi(core[:dash])
		if convErr != nil {
			continue
		}
		if pid == self || pidAlive(pid) {
			continue // owner still running — its socket may be in use
		}
		if os.RemoveAll(filepath.Join(dir, name)) == nil {
			removed++
		}
	}
	return removed
}

// CountStaleAgentSockets returns how many sockets would be swept, without
// removing anything (used by `codefly doctor`).
func CountStaleAgentSockets() int {
	dir := agentSocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	self := os.Getpid()
	count := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "codefly-uds-") {
			continue
		}
		core := strings.TrimPrefix(name, "codefly-uds-")
		dash := strings.IndexByte(core, '-')
		if dash <= 0 {
			continue
		}
		pid, convErr := strconv.Atoi(core[:dash])
		if convErr != nil {
			continue
		}
		if pid != self && !pidAlive(pid) {
			count++
		}
	}
	return count
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
