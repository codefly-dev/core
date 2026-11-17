package dependencies

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/runners/runstate"
)

// startStalledSession starts a dependency session against a control server
// whose flow never becomes ready, and returns what that server recorded: the
// session fails, and the server has by then said what it was started with.
func startStalledSession(t *testing.T, opts ...OptionFunc) string {
	t.Helper()
	binary := buildControlServer(t)
	enterSessionWorkspace(t)
	records := t.TempDir()
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", records)
	t.Setenv("FAKE_CONTROL_MODE", "stalled-dependency")

	if _, err := WithDependencies(context.Background(), append(opts, WithTimeout(2*time.Second))...); err == nil {
		t.Fatal("WithDependencies() accepted a flow that never became ready")
	}
	return readRecord(t, onlyRecord(t, records))
}

// A dependency stack is started for the process that asked for it. The run is
// told whose it is, by birth, so that it can stop when that process is gone
// without having been stopped: a test binary that timed out or was killed
// calls nothing.
func TestADependencyRunIsToldWhoOwnsIt(t *testing.T) {
	self, err := runstate.BirthOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	record := startStalledSession(t)
	if want := "owner=" + self.String() + "\n"; !strings.Contains(record, want) {
		t.Fatalf("the run was started with:\n%s\nwant it told %q", record, want)
	}
}

// A stack kept running is meant to outlive the process that started it, and a
// later process attaches to it: it is nobody's.
func TestAStackKeptRunningHasNoOwner(t *testing.T) {
	t.Setenv("HOME", warmCacheHome(t))
	record := startStalledSession(t, WithKeepRunning())
	if !strings.Contains(record, "owner=\n") {
		t.Fatalf("the run was started with:\n%s\nwant no owner", record)
	}
}

// An inherited declaration names somebody else's run. A stack kept running
// must not end with that process either.
func TestAStackKeptRunningDoesNotInheritAnOwner(t *testing.T) {
	t.Setenv("HOME", warmCacheHome(t))
	t.Setenv(runstate.OwnerEnvironment, "4242:1:boot")
	record := startStalledSession(t, WithKeepRunning())
	if !strings.Contains(record, "owner=\n") {
		t.Fatalf("the run was started with:\n%s\nwant no owner", record)
	}
}
