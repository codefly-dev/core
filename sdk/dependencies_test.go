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
	_, err := WithDependencies(context.Background(), WithTimeout(10*time.Second))
	if err == nil {
		t.Fatal("expected WithDependencies to report the early CLI exit")
	}
	if !strings.Contains(err.Error(), "CLI subprocess exited") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Race instrumentation and process-group cleanup can add a few seconds on
	// saturated CI hosts. This still proves the exit is observed well before the
	// configured 10-second readiness deadline.
	if elapsed := time.Since(started); elapsed > 6*time.Second {
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

func TestWithDependenciesUsesPinnedCodeflyBinary(t *testing.T) {
	dir := t.TempDir()
	selectedPath := filepath.Join(dir, "selected-codefly")
	conflictingPath := filepath.Join(dir, "conflicting-codefly")
	markerPath := filepath.Join(dir, "selected")

	selected := "#!/bin/sh\nprintf selected > " + markerPath + "\nexit 23\n"
	if err := os.WriteFile(selectedPath, []byte(selected), 0o755); err != nil {
		t.Fatalf("write selected binary: %v", err)
	}
	conflicting := "#!/bin/sh\nprintf conflicting > " + markerPath + "\nexit 24\n"
	if err := os.WriteFile(conflictingPath, []byte(conflicting), 0o755); err != nil {
		t.Fatalf("write conflicting binary: %v", err)
	}
	t.Setenv("CODEFLY_BINARY", conflictingPath)

	_, err := WithDependencies(t.Context(), WithCodeflyBinary(selectedPath), WithTimeout(10*time.Second))
	if err == nil || !strings.Contains(err.Error(), "CLI subprocess exited") {
		t.Fatalf("WithDependencies() error = %v, want selected child exit", err)
	}
	marker, readErr := os.ReadFile(markerPath)
	if readErr != nil {
		t.Fatalf("read selected binary marker: %v", readErr)
	}
	if got := string(marker); got != "selected" {
		t.Fatalf("dependency runner = %q, want selected", got)
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

func TestWithDependencyHomeChangesOnlySpawnedDependencyEnvironment(t *testing.T) {
	t.Setenv("HOME", "/real-home")
	environment := []string{
		"PATH=/real-home/.local/share/mise/shims:/usr/bin",
		"HOME=/real-home",
		"CODEFLY_HOME=/real-home/.codefly",
	}

	got := withDependencyHome(environment, "/tmp/dependency-home")
	want := []string{
		"PATH=/real-home/.local/share/mise/shims:/usr/bin",
		"CODEFLY_HOME=/real-home/.codefly",
		"HOME=/tmp/dependency-home",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependency environment = %v, want %v", got, want)
	}
	if gotHome := os.Getenv("HOME"); gotHome != "/real-home" {
		t.Fatalf("caller HOME = %q, want /real-home", gotHome)
	}
}

func TestWithDependencyHomeRequiresAbsolutePath(t *testing.T) {
	option := &Option{}
	WithDependencyHome("relative/home")(option)
	if err := validateDependencyOptions(option); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("validateDependencyOptions() error = %v, want absolute-path rejection", err)
	}
}

func TestWithCodeflyBinaryRequiresAbsolutePath(t *testing.T) {
	option := &Option{}
	WithCodeflyBinary("codefly")(option)
	if err := validateDependencyOptions(option); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("validateDependencyOptions() error = %v, want absolute-path rejection", err)
	}
}

func TestWorkspaceConfigurationOptionsUseOnePrivateChildCarrier(t *testing.T) {
	option := &Option{}
	WithWorkspaceConfiguration("routing", "REGION", "local")(option)
	WithWorkspaceSecret("execution-scheduler-auth", "TOKEN", "test-token")(option)

	got, err := withWorkspaceConfigurationOverrides([]string{
		"PATH=/usr/bin",
		resources.WorkspaceConfigurationOverridesEnvironment + "=stale",
	}, option.WorkspaceConfigurations)
	if err != nil {
		t.Fatalf("withWorkspaceConfigurationOverrides: %v", err)
	}
	var carriers []string
	for _, entry := range got {
		if strings.HasPrefix(entry, resources.WorkspaceConfigurationOverridesEnvironment+"=") {
			carriers = append(carriers, entry)
		}
	}
	if len(carriers) != 1 {
		t.Fatalf("workspace configuration carriers = %v, want exactly one", carriers)
	}
	encoded := strings.TrimPrefix(carriers[0], resources.WorkspaceConfigurationOverridesEnvironment+"=")
	overrides, err := resources.DecodeWorkspaceConfigurationOverrides(encoded)
	if err != nil {
		t.Fatalf("decode child carrier: %v", err)
	}
	want := []resources.WorkspaceConfigurationOverride{
		{Name: "execution-scheduler-auth", Key: "TOKEN", Value: "test-token", Secret: true},
		{Name: "routing", Key: "REGION", Value: "local"},
	}
	if !reflect.DeepEqual(overrides, want) {
		t.Fatalf("child overrides = %#v, want %#v", overrides, want)
	}
	if !slices.Contains(got, "PATH=/usr/bin") {
		t.Fatalf("unrelated child environment was not preserved: %v", got)
	}
}

func TestWorkspaceConfigurationOptionsRejectDuplicateCoordinates(t *testing.T) {
	option := &Option{}
	WithWorkspaceSecret("execution-scheduler-auth", "TOKEN", "first")(option)
	WithWorkspaceSecret("execution_scheduler_auth", "token", "second")(option)

	_, err := withWorkspaceConfigurationOverrides(nil, option.WorkspaceConfigurations)
	if err == nil || !strings.Contains(err.Error(), "duplicate workspace configuration override") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestWorkspaceConfigurationOptionsRejectReusableOrParentOwnedStacks(t *testing.T) {
	option := &Option{}
	WithKeepRunning()(option)
	WithWorkspaceSecret("execution-scheduler-auth", "TOKEN", "test-token")(option)
	if err := validateDependencyOptions(option); err == nil || !strings.Contains(err.Error(), "reusable dependency stack") {
		t.Fatalf("reusable-stack error = %v", err)
	}

	t.Setenv(resources.RunningPrefix, "true")
	t.Setenv(resources.EndpointPrefix+"__INFRA__POSTGRES__TCP__TCP", "127.0.0.1:5432")
	_, err := WithDependencies(t.Context(), WithWorkspaceSecret("execution-scheduler-auth", "TOKEN", "test-token"))
	if err == nil || !strings.Contains(err.Error(), "parent Codefly runtime") {
		t.Fatalf("parent-owned-stack error = %v", err)
	}
}

func TestServiceConfigurationOptionsUseOnePrivateChildCarrier(t *testing.T) {
	option := &Option{}
	WithServiceConfiguration("users/store", "postgres", "POSTGRES_USER", "mind")(option)
	WithServiceSecret("users/store", "postgres", "POSTGRES_PASSWORD", "test-password")(option)

	got, err := withServiceConfigurationOverrides([]string{
		"PATH=/usr/bin",
		resources.ServiceConfigurationOverridesEnvironment + "=stale",
	}, option.ServiceConfigurations)
	if err != nil {
		t.Fatalf("withServiceConfigurationOverrides: %v", err)
	}
	var carriers []string
	for _, entry := range got {
		if strings.HasPrefix(entry, resources.ServiceConfigurationOverridesEnvironment+"=") {
			carriers = append(carriers, entry)
		}
	}
	if len(carriers) != 1 {
		t.Fatalf("service configuration carriers = %v, want exactly one", carriers)
	}
	encoded := strings.TrimPrefix(carriers[0], resources.ServiceConfigurationOverridesEnvironment+"=")
	overrides, err := resources.DecodeServiceConfigurationOverrides(encoded)
	if err != nil {
		t.Fatalf("decode child carrier: %v", err)
	}
	want := []resources.ServiceConfigurationOverride{
		{Service: "users/store", Name: "postgres", Key: "POSTGRES_PASSWORD", Value: "test-password", Secret: true},
		{Service: "users/store", Name: "postgres", Key: "POSTGRES_USER", Value: "mind"},
	}
	if !reflect.DeepEqual(overrides, want) {
		t.Fatalf("child overrides = %#v, want %#v", overrides, want)
	}
	if !slices.Contains(got, "PATH=/usr/bin") {
		t.Fatalf("unrelated child environment was not preserved: %v", got)
	}
}

func TestServiceConfigurationOptionsRejectReusableOrParentOwnedStacks(t *testing.T) {
	option := &Option{}
	WithKeepRunning()(option)
	WithServiceSecret("users/store", "postgres", "POSTGRES_PASSWORD", "test-password")(option)
	if err := validateDependencyOptions(option); err == nil || !strings.Contains(err.Error(), "reusable dependency stack") {
		t.Fatalf("reusable-stack error = %v", err)
	}

	t.Setenv(resources.RunningPrefix, "true")
	t.Setenv(resources.EndpointPrefix+"__INFRA__POSTGRES__TCP__TCP", "127.0.0.1:5432")
	_, err := WithDependencies(t.Context(), WithServiceSecret("users/store", "postgres", "POSTGRES_PASSWORD", "test-password"))
	if err == nil || !strings.Contains(err.Error(), "parent Codefly runtime") {
		t.Fatalf("parent-owned-stack error = %v", err)
	}
}

func TestWithFixtureSelectsDependencyStackFixture(t *testing.T) {
	option := &Option{}
	WithFixture("dev-admin")(option)
	if option.Fixture != "dev-admin" {
		t.Fatalf("fixture = %q, want dev-admin", option.Fixture)
	}
}

func TestRunProfilesPassThroughToCodefly(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "../resources/testdata/workspaces/run-profiles")
	if err != nil {
		t.Fatalf("load run-profile fixture: %v", err)
	}

	tests := []struct {
		name     string
		resolved resources.RunProfile
	}{
		{
			name: "local",
			resolved: resources.RunProfile{
				ExcludeDependencies:            []string{"users/accounts"},
				ExcludeWorkspaceConfigurations: []string{"internal-auth"},
			},
		},
		{name: "saas", resolved: resources.RunProfile{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := workspace.ResolveRunProfile(ctx, tt.name, resources.RunProfile{})
			if err != nil {
				t.Fatalf("resolve %s profile: %v", tt.name, err)
			}
			if !reflect.DeepEqual(resolved, tt.resolved) {
				t.Fatalf("resolved %s profile = %#v, want %#v", tt.name, resolved, tt.resolved)
			}

			option := &Option{}
			WithRunProfile(tt.name)(option)
			WithExcludedDependencies("storage/postgres")(option)
			args := dependencyCommandArguments(option)

			profileFlag := slices.Index(args, "--profile")
			if profileFlag == -1 || profileFlag+1 >= len(args) || args[profileFlag+1] != tt.name {
				t.Fatalf("profile arguments = %v, want --profile %s", args, tt.name)
			}
			excludeFlag := slices.Index(args, "--exclude-dependency")
			if excludeFlag == -1 || excludeFlag+1 >= len(args) || args[excludeFlag+1] != "storage/postgres" {
				t.Fatalf("explicit exclusion arguments = %v", args)
			}
		})
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
	if !waitFor(5*time.Second, func() bool {
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
