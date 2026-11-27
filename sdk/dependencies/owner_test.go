package dependencies

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/codefly-dev/core/runners/runstate"
)

// recordSessionOwner starts a real control session and reads the owner
// declaration the child saw. A failed flow makes the SDK reap the child.
func recordSessionOwner(t *testing.T, binary string, opts ...OptionFunc) string {
	t.Helper()
	enterSessionWorkspace(t)
	records := t.TempDir()
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", records)
	t.Setenv("FAKE_CONTROL_MODE", "failed-dependency")

	_, err := WithDependencies(t.Context(), opts...)
	var failed *ReadinessFailure
	if !errors.As(err, &failed) {
		t.Fatalf("WithDependencies() error = %v, want an explicit readiness failure", err)
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
	record := recordSessionOwner(t, buildControlServer(t))
	if want := "owner=" + self.String() + "\n"; !strings.Contains(record, want) {
		t.Fatalf("the run was started with:\n%s\nwant it told %q", record, want)
	}
}

// A stack kept running is meant to outlive the process that started it, and a
// later process attaches to it: it is nobody's.
func TestAStackKeptRunningHasNoOwner(t *testing.T) {
	// Build before isolating runtime HOME so Go can use its normal caches.
	binary := buildControlServer(t)
	t.Setenv("HOME", warmCacheHome(t))
	record := recordSessionOwner(t, binary, WithKeepRunning())
	if !strings.Contains(record, "owner=\n") {
		t.Fatalf("the run was started with:\n%s\nwant no owner", record)
	}
}

// An inherited declaration names somebody else's run. A stack kept running
// must not end with that process either.
func TestAStackKeptRunningDoesNotInheritAnOwner(t *testing.T) {
	// Build before isolating runtime HOME so Go can use its normal caches.
	binary := buildControlServer(t)
	t.Setenv("HOME", warmCacheHome(t))
	t.Setenv(runstate.OwnerEnvironment, "4242:1:boot")
	record := recordSessionOwner(t, binary, WithKeepRunning())
	if !strings.Contains(record, "owner=\n") {
		t.Fatalf("the run was started with:\n%s\nwant no owner", record)
	}
}
