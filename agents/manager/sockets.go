package manager

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// agentSocketDir is where per-spawn UDS sockets live (see loader.go). Each
// socket is named agent-<owner-pid>-<nano>.sock, where owner-pid is the codefly
// process that spawned the agent.
func agentSocketDir() string {
	return filepath.Join(os.TempDir(), "codefly-uds")
}

var socketSweepOnce sync.Once

// sweepStaleAgentSocketsOnce runs SweepStaleAgentSockets at most once per
// process — the "future cleanup hook" the loader left a TODO for. Called lazily
// on the first agent Load so a codefly invocation tidies sockets left behind by
// previously-crashed CLIs without anyone having to remember to.
func sweepStaleAgentSocketsOnce() {
	socketSweepOnce.Do(func() { _ = SweepStaleAgentSockets() })
}

// SweepStaleAgentSockets removes UDS socket files whose owning codefly process
// (encoded in the filename) is no longer alive. Sockets owned by a live process
// — including this one — are left untouched. Returns the number removed.
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
		if e.IsDir() || !strings.HasSuffix(name, ".sock") || !strings.HasPrefix(name, "agent-") {
			continue
		}
		// agent-<pid>-<nano>.sock
		core := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".sock")
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
		if os.Remove(filepath.Join(dir, name)) == nil {
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
		if e.IsDir() || !strings.HasSuffix(name, ".sock") || !strings.HasPrefix(name, "agent-") {
			continue
		}
		core := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".sock")
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
