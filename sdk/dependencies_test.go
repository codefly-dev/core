package sdk

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
)

// TestWithDependencies_KillsProcessGroupOnCancellation verifies that when a
// post-spawn error path is hit (here: cancellation before gRPC readiness),
// WithDependencies tears down the entire spawned process group instead of
// leaking it. A stand-in "codefly" binary backgrounds a child, records both
// PIDs, then blocks forever without ever serving gRPC.
func TestWithDependencies_KillsProcessGroupOnCancellation(t *testing.T) {
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

	// Do not use a short readiness timeout as an implicit "the child started"
	// signal. An all-package race sweep can starve the newly spawned shell long
	// enough for that deadline to fire before it records its PIDs. Wait for the
	// explicit PID-file handshake, then cancel to exercise the same post-spawn
	// cleanup path deterministically.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := WithDependencies(ctx, WithTimeout(30*time.Second))
		result <- err
	}()

	leaderPID, childPID := readPIDFile(t, pidFile)
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected WithDependencies to fail after cancellation")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WithDependencies did not return after cancellation")
	}

	if !waitFor(3*time.Second, func() bool { return !pidAlive(leaderPID) }) {
		t.Errorf("group leader %d still alive after WithDependencies error — process group leaked", leaderPID)
	}
	if !waitFor(3*time.Second, func() bool { return !pidAlive(childPID) }) {
		t.Errorf("child %d still alive after WithDependencies error — process group leaked", childPID)
	}
}

func TestWithDependencies_ReturnsWhenCLIExitsBeforeReady(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "codefly")
	argsPath := filepath.Join(dir, "args")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"" + argsPath + "\"\n" +
		"exit 23\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("CODEFLY_BINARY", binPath)

	started := time.Now()
	_, err := WithDependencies(context.Background(), WithTimeout(30*time.Second))
	if err == nil {
		t.Fatal("expected WithDependencies to report the early CLI exit")
	}
	if !strings.Contains(err.Error(), "CLI subprocess exited") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Race instrumentation and process-group cleanup can add several seconds on
	// saturated CI hosts. This still proves the exit is observed well before the
	// configured 30-second readiness deadline.
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("early CLI exit took %s to report", elapsed)
	}
	args, readErr := os.ReadFile(argsPath)
	if readErr != nil {
		t.Fatalf("read fake CLI arguments: %v", readErr)
	}
	if !slices.Contains(strings.Fields(string(args)), "--temporary-ports") {
		t.Fatalf("SDK-owned dependency flow did not request temporary ports: %s", args)
	}
}

func TestWithCLIServerPortPinsChildControlChannel(t *testing.T) {
	environment := []string{
		"PATH=/usr/bin",
		"CODEFLY_CLI_SERVER_PORT=21854",
		"HOME=/tmp/home",
		"CODEFLY_CLI_SERVER_PORT=stale-duplicate",
	}

	got := withCLIServerPort(environment, "127.0.0.1:25870")
	var controlSettings []string
	for _, entry := range got {
		if strings.HasPrefix(entry, "CODEFLY_CLI_SERVER_PORT=") {
			controlSettings = append(controlSettings, entry)
		}
	}
	if len(controlSettings) != 1 {
		t.Fatalf("control-port settings = %v, want exactly one", controlSettings)
	}
	if controlSettings[0] != "CODEFLY_CLI_SERVER_PORT=25870" {
		t.Fatalf("control-port setting = %q, want SDK-selected port", controlSettings[0])
	}
	if !slices.Contains(got, "PATH=/usr/bin") || !slices.Contains(got, "HOME=/tmp/home") {
		t.Fatalf("unrelated child environment was not preserved: %v", got)
	}
}

func TestWithCLIServerPortLeavesEnvironmentForInvalidAddress(t *testing.T) {
	environment := []string{"CODEFLY_CLI_SERVER_PORT=32100", "PATH=/usr/bin"}
	got := withCLIServerPort(environment, "not-an-address")
	if !reflect.DeepEqual(got, environment) {
		t.Fatalf("invalid address changed environment: got %v want %v", got, environment)
	}
}

func TestWithFixtureSelectsDependencyStackFixture(t *testing.T) {
	option := &Option{}
	WithFixture("dev-admin")(option)
	if option.Fixture != "dev-admin" {
		t.Fatalf("fixture = %q, want dev-admin", option.Fixture)
	}
}

func TestManagedDependencyEnvironmentRequiresRunnerAndEndpoint(t *testing.T) {
	endpoint := resources.EndpointPrefix + "__SAAS_STARTER__STORE__TCP__TCP"
	tests := []struct {
		name        string
		environment []string
		want        bool
	}{
		{
			name: "managed runtime with endpoint",
			environment: []string{
				resources.RunningPrefix + "=true",
				resources.RuntimeContextPrefix + "=" + resources.RuntimeContextNative,
				endpoint + "=localhost:5432",
			},
			want: true,
		},
		{
			name: "standalone managed runtime",
			environment: []string{
				resources.RunningPrefix + "=true",
				resources.RuntimeContextPrefix + "=" + resources.RuntimeContextNative,
			},
		},
		{
			name: "direct test with endpoint-shaped value",
			environment: []string{
				endpoint + "=localhost:5432",
			},
		},
		{
			name: "empty endpoint",
			environment: []string{
				resources.RunningPrefix + "=true",
				endpoint + "=",
			},
		},
		{
			name: "false runner marker",
			environment: []string{
				resources.RunningPrefix + "=false",
				endpoint + "=localhost:5432",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasManagedDependencyEnvironment(tt.environment); got != tt.want {
				t.Fatalf("hasManagedDependencyEnvironment() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithDependenciesReusesParentManagedRuntime(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "nested-flow-started")
	binPath := filepath.Join(dir, "codefly")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\ntouch \""+started+"\"\nexit 23\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("CODEFLY_BINARY", binPath)
	t.Setenv(resources.RunningPrefix, "true")
	t.Setenv(resources.RuntimeContextPrefix, resources.RuntimeContextNative)
	t.Setenv(resources.EndpointPrefix+"__SAAS_STARTER__STORE__TCP__TCP", "localhost:5432")

	deps, err := WithDependencies(context.Background())
	if err != nil {
		t.Fatalf("WithDependencies() error = %v", err)
	}
	if !deps.inherited {
		t.Fatal("managed dependencies were not marked as inherited")
	}
	if err := deps.WaitForReady(context.Background(), &Option{Timeout: time.Second}); err != nil {
		t.Fatalf("WaitForReady() error = %v", err)
	}
	if err := deps.SetEnvironment(context.Background()); err != nil {
		t.Fatalf("SetEnvironment() error = %v", err)
	}
	if err := deps.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := deps.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("nested Codefly flow was started; stat error = %v", err)
	}
}

// readPIDFile waits for the fake binary to record its two PIDs, then parses them.
func readPIDFile(t *testing.T, path string) (int, int) {
	t.Helper()
	var content []byte
	if !waitFor(15*time.Second, func() bool {
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
