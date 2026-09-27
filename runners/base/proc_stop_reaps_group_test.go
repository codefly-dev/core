package base

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A service process is rarely one process. `npm run dev` forks a shell, which
// forks node, which forks the server that holds the port. Stop must end that
// whole tree: a descendant that outlives it is reparented to init, keeps its
// listener, and the next run of the same service fails with EADDRINUSE.
//
// The tree below is the shape that leaked. The leader dies promptly on SIGTERM,
// as npm does; its child ignores SIGTERM, as a server draining connections
// effectively does, and has a child of its own. Liveness therefore has to be
// observed on the group, never on the leader: a Stop that returns once the
// leader exits leaves the rest running.
//
// Two variants, because the leak had two routes. When the descendant has let
// go of the leader's stdout and stderr, the leader's exit is all the runner
// waits for. When it still holds them, the runner's forwarders never reach EOF,
// the leader stays an unreaped zombie on the group id, and the escalation has to
// authenticate a group whose leader can no longer be inspected.
func resistantTreeScript(pidFile string, detachStdio bool) string {
	descendant := `sh -c 'trap "" TERM; echo $$ > "$0"; while :; do sleep 1; done' ` + strconvQuote(pidFile)
	if detachStdio {
		descendant += ` </dev/null >/dev/null 2>&1`
	}
	return descendant + " &\nexec sleep 300\n"
}

func strconvQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func waitForPidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant never announced itself in %s", path)
	return 0
}

// assertTreeGone fails when anything of the group, or the named descendant,
// is still alive once Stop has returned. Stop is the synchronous boundary: a
// caller that proceeds to exit or to start the next run must not race a
// survivor, so this does not poll.
func assertTreeGone(t *testing.T, pgid, descendant int) {
	t.Helper()
	if IsProcessAlive(descendant) {
		t.Errorf("descendant %d survived Stop", descendant)
	}
	if isProcessGroupAlive(pgid) {
		t.Errorf("process group %d still has members after Stop", pgid)
	}
}

// reapTreeOnCleanup SIGKILLs whatever a broken Stop left, so a failing run of
// this test never strands a process on the machine. Signalling the bare pgid is
// safe here in a way it is not in production: the group was started moments ago
// by this test, and recycling the pgid would need a full pid-space wrap.
func reapTreeOnCleanup(t *testing.T, pgid func() int, descendant *int) {
	t.Cleanup(func() {
		if pid := pgid(); pid > 1 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		if *descendant > 1 {
			_ = syscall.Kill(*descendant, syscall.SIGKILL)
		}
	})
}

func TestNativeProcStopReapsTheWholeProcessGroup(t *testing.T) {
	for _, variant := range []struct {
		name        string
		detachStdio bool
	}{
		{name: "descendant released stdio", detachStdio: true},
		{name: "descendant holds stdio", detachStdio: false},
	} {
		t.Run(variant.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx := context.Background()
			env, err := NewNativeEnvironment(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := env.Init(ctx); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(t.TempDir(), "descendant.pid")
			proc, err := env.NewProcess("sh", "-c", resistantTreeScript(pidFile, variant.detachStdio))
			if err != nil {
				t.Fatal(err)
			}
			native := proc.(*NativeProc)
			native.WithOutput(&strings.Builder{})
			descendant := 0
			reapTreeOnCleanup(t, func() int {
				if native.exec == nil || native.exec.Process == nil {
					return 0
				}
				return native.exec.Process.Pid
			}, &descendant)

			if err := proc.Start(ctx); err != nil {
				t.Fatal(err)
			}
			descendant = waitForPidFile(t, pidFile)
			pgid := native.exec.Process.Pid

			stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := proc.Stop(stopCtx); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			assertTreeGone(t, pgid, descendant)
		})
	}
}

func TestNixProcStopReapsTheWholeProcessGroup(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		name        string
		detachStdio bool
	}{
		{name: "descendant released stdio", detachStdio: true},
		{name: "descendant holds stdio", detachStdio: false},
	} {
		t.Run(variant.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx := context.Background()
			env := &NixEnvironment{
				dir:          t.TempDir(),
				materialized: map[string]string{"PATH": filepath.Dir(shell) + ":/bin:/usr/bin"},
			}
			pidFile := filepath.Join(t.TempDir(), "descendant.pid")
			proc, err := env.NewProcess("sh", "-c", resistantTreeScript(pidFile, variant.detachStdio))
			if err != nil {
				t.Fatal(err)
			}
			nix := proc.(*NixProc)
			nix.WithOutput(&strings.Builder{})
			descendant := 0
			reapTreeOnCleanup(t, func() int {
				if nix.exec == nil || nix.exec.Process == nil {
					return 0
				}
				return nix.exec.Process.Pid
			}, &descendant)

			if err := proc.Start(ctx); err != nil {
				t.Fatal(err)
			}
			descendant = waitForPidFile(t, pidFile)
			pgid := nix.exec.Process.Pid

			stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := proc.Stop(stopCtx); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			assertTreeGone(t, pgid, descendant)
		})
	}
}
