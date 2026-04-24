// Package leakcheck provides test-time helpers for detecting resource
// leaks: file descriptors, docker containers, and (via go.uber.org/goleak
// wired at the TestMain level) goroutines.
//
// Usage in a test:
//
//	func TestSomething(t *testing.T) {
//	    defer leakcheck.FDCheck(t)()
//	    // test body
//	}
//
// The returned closure records the baseline and, when deferred, compares
// to the current count. A small slack (2 by default) accommodates buffered
// log writes and is configurable via FDCheckN.
//
// DockerContainerCheck compares the count of containers matching a label
// filter before/after the test, catching runaway container leaks from
// docker-based runners.
//
// All functions are safe no-ops on platforms where the underlying probe
// is unavailable; they log a diagnostic rather than failing the test.
package leakcheck

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// FDCheck returns a deferred-style checker that records the current FD
// count and, on invocation, fails the test if more than 2 additional FDs
// are open. Equivalent to FDCheckN(t, 2).
func FDCheck(t testing.TB) func() {
	return FDCheckN(t, 2)
}

// FDCheckN is FDCheck with a configurable slack.
func FDCheckN(t testing.TB, slack int) func() {
	t.Helper()
	before, ok := countOpenFDs()
	if !ok {
		t.Logf("leakcheck: FD probe unsupported on %s — skipping", runtime.GOOS)
		return func() {}
	}
	return func() {
		after, _ := countOpenFDs()
		delta := after - before
		if delta > slack {
			t.Errorf("leakcheck: %d FDs leaked (before=%d after=%d slack=%d)",
				delta, before, after, slack)
		}
	}
}

// countOpenFDs returns the current process's FD count. Returns (0, false)
// if unsupported on the platform. Uses /proc/self/fd on linux, lsof on
// darwin (slower but accurate); windows is unsupported.
func countOpenFDs() (int, bool) {
	switch runtime.GOOS {
	case "linux":
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			return 0, false
		}
		return len(entries), true
	case "darwin":
		cmd := exec.Command("lsof", "-p", itoa(os.Getpid()))
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return 0, false
		}
		// Header line + one line per FD.
		return bytes.Count(out.Bytes(), []byte{'\n'}) - 1, true
	default:
		return 0, false
	}
}

// DockerContainerCheck returns a deferred checker that verifies no new
// containers matching filter are left running by the test. filter is
// a docker ps --filter expression, e.g. "label=test-codefly=true" or
// "name=codefly-test-".
//
// If docker is unavailable, the check is skipped with a log line.
func DockerContainerCheck(t testing.TB, filter string) func() {
	t.Helper()
	before, ok := countContainers(filter)
	if !ok {
		t.Logf("leakcheck: docker unavailable — skipping container check")
		return func() {}
	}
	return func() {
		after, _ := countContainers(filter)
		if after > before {
			t.Errorf("leakcheck: %d containers leaked matching %q (before=%d after=%d)",
				after-before, filter, before, after)
		}
	}
}

// countContainers returns the count of containers matching filter.
// Returns (0, false) if docker is not available.
func countContainers(filter string) (int, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", filter, "-q")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, false
	}
	if out.Len() == 0 {
		return 0, true
	}
	return bytes.Count(out.Bytes(), []byte{'\n'}), true
}

// itoa is a small helper — avoids importing strconv just for this.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
