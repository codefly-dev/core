package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// orphanedStore is a store agent that was launched, loaded and initialized by
// a run that was then killed: nothing told the agent, and nothing ever will.
type orphanedStore struct {
	dir    string
	agent  int
	server int
}

func running(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func requireEnds(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for running(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("%s (process %d) still runs after the run that launched the agent was killed", what, pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func killRunOfAStoreAgent(t *testing.T, environment ...string) orphanedStore {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "storeagent")
	build := exec.Command("go", "build", "-o", binary, "./testdata/storeagent")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build store agent fixture: %v\n%s", err, output)
	}
	store := orphanedStore{dir: filepath.Join(t.TempDir(), "redis", "run")}
	if err := os.MkdirAll(filepath.Join(store.dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report")

	const token = "test-token"
	// The run: a process that launches the agent and waits for it, as the CLI
	// does. The agent's handshake still reaches this test.
	run := exec.Command("sh", "-c", `"$0" & wait`, binary)
	run.Env = append(os.Environ(), "CODEFLY_AGENT_TOKEN="+token, "CODEFLY_AGENT_UDS_PATH=",
		"STOREAGENT_DIR="+store.dir, "STOREAGENT_REPORT="+report)
	run.Env = append(run.Env, environment...)
	stdout, err := run.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The agent's stderr is a pipe the run reads, as the CLI reads it: once
	// the run is killed nobody reads it any more, and an agent that writes
	// there is not to die of it before it has stopped anything.
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	run.Stderr = write
	stderr := &lockedBuffer{}
	go func() { _, _ = io.Copy(stderr, read) }()
	agentLog := stderr.String
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	_ = write.Close()
	t.Cleanup(func() {
		_ = run.Process.Kill()
		_ = run.Wait()
		for _, pid := range []int{store.agent, store.server} {
			if pid > 1 && running(pid) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	endpoint := readHandshakeEndpoint(t, stdout, stderr)
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", endpoint, err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, AuthMetadataKey, token)
	client := runtimev0.NewRuntimeClient(conn)
	if _, err := client.Load(ctx, &runtimev0.LoadRequest{}); err != nil {
		t.Fatalf("Load: %v\nagent stderr:\n%s", err, agentLog())
	}
	if _, err := client.Init(ctx, &runtimev0.InitRequest{}); err != nil {
		t.Fatalf("Init: %v\nagent stderr:\n%s", err, agentLog())
	}
	reported, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscan(string(reported), &store.agent, &store.server); err != nil {
		t.Fatalf("read the agent's report %q: %v", reported, err)
	}
	if !running(store.agent) || !running(store.server) {
		t.Fatalf("the agent %d and its server %d must run before the run is killed", store.agent, store.server)
	}

	// kill -9 of the run: it stops nothing and tells nobody.
	if err := run.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = run.Wait()
	_ = read.Close()
	return store
}

func requireDirectory(t *testing.T, dir string, kept bool) {
	t.Helper()
	_, err := os.Stat(dir)
	if kept && err != nil {
		t.Fatalf("the state directory %s must be kept: %v", dir, err)
	}
	if !kept && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the state directory %s of the killed disposable run was left (%v)", dir, err)
	}
}

// A killed run signals nobody. Its agent notices that the run is gone and
// stops in order, as it does when it is asked to: the server it started
// stops, the agent exits, and the run's disposable state goes with it.
func TestServeStopsAnAgentWhoseRunWasKilled(t *testing.T) {
	store := killRunOfAStoreAgent(t, "STOREAGENT_DISPOSABLE=true")
	requireEnds(t, store.server, "the server the agent started")
	requireEnds(t, store.agent, "the agent")
	requireDirectory(t, store.dir, false)
}

// A developer's run is stopped in order too, and its data is kept: the agent
// stops its server the way a stop of the run does, and removes nothing.
func TestServeKeepsTheDataOfAPersistentRunThatWasKilled(t *testing.T) {
	store := killRunOfAStoreAgent(t)
	requireEnds(t, store.server, "the server the agent started")
	requireEnds(t, store.agent, "the agent")
	requireDirectory(t, store.dir, true)
}

// What the agent's own stop leaves running in a disposable directory is
// nobody's once the run is gone, and is stopped through the directory's
// records.
func TestServeStopsWhatAnAgentsStopLeftOfAKilledDisposableRun(t *testing.T) {
	store := killRunOfAStoreAgent(t, "STOREAGENT_DISPOSABLE=true", "STOREAGENT_STOP_KEEPS_SERVER=true")
	requireEnds(t, store.server, "the server the agent's stop left running")
	requireEnds(t, store.agent, "the agent")
	requireDirectory(t, store.dir, false)
}

// The same server in a developer's directory is not the agent's to end: no
// right to it was ever stated, and the same run may come back to it.
func TestServeLeavesWhatAnAgentsStopLeftOfAPersistentRun(t *testing.T) {
	store := killRunOfAStoreAgent(t, "STOREAGENT_STOP_KEEPS_SERVER=true")
	requireEnds(t, store.agent, "the agent")
	if !running(store.server) {
		t.Fatalf("the server %d of a persistent run was stopped by an agent whose own stop keeps it", store.server)
	}
	requireDirectory(t, store.dir, true)
}
