package base_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak after the runners tests. Catches process-reaper,
// log-forwarder, and docker-client goroutines that aren't joined on test
// teardown.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// docker sdk's connection-close goroutine can outlive individual
		// tests when the shared client is reused — accept it until we
		// wire per-test client lifecycles.
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		// Docker client uses net/http keep-alive. Its persistConn
		// read/write loops stay parked on the idle connection until the
		// transport's IdleConnTimeout fires (default 90s) — well after
		// the test returns. Nothing leaking, just async teardown.
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		// nix-materialization tests spawn long-running nix-build
		// subprocesses; their cmd.Wait goroutine lingers briefly after
		// test exit in rare cases.
		goleak.IgnoreTopFunction("os/exec.(*Cmd).Wait"),
	)
}
