package sdk

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWithDependencies_KillsProcessGroupOnReadyTimeout verifies that when a
// post-spawn error path is hit (here: the CLI server never becomes gRPC-ready),
// WithDependencies tears down the entire spawned process group instead of
// leaking it. A stand-in "codefly" binary backgrounds a child, records both
// PIDs, then blocks forever without ever serving gRPC.
func TestWithDependencies_KillsProcessGroupOnReadyTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	binPath := filepath.Join(dir, "codefly")

	// $$ (before exec) is the group leader; it keeps the same PID after exec
	// replaces the shell with sleep. $! is the backgrounded child in the group.
	script := "#!/bin/sh\n" +
		"sleep 60 &\n" +
		"echo \"$$ $!\" > \"" + pidFile + "\"\n" +
		"exec sleep 60\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("CODEFLY_BINARY", binPath)

	_, err := WithDependencies(context.Background(), WithTimeout(2*time.Second))
	if err == nil {
		t.Fatal("expected WithDependencies to fail (fake CLI never becomes ready)")
	}

	leaderPID, childPID := readPIDFile(t, pidFile)

	if !waitFor(3*time.Second, func() bool { return !pidAlive(leaderPID) }) {
		t.Errorf("group leader %d still alive after WithDependencies error — process group leaked", leaderPID)
	}
	if !waitFor(3*time.Second, func() bool { return !pidAlive(childPID) }) {
		t.Errorf("child %d still alive after WithDependencies error — process group leaked", childPID)
	}
}

// readPIDFile waits for the fake binary to record its two PIDs, then parses them.
func readPIDFile(t *testing.T, path string) (int, int) {
	t.Helper()
	var content []byte
	if !waitFor(2*time.Second, func() bool {
		b, err := os.ReadFile(path)
		if err != nil || len(strings.Fields(string(b))) < 2 {
			return false
		}
		content = b
		return true
	}) {
		t.Fatalf("fake binary never recorded PIDs to %s", path)
	}
	fields := strings.Fields(string(content))
	leader, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("parse leader PID %q: %v", fields[0], err)
	}
	child, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("parse child PID %q: %v", fields[1], err)
	}
	return leader, child
}
