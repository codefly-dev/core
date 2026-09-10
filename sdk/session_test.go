package sdk

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests drive the ordinary WithDependencies spawn path against a real
// Codefly control server (sdk/internal/testcli) over a real socket: the session
// resolves its identity, calls the CLI contract, and publishes an endpoint the
// test then connects to.

var (
	testCLIOnce sync.Once
	testCLIDir  string
	testCLIPath string
	testCLIErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if testCLIDir != "" {
		_ = os.RemoveAll(testCLIDir)
	}
	os.Exit(code)
}

func testCLI(t *testing.T) string {
	t.Helper()
	testCLIOnce.Do(func() {
		testCLIDir, testCLIErr = os.MkdirTemp("", "codefly-testcli-*")
		if testCLIErr != nil {
			return
		}
		testCLIPath = filepath.Join(testCLIDir, "codefly")
		build := exec.Command("go", "build", "-o", testCLIPath, "./testdata/testcli")
		// Pin the build directory: a test that has changed the working
		// directory must not decide where this package is compiled from.
		build.Dir = packageDir
		if output, err := build.CombinedOutput(); err != nil {
			testCLIErr = fmt.Errorf("build test CLI: %w: %s", err, output)
		}
	})
	if testCLIErr != nil {
		t.Fatalf("test CLI unavailable: %v", testCLIErr)
	}
	return testCLIPath
}

// uniqueScope keeps a test run off every other run's control port.
// network.CLIServerPort hashes the workspace name, so without a per-run scope
// two concurrent `go test` invocations — two checkouts on one machine, or a CI
// matrix on one runner — resolve the same address, and the second SDK drives
// the first run's server instead of its own.
func uniqueScope(t *testing.T) string {
	t.Helper()
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("generate scope: %v", err)
	}
	return "t" + hex.EncodeToString(buf[:])
}

// freeSharedScope picks a naming scope whose shared control port is actually
// free right now. The shared channel's port is hashed from the workspace name,
// so it is machine-global: on a host running several checkouts, a scope chosen
// blindly can land on a port something else already holds, and the child would
// fail to bind for a reason that has nothing to do with what the test asserts.
func freeSharedScope(t *testing.T, dir string) string {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		scope := uniqueScope(t)
		listener, err := net.Listen("tcp", cliServerAddress(context.Background(), dir, scope))
		if err != nil {
			continue
		}
		if err := listener.Close(); err != nil {
			t.Fatalf("release probed control port: %v", err)
		}
		return scope
	}
	t.Fatal("no free shared control port after 20 attempts")
	return ""
}

// markerBinary is a stand-in codefly that records the fact that it ran. A test
// asserts the marker is absent to prove a session was refused before it
// provisioned anything.
func markerBinary(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "codefly")
	marker := filepath.Join(dir, "spawned")
	script := "#!/bin/sh\ntouch \"" + marker + "\"\nexit 23\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write marker binary: %v", err)
	}
	return binary, marker
}

func endpointKey(module, service string) string {
	return fmt.Sprintf("CODEFLY__ENDPOINT__%s__%s__TCP__TCP", strings.ToUpper(module), strings.ToUpper(service))
}

// dependencyIdentity connects to a resolved endpoint address and reads back the
// identity of the session that published it.
func dependencyIdentity(t *testing.T, address string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial dependency at %s: %v", address, err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read from dependency at %s: %v", address, err)
	}
	return strings.TrimSpace(line)
}

// Two sessions run at the same time, each anchored to its own service, and each
// one's endpoint reaches its own dependency. Neither writes to os.Environ.
func TestConcurrentSessionsCarryTheirOwnCommandScopedEndpoints(t *testing.T) {
	binary := testCLI(t)
	sessions := []struct {
		dir        string
		module     string
		service    string
		endpoint   string
		dependency string
	}{
		{fixtureDir(t, "alpha", "modules", "shop", "services", "web"), "shop", "web", endpointKey("shop", "store"), "shop/store"},
		{fixtureDir(t, "beta", "modules", "office", "services", "portal"), "office", "portal", endpointKey("office", "vault"), "office/vault"},
	}

	type result struct {
		deps *Dependencies
		err  error
	}
	results := make([]result, len(sessions))
	var wait sync.WaitGroup
	for i, session := range sessions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			deps, err := WithDependencies(context.Background(),
				WithDirectory(session.dir),
				WithCommandScopedEnvironment(),
				WithCodeflyBinary(binary),
				WithTimeout(60*time.Second))
			results[i] = result{deps: deps, err: err}
		}()
	}
	wait.Wait()

	for i, session := range sessions {
		if results[i].err != nil {
			t.Fatalf("session %s: WithDependencies() error = %v", session.service, results[i].err)
		}
		deps := results[i].deps
		defer func() { _ = deps.Destroy(context.Background()) }()

		svc, err := deps.Service(context.Background())
		if err != nil || svc.Name != session.service {
			t.Fatalf("session service = %v, %v, want %s", svc, err, session.service)
		}
		address := deps.EnvironmentVariables()[session.endpoint]
		if address == "" {
			t.Fatalf("session %s resolved no %s: %v", session.service, session.endpoint, deps.EnvironmentVariables())
		}
		if identity := dependencyIdentity(t, address); identity != session.module+"/"+session.service {
			t.Fatalf("endpoint %s reached %q, want %s/%s", session.endpoint, identity, session.module, session.service)
		}
		if !slices.Contains(deps.Environ(), session.endpoint+"="+address) {
			t.Fatalf("Environ() does not carry %s for a child command", session.endpoint)
		}
		if got := os.Getenv(session.endpoint); got != "" {
			t.Fatalf("a command-scoped session wrote %s=%q into the process environment", session.endpoint, got)
		}
		// Connection reads the session's own resolved values, so it answers for
		// a command-scoped session that never touched the process environment.
		if got := deps.Connection("configuration/"+session.dependency, "connection"); got != address {
			t.Fatalf("Connection() = %q, want the session's own %q", got, address)
		}
	}
}

// A session that fails while resolving configuration leaves the process
// environment byte-for-byte as it found it — the endpoints it had already
// resolved are not injected on the way out.
func TestConfigurationResolutionFailureLeavesTheEnvironmentUnchanged(t *testing.T) {
	binary := testCLI(t)
	t.Setenv("CODEFLY_TESTCLI_FAIL", "GetDependenciesConfigurations")

	before := os.Environ()
	slices.Sort(before)

	_, err := WithDependencies(context.Background(),
		WithDirectory(fixtureDir(t, "alpha", "modules", "shop", "services", "web")),
		WithCodeflyBinary(binary),
		WithTimeout(60*time.Second))
	if err == nil || !strings.Contains(err.Error(), "dependencies configurations") {
		t.Fatalf("WithDependencies() error = %v, want the configuration failure", err)
	}

	after := os.Environ()
	slices.Sort(after)
	if !slices.Equal(before, after) {
		t.Fatalf("failed resolution changed the process environment:\nbefore %v\nafter  %v", before, after)
	}
}

// Global injection has one owner: a second session is refused before it mutates
// anything, and only takes over once the first session releases on Stop.
func TestGlobalInjectionHasASingleOwnerAcrossSessions(t *testing.T) {
	binary := testCLI(t)
	alpha := fixtureDir(t, "alpha", "modules", "shop", "services", "web")
	beta := fixtureDir(t, "beta", "modules", "office", "services", "portal")
	alphaKey, betaKey := endpointKey("shop", "store"), endpointKey("office", "vault")

	first, err := WithDependencies(context.Background(),
		WithDirectory(alpha), WithCodeflyBinary(binary), WithTimeout(60*time.Second))
	if err != nil {
		t.Fatalf("WithDependencies() error = %v", err)
	}
	// A session that holds the process environment has to be released even if
	// an assertion below fails, or every later test in this package is refused.
	// Destroy after the explicit Stop is a no-op.
	defer func() { _ = first.Destroy(context.Background()) }()
	injected := os.Getenv(alphaKey)
	if injected == "" {
		t.Fatal("the owning session did not inject its endpoint")
	}

	// A refused session must not have provisioned anything first: the stand-in
	// binary records the fact that it ran, and it must not have.
	refusedBinary, spawned := markerBinary(t)
	_, err = WithDependencies(context.Background(),
		WithDirectory(beta), WithCodeflyBinary(refusedBinary), WithTimeout(60*time.Second))
	if err == nil || !strings.Contains(err.Error(), "owns the process environment") {
		t.Fatalf("second session error = %v, want single-owner rejection", err)
	}
	if !strings.Contains(err.Error(), alpha) {
		t.Fatalf("rejection %q does not name the session holding the environment", err)
	}
	if _, statErr := os.Stat(spawned); !os.IsNotExist(statErr) {
		t.Fatalf("the refused session spawned a dependency stack; stat error = %v", statErr)
	}
	if got := os.Getenv(betaKey); got != "" {
		t.Fatalf("the rejected session injected %s=%q", betaKey, got)
	}
	if got := os.Getenv(alphaKey); got != injected {
		t.Fatalf("the owning session's value changed to %q", got)
	}

	if err := first.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, present := os.LookupEnv(alphaKey); present {
		t.Fatalf("%s survived the owning session's release", alphaKey)
	}
}

// The shared control channel is a workspace-hashed port, so two sessions in one
// process can select the same address. The second must be refused rather than
// silently driving the first session's server, and refused before it spawns.
func TestSecondSessionOnTheSameControlChannelIsRefusedBeforeSpawning(t *testing.T) {
	binary := testCLI(t)
	alpha := fixtureDir(t, "alpha", "modules", "shop", "services", "web")
	scope := freeSharedScope(t, alpha)

	first, err := WithDependencies(context.Background(),
		WithDirectory(alpha), WithNamingScope(scope), WithSharedControlChannel(),
		WithCommandScopedEnvironment(), WithCodeflyBinary(binary), WithTimeout(60*time.Second))
	if err != nil {
		t.Fatalf("WithDependencies() error = %v", err)
	}
	defer func() { _ = first.Destroy(context.Background()) }()

	refusedBinary, spawned := markerBinary(t)
	_, err = WithDependencies(context.Background(),
		WithDirectory(alpha), WithNamingScope(scope), WithSharedControlChannel(),
		WithCommandScopedEnvironment(), WithCodeflyBinary(refusedBinary), WithTimeout(60*time.Second))
	if err == nil || !strings.Contains(err.Error(), "control channel") {
		t.Fatalf("second session error = %v, want a control-channel rejection", err)
	}
	if _, statErr := os.Stat(spawned); !os.IsNotExist(statErr) {
		t.Fatalf("the refused session spawned a dependency stack; stat error = %v", statErr)
	}
}

// A session refused the process environment must not walk away still holding
// the control channel it claimed a moment earlier.
func TestRefusedGlobalInjectionReleasesTheControlChannel(t *testing.T) {
	binary := testCLI(t)
	alpha := fixtureDir(t, "alpha", "modules", "shop", "services", "web")

	holder := &Dependencies{dir: "holder"}
	if err := claimGlobalEnvironment(holder); err != nil {
		t.Fatalf("claimGlobalEnvironment() error = %v", err)
	}
	t.Cleanup(holder.ReleaseEnvironment)

	claimed := claimedControlChannels()
	_, err := WithDependencies(context.Background(),
		WithDirectory(alpha), WithCodeflyBinary(binary), WithTimeout(60*time.Second))
	if err == nil || !strings.Contains(err.Error(), "owns the process environment") {
		t.Fatalf("WithDependencies() error = %v, want single-owner rejection", err)
	}

	if after := claimedControlChannels(); after != claimed {
		t.Fatalf("control channels claimed after the refusal = %d, want %d", after, claimed)
	}
}

// claimedControlChannels counts the control channels this process is holding.
// An isolated session's channel is a per-invocation socket path, so a test
// cannot name it from outside — but it can prove none was left behind.
func claimedControlChannels() int {
	controlAddresses.mu.Lock()
	defer controlAddresses.mu.Unlock()
	return len(controlAddresses.inUse)
}

// Releasing a session hands its control channel back to the process.
func TestReleasedControlChannelCanBeClaimedAgain(t *testing.T) {
	address := "127.0.0.1:" + uniqueScope(t)
	if err := claimControlAddress(address, "first"); err != nil {
		t.Fatalf("claimControlAddress() error = %v", err)
	}
	if err := claimControlAddress(address, "second"); err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("competing claim error = %v, want a rejection naming the holder", err)
	}
	releaseControlAddress(address)
	if err := claimControlAddress(address, "second"); err != nil {
		t.Fatalf("claim after release error = %v", err)
	}
	releaseControlAddress(address)
}

// A session dropped without Stop must not lock the process out of global
// injection forever. Once its Codefly process is gone its injected values point
// at dependencies that no longer exist, so the next session restores them and
// takes over.
func TestDefunctOwnerReleasesTheProcessEnvironment(t *testing.T) {
	binary := testCLI(t)
	alphaKey := endpointKey("shop", "store")

	leaked, err := WithDependencies(context.Background(),
		WithDirectory(fixtureDir(t, "alpha", "modules", "shop", "services", "web")),
		WithCodeflyBinary(binary), WithTimeout(60*time.Second))
	if err != nil {
		t.Fatalf("WithDependencies() error = %v", err)
	}
	// The takeover below is what normally releases this session; on a failing
	// assertion nothing would, and the rest of the package would be refused.
	defer func() { _ = leaked.Destroy(context.Background()) }()
	if os.Getenv(alphaKey) == "" {
		t.Fatal("the owning session did not inject its endpoint")
	}

	// The caller drops the session without stopping it and its stack dies.
	if err := leaked.proc.Kill(); err != nil {
		t.Fatalf("kill leaked stack: %v", err)
	}
	select {
	case <-leaked.proc.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("leaked stack did not exit")
	}

	next := &Dependencies{dir: "next"}
	if err := claimGlobalEnvironment(next); err != nil {
		t.Fatalf("claiming after a defunct owner error = %v", err)
	}
	t.Cleanup(next.ReleaseEnvironment)
	if _, present := os.LookupEnv(alphaKey); present {
		t.Fatalf("%s from the defunct session was not restored", alphaKey)
	}
}
