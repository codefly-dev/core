package sdk

import (
	"bufio"
	"context"
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
		build := exec.Command("go", "build", "-o", testCLIPath, "./internal/testcli")
		if output, err := build.CombinedOutput(); err != nil {
			testCLIErr = fmt.Errorf("build test CLI: %w: %s", err, output)
		}
	})
	if testCLIErr != nil {
		t.Fatalf("test CLI unavailable: %v", testCLIErr)
	}
	return testCLIPath
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
		dir      string
		module   string
		service  string
		endpoint string
	}{
		{fixtureDir(t, "alpha", "modules", "shop", "services", "web"), "shop", "web", endpointKey("shop", "store")},
		{fixtureDir(t, "beta", "modules", "office", "services", "portal"), "office", "portal", endpointKey("office", "vault")},
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
	injected := os.Getenv(alphaKey)
	if injected == "" {
		t.Fatal("the owning session did not inject its endpoint")
	}

	_, err = WithDependencies(context.Background(),
		WithDirectory(beta), WithCodeflyBinary(binary), WithTimeout(60*time.Second))
	if err == nil || !strings.Contains(err.Error(), "owns the process environment") {
		t.Fatalf("second session error = %v, want single-owner rejection", err)
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

	second, err := WithDependencies(context.Background(),
		WithDirectory(beta), WithCodeflyBinary(binary), WithTimeout(60*time.Second))
	if err != nil {
		t.Fatalf("WithDependencies() after release error = %v", err)
	}
	defer func() { _ = second.Destroy(context.Background()) }()
	if got := os.Getenv(betaKey); got == "" {
		t.Fatal("the next owner did not inject its endpoint")
	}
	if _, present := os.LookupEnv(alphaKey); present {
		t.Fatalf("%s from the previous session is still set", alphaKey)
	}
}
