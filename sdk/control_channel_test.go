package sdk

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/sdk/session"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestDefaultSessionsNeverShareAControlChannel is the workspace-collision case
// from the audit: two default invocations of the same workspace — independent
// test packages, or two worktrees checked out under one workspace name —
// previously computed the same control address from the workspace name alone.
func TestDefaultSessionsNeverShareAControlChannel(t *testing.T) {
	ctx := context.Background()
	first := mustControlChannel(t, ctx, &Option{})
	second := mustControlChannel(t, ctx, &Option{})

	if first.target == second.target {
		t.Fatalf("two default sessions selected the same control channel %s", first.target)
	}
	if first.session.ID == second.session.ID {
		t.Fatal("two default sessions minted the same invocation identity")
	}
	if first.scope == second.scope {
		t.Fatalf("two default sessions selected the same naming scope %s", first.scope)
	}
	if !strings.HasPrefix(first.target, "unix:") {
		t.Fatalf("default control channel = %s, want an SDK-owned socket", first.target)
	}

	// A caller-supplied naming scope is a human label: it stays readable but
	// the invocation identity is what keeps the scope unique.
	labelled := mustControlChannel(t, ctx, &Option{NamingScope: "dev"})
	if !strings.HasPrefix(labelled.scope, "dev-") || labelled.scope == "dev" {
		t.Fatalf("labelled scope = %q, want the label plus an invocation identity", labelled.scope)
	}
}

func TestSharedControlChannelStaysOnTheWorkspacePort(t *testing.T) {
	ctx := context.Background()
	channel := mustControlChannel(t, ctx, &Option{SharedControlChannel: true, NamingScope: "dev"})
	if channel.isolated() {
		t.Fatal("the shared control channel claimed session isolation")
	}
	if want := cliServerAddress(ctx, "dev"); channel.target != want {
		t.Fatalf("shared control target = %s, want %s", channel.target, want)
	}
	if channel.scope != "dev" {
		t.Fatalf("shared control scope = %q, want the caller's own label", channel.scope)
	}
}

// TestConcurrentDefaultSessionsAreIndependent is the acceptance case: two
// default dependency sessions started concurrently in one workspace, with no
// naming flags, against real CLI subprocesses over real sockets. Each must own
// its own control channel, and destroying one must leave the other untouched.
func TestConcurrentDefaultSessionsAreIndependent(t *testing.T) {
	binary := buildControlServer(t)
	records := t.TempDir()
	enterSessionWorkspace(t)
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", records)

	sessions := make([]*Dependencies, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sessions[i], errs[i] = WithDependencies(context.Background(), WithTimeout(30*time.Second))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("session %d: WithDependencies() error = %v", i, err)
		}
		t.Cleanup(func() { _ = sessions[i].Destroy(context.Background()) })
	}

	if sessions[0].control.Directory == sessions[1].control.Directory {
		t.Fatalf("both sessions shared the control directory %s", sessions[0].control.Directory)
	}
	for i, deps := range sessions {
		if _, err := deps.cli.Ping(context.Background(), &emptypb.Empty{}); err != nil {
			t.Fatalf("session %d is not reachable on its own channel: %v", i, err)
		}
	}

	survivor := sessions[1]
	survivorRecord := recordFor(t, records, survivor)
	if err := sessions[0].Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if got := readRecord(t, recordFor(t, records, sessions[0])); !strings.Contains(got, "DestroyFlow") {
		t.Fatalf("destroyed session never received DestroyFlow: %s", got)
	}
	if got := readRecord(t, survivorRecord); strings.Contains(got, "DestroyFlow") || strings.Contains(got, "StopFlow") {
		t.Fatalf("tearing one session down reached the other: %s", got)
	}
	if _, err := survivor.cli.Ping(context.Background(), &emptypb.Empty{}); err != nil {
		t.Fatalf("the surviving session lost its control channel: %v", err)
	}
}

func TestForeignControlServerIsRejectedBeforeDestructiveRPCs(t *testing.T) {
	records := runFailingSession(t, "foreign", 30*time.Second, "does not own this dependency session")
	if got := readRecord(t, onlyRecord(t, records)); strings.Contains(got, "StopFlow") || strings.Contains(got, "DestroyFlow") || strings.Contains(got, "GetConfiguration") {
		t.Fatalf("a server that failed the handshake was still driven: %s", got)
	}
}

func TestControlServerWithoutHandshakeIsReportedNotDowngraded(t *testing.T) {
	records := runFailingSession(t, "no-handshake", 30*time.Second, "does not implement the session handshake")
	if got := readRecord(t, onlyRecord(t, records)); strings.Contains(got, "GetFlowStatus") {
		t.Fatalf("an unverified control server was driven anyway: %s", got)
	}
}

func TestControlSocketNeverBoundIsReportedAsAMissingCapability(t *testing.T) {
	// The deadline is the assertion here: a CLI that never binds the socket
	// must be reported as a missing capability rather than waited on forever.
	runFailingSession(t, "no-socket", 10*time.Second, session.SocketEnvironment)
}

// runFailingSession starts one default session against a misbehaving control
// server and asserts WithDependencies refuses it with a specific diagnosis.
func runFailingSession(t *testing.T, mode string, timeout time.Duration, wantError string) string {
	t.Helper()
	binary := buildControlServer(t)
	records := t.TempDir()
	enterSessionWorkspace(t)
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", records)
	t.Setenv("FAKE_CONTROL_MODE", mode)

	_, err := WithDependencies(context.Background(), WithTimeout(timeout))
	if err == nil {
		t.Fatal("WithDependencies() accepted a control server it does not own")
	}
	if !strings.Contains(err.Error(), wantError) {
		t.Fatalf("WithDependencies() error = %v, want it to mention %q", err, wantError)
	}
	return records
}

func TestWarmSessionReuseRequiresAMatchingPlan(t *testing.T) {
	channel, err := newControlChannel(context.Background(), &Option{KeepRunning: true, Fixture: "dev-admin"})
	if err != nil {
		t.Fatalf("newControlChannel() error = %v", err)
	}
	t.Cleanup(func() { _ = channel.control.Remove() })

	if _, err := warmSessionOwner(channel); err == nil || !strings.Contains(err.Error(), "no reusable dependency session") {
		t.Fatalf("warmSessionOwner() error = %v, want a missing-receipt refusal", err)
	}

	if err := channel.control.WriteReceipt(session.Receipt{ID: "id", Secret: "secret", Fingerprint: "another-plan"}); err != nil {
		t.Fatalf("WriteReceipt() error = %v", err)
	}
	if _, err := warmSessionOwner(channel); err == nil || !strings.Contains(err.Error(), "different plan") {
		t.Fatalf("warmSessionOwner() error = %v, want a plan-mismatch refusal", err)
	}

	if err := channel.control.WriteReceipt(session.Receipt{ID: "id", Secret: "secret", Fingerprint: channel.fingerprint}); err != nil {
		t.Fatalf("WriteReceipt() error = %v", err)
	}
	owner, err := warmSessionOwner(channel)
	if err != nil {
		t.Fatalf("warmSessionOwner() error = %v", err)
	}
	if owner.ID != "id" || owner.Secret != "secret" {
		t.Fatalf("warm owner = %+v, want the recorded session", owner)
	}
}

func TestReuseFingerprintTracksThePlanNotJustTheWorkspace(t *testing.T) {
	ctx := context.Background()
	base := reuseFingerprint(ctx, &Option{})
	cases := map[string]*Option{
		"fixture":     {Fixture: "dev-admin"},
		"profile":     {RunProfile: "local"},
		"exclusions":  {ExcludedDependencies: []string{"infra/temporal"}},
		"naming":      {NamingScope: "dev"},
		"home":        {DependencyHome: "/tmp/home"},
		"unchanged":   {},
		"reorderable": {ExcludedDependencies: []string{"b", "a"}},
	}
	if got := reuseFingerprint(ctx, cases["unchanged"]); got != base {
		t.Fatal("an identical plan produced a different fingerprint")
	}
	reordered := reuseFingerprint(ctx, &Option{ExcludedDependencies: []string{"a", "b"}})
	if reordered != reuseFingerprint(ctx, cases["reorderable"]) {
		t.Fatal("exclusion order changed the fingerprint")
	}
	for name, opt := range cases {
		if name == "unchanged" {
			continue
		}
		if reuseFingerprint(ctx, opt) == base {
			t.Fatalf("changing %s left the reuse fingerprint unchanged", name)
		}
	}
}

func mustControlChannel(t *testing.T, ctx context.Context, opt *Option) *controlChannel {
	t.Helper()
	channel, err := newControlChannel(ctx, opt)
	if err != nil {
		t.Fatalf("newControlChannel() error = %v", err)
	}
	t.Cleanup(channel.discard)
	return channel
}

// enterSessionWorkspace runs the test from a real service inside a real
// workspace and clears the process-wide resource cache the SDK keeps, so a
// prior test's workspace cannot leak into this one. It also warms the cache so
// concurrent sessions only read it.
func enterSessionWorkspace(t *testing.T) {
	t.Helper()
	t.Chdir("testdata/session-workspace/modules/app/services/api")
	runningModule, runningService = nil, nil
	t.Cleanup(func() { runningModule, runningService = nil, nil })
	t.Setenv("CODEFLY__RUNTIME_CONTEXT", "native")
	if _, err := Service(); err != nil {
		t.Fatalf("load session workspace fixture: %v", err)
	}
	if _, err := Module(); err != nil {
		t.Fatalf("load session workspace fixture: %v", err)
	}
}

func buildControlServer(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "controlserver")
	build := exec.Command("go", "build", "-o", binary, "./testdata/controlserver")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build control server fixture: %v\n%s", err, output)
	}
	return binary
}

func recordFor(t *testing.T, records string, deps *Dependencies) string {
	t.Helper()
	return filepath.Join(records, filepath.Base(deps.control.Directory)+".log")
}

func onlyRecord(t *testing.T, records string) string {
	t.Helper()
	entries, err := os.ReadDir(records)
	if err != nil {
		t.Fatalf("read record directory: %v", err)
	}
	if len(entries) != 1 {
		return filepath.Join(records, "absent.log")
	}
	return filepath.Join(records, entries[0].Name())
}

func readRecord(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read RPC record: %v", err)
	}
	return string(content)
}
