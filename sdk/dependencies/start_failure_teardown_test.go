package dependencies

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// slowStopCLI writes a stand-in `codefly` that never becomes ready — it binds
// no control socket and serves nothing — and whose ordered stop takes longer
// than the grace a group teardown gives: on SIGTERM it works for about five
// seconds, then records that it finished. It is the shape of the real CLI
// stopping a composition's services one by one and taking back what they left.
// The teardown is a shell loop, so a SIGTERM that also reaches its `sleep`
// does not shorten it.
func slowStopCLI(t *testing.T) (pidFile, finished string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "pid")
	finished = filepath.Join(dir, "teardown-finished")
	script := "#!/bin/sh\n" +
		"trap 'i=0; while [ $i -lt 50 ]; do sleep 0.1; i=$((i+1)); done; echo done > \"" + finished + "\"; exit 0' TERM\n" +
		"echo $$ > \"" + pidFile + "\"\n" +
		"while :; do sleep 0.2; done\n"
	binary := filepath.Join(dir, "codefly")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEFLY_BINARY", binary)
	return pidFile, finished
}

// TestAStartThatTimesOutLetsTheCLIFinishItsOrderedStop: a start that times out
// is torn down by the CLI the SDK started, in order and to its end. The group
// teardown's three-second grace is for processes, and a CLI given only that
// was killed with services half stopped and their containers still up.
func TestAStartThatTimesOutLetsTheCLIFinishItsOrderedStop(t *testing.T) {
	pidFile, finished := slowStopCLI(t)

	_, err := WithDependencies(context.Background(), WithTimeout(time.Second))
	if err == nil || !strings.Contains(err.Error(), "did not bind the isolated control socket") {
		t.Fatalf("the start did not time out on the control socket: %v", err)
	}
	if _, statErr := os.Stat(finished); statErr != nil {
		t.Fatalf("the CLI was killed before its ordered stop finished: %v", statErr)
	}
	leader := readOnePID(t, pidFile)
	if !waitFor(3*time.Second, func() bool { return !pidAlive(leader) }) {
		t.Fatalf("the CLI %d outlived the failed start", leader)
	}
}

// TestACancelledStartLetsTheCLIFinishItsOrderedStop: cancelling the start asks
// the CLI to stop the same way, where it used to kill it outright.
func TestACancelledStartLetsTheCLIFinishItsOrderedStop(t *testing.T) {
	pidFile, finished := slowStopCLI(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := WithDependencies(ctx, WithTimeout(time.Minute))
		result <- err
	}()
	leader := readOnePID(t, pidFile)
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("a cancelled start reported success")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("WithDependencies did not return after cancellation")
	}
	if _, statErr := os.Stat(finished); statErr != nil {
		t.Fatalf("the CLI was killed before its ordered stop finished: %v", statErr)
	}
	if !waitFor(3*time.Second, func() bool { return !pidAlive(leader) }) {
		t.Fatalf("the CLI %d outlived the cancelled start", leader)
	}
}

// TestAnOrderedStopIsBounded: a CLI that does not stop when asked is killed
// with its group once the budget is spent, so a failed start always returns.
func TestAnOrderedStopIsBounded(t *testing.T) {
	mp, err := startManaged(context.Background(), exec.Command("sh", "-c", `trap "" TERM; echo $$; while :; do sleep 1; done`))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}
	reapTestGroup(t, mp)
	leader := readPID(t, mp)

	started := time.Now()
	if err := mp.stopInOrder(300 * time.Millisecond); err != nil {
		t.Fatalf("stopInOrder: %v", err)
	}
	if elapsed := time.Since(started); elapsed > killDeadline {
		t.Errorf("stopInOrder took %s, want under %s", elapsed, killDeadline)
	}
	if pidAlive(leader) {
		t.Errorf("leader %d survived an ordered stop it ignored", leader)
	}
}

func readOnePID(t *testing.T, path string) int {
	t.Helper()
	pid := 0
	if !waitFor(10*time.Second, func() bool {
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil && pid > 0
	}) {
		t.Fatalf("the stand-in CLI never recorded its PID in %s", path)
	}
	return pid
}

// stoppingCLI is the control client of a CLI that is already stopping: its
// control server is gone, so Stop and Destroy are answered by nobody.
type stoppingCLI struct{ v0.CLIClient }

func (stoppingCLI) StopFlow(context.Context, *v0.StopFlowRequest, ...grpc.CallOption) (*v0.StopFlowResponse, error) {
	return nil, status.Error(codes.Unavailable, "error reading from server: EOF")
}

func (stoppingCLI) DestroyFlow(context.Context, *v0.DestroyFlowRequest, ...grpc.CallOption) (*v0.DestroyFlowResponse, error) {
	return nil, status.Error(codes.Unavailable, "error reading from server: EOF")
}

// TestATeardownTheCLINoLongerAnswersLetsItFinishItsOrderedStop: a session
// started under a context that ends before its cleanup runs (a test's
// t.Context, cancelled just before its Cleanup functions) has a CLI that was
// asked to stop when the context ended and is in the middle of stopping its
// services when Destroy arrives. Destroy is answered Unavailable, and the
// group used to be killed three seconds later, with the composition's
// containers, their networks and its detached servers left behind. The CLI is
// given the time its ordered stop takes instead, as after a failed start.
func TestATeardownTheCLINoLongerAnswersLetsItFinishItsOrderedStop(t *testing.T) {
	for name, teardown := range map[string]func(*Dependencies, context.Context) error{
		"Destroy": (*Dependencies).Destroy,
		"Stop":    (*Dependencies).Stop,
	} {
		t.Run(name, func(t *testing.T) {
			pidFile, finished := slowStopCLI(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mp, err := startManaged(ctx, exec.CommandContext(ctx, os.Getenv("CODEFLY_BINARY")))
			if err != nil {
				t.Fatalf("startManaged: %v", err)
			}
			reapTestGroup(t, mp)
			leader := readOnePID(t, pidFile)
			session := &Dependencies{proc: mp, cli: stoppingCLI{}}

			// The context the session was started under ends, then its cleanup runs.
			cancel()
			if err := teardown(session, context.Background()); status.Code(err) != codes.Unavailable {
				t.Fatalf("%s answered %v, want the CLI's Unavailable reported", name, err)
			}
			if _, statErr := os.Stat(finished); statErr != nil {
				t.Fatalf("the CLI was killed before its ordered stop finished: %v", statErr)
			}
			if !waitFor(3*time.Second, func() bool { return !pidAlive(leader) }) {
				t.Fatalf("the CLI %d outlived %s", leader, name)
			}
		})
	}
}
