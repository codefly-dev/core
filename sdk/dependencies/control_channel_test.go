package dependencies

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/sdk/session"
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
	if want := cliServerAddress(ctx, testSessionDirectory(t), "dev"); channel.target != want {
		t.Fatalf("shared control target = %s, want %s", channel.target, want)
	}
	if channel.scope != "dev" {
		t.Fatalf("shared control scope = %q, want the caller's own label", channel.scope)
	}
}

// TestIndependentProcessesInSameNamedWorkspacesAreIsolated is the acceptance
// case: two default dependency sessions started as separate OS processes, from
// two checkouts that carry the same workspace name, with no naming flags.
//
// It runs real driver processes rather than goroutines because two sessions in
// one process cannot both be used — SetEnvironment injects into the shared
// process environment, so the second would overwrite the first — which means
// an in-process test cannot reproduce what the audit describes.
func TestIndependentProcessesInSameNamedWorkspacesAreIsolated(t *testing.T) {
	controlServer := buildFixture(t, "./testdata/controlserver")
	driver := buildFixture(t, "./testdata/sessiondriver")

	first := startSessionProcess(t, driver, controlServer, copyWorkspace(t))
	second := startSessionProcess(t, driver, controlServer, copyWorkspace(t))

	firstIdentity := first.identity(t)
	secondIdentity := second.identity(t)
	if firstIdentity.session == secondIdentity.session {
		t.Fatalf("both processes were handed session %s", firstIdentity.session)
	}
	if firstIdentity.socket == secondIdentity.socket {
		t.Fatalf("both processes were handed control socket %s", firstIdentity.socket)
	}
	if firstIdentity.scope == secondIdentity.scope {
		t.Fatalf("both processes were handed naming scope %s", firstIdentity.scope)
	}
	// The naming scope is what separates the resources the CLI creates, so it
	// has to be derived from the invocation identity rather than left empty.
	if !strings.Contains(firstIdentity.scope, "s"+firstIdentity.session[:12]) {
		t.Fatalf("naming scope %q does not carry session %s", firstIdentity.scope, firstIdentity.session)
	}

	first.stop(t)
	if got := first.record(t); !strings.Contains(got, "DestroyFlow") {
		t.Fatalf("the stopped session never received DestroyFlow: %s", got)
	}
	if got := second.record(t); strings.Contains(got, "DestroyFlow") || strings.Contains(got, "StopFlow") {
		t.Fatalf("stopping one process reached the other session: %s", got)
	}
	second.stop(t)
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
	channel, err := newControlChannel(context.Background(), testSessionDirectory(t), &Option{KeepRunning: true, Fixture: "dev-admin"})
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
	dir := testSessionDirectory(t)
	base := reuseFingerprint(ctx, dir, &Option{})
	cases := map[string]*Option{
		"fixture":     {Fixture: "dev-admin"},
		"profile":     {RunProfile: "local"},
		"exclusions":  {ExcludedDependencies: []string{"infra/temporal"}},
		"naming":      {NamingScope: "dev"},
		"home":        {DependencyHome: "/tmp/home"},
		"unchanged":   {},
		"reorderable": {ExcludedDependencies: []string{"b", "a"}},
	}
	if got := reuseFingerprint(ctx, dir, cases["unchanged"]); got != base {
		t.Fatal("an identical plan produced a different fingerprint")
	}
	reordered := reuseFingerprint(ctx, dir, &Option{ExcludedDependencies: []string{"a", "b"}})
	if reordered != reuseFingerprint(ctx, dir, cases["reorderable"]) {
		t.Fatal("exclusion order changed the fingerprint")
	}
	for name, opt := range cases {
		if name == "unchanged" {
			continue
		}
		if reuseFingerprint(ctx, dir, opt) == base {
			t.Fatalf("changing %s left the reuse fingerprint unchanged", name)
		}
	}
}

// TestWarmRespawnRefusesToDisplaceALiveControlServer covers the hazard the
// isolated design would otherwise reintroduce: a reusable session's socket
// path is stable, so clearing it when a server is still listening frees the
// path for a second child and puts two stacks on one set of containers.
func TestWarmRespawnRefusesToDisplaceALiveControlServer(t *testing.T) {
	channel, err := newControlChannel(context.Background(), testSessionDirectory(t), &Option{KeepRunning: true})
	if err != nil {
		t.Fatalf("newControlChannel() error = %v", err)
	}
	t.Cleanup(func() { _ = channel.control.Remove() })

	listener, err := net.Listen("unix", channel.control.Socket)
	if err != nil {
		t.Fatalf("occupy the reusable control socket: %v", err)
	}
	defer func() { _ = listener.Close() }()

	if err := channel.control.ClearStaleSocket(); err == nil || !strings.Contains(err.Error(), "live control server") {
		t.Fatalf("ClearStaleSocket() error = %v, want a refusal to displace the live server", err)
	}
	if _, err := os.Stat(channel.control.Socket); err != nil {
		t.Fatalf("a live control socket was removed anyway: %v", err)
	}

	// Once the owner is gone the same path is genuinely stale and must clear,
	// or a warm session could never restart after a crash.
	_ = listener.Close()
	_ = os.Remove(channel.control.Socket)
	if err := channel.control.ClearStaleSocket(); err != nil {
		t.Fatalf("ClearStaleSocket() on an absent socket error = %v", err)
	}
}

func TestReusableControlDirectoryRejectsAPlantedSymlink(t *testing.T) {
	channel, err := newControlChannel(context.Background(), testSessionDirectory(t), &Option{KeepRunning: true})
	if err != nil {
		t.Fatalf("newControlChannel() error = %v", err)
	}
	directory := channel.control.Directory
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.RemoveAll(directory); err != nil {
		t.Fatalf("clear control directory: %v", err)
	}
	// Stand in for a local user who guessed the fingerprint and planted a link
	// to a directory of their choosing before the session started.
	if err := os.Symlink(t.TempDir(), directory); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}
	if _, err := session.OpenControl(filepath.Base(directory)[len("codefly-warm-"):]); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("OpenControl() error = %v, want a refusal to follow the planted link", err)
	}
}

func TestRejectedOptionsDoNotOrphanAControlDirectory(t *testing.T) {
	before := countControlDirectories(t)
	_, err := WithDependencies(context.Background(),
		WithWorkspaceConfiguration("auth", "TOKEN", "first"),
		WithWorkspaceConfiguration("AUTH", "token", "second"))
	if err == nil || !strings.Contains(err.Error(), "duplicate workspace configuration override") {
		t.Fatalf("WithDependencies() error = %v, want the duplicate-coordinate rejection", err)
	}
	if after := countControlDirectories(t); after != before {
		t.Fatalf("control directories %d -> %d: a rejected option set orphaned one", before, after)
	}
}

// TestHandshakeTimesOutAgainstAStalledServer pins the handshake's own
// deadline. The fixture completes the gRPC and health handshakes and then
// never answers the RPC, so the connection stays healthy and nothing else
// bounds the call — without its own timeout WithDependencies waits forever.
func TestHandshakeTimesOutAgainstAStalledServer(t *testing.T) {
	runFailingSession(t, "hang-handshake", 5*time.Second, "session handshake with the CLI server")
}

func TestHandshakeReportsVersionSkewAsAnUpgrade(t *testing.T) {
	records := runFailingSession(t, "wrong-version", 30*time.Second, "protocol version")
	if got := readRecord(t, onlyRecord(t, records)); strings.Contains(got, "GetFlowStatus") {
		t.Fatalf("a version-skewed control server was driven anyway: %s", got)
	}
}

func TestHandshakeRequiresTheIsolationCapability(t *testing.T) {
	runFailingSession(t, "no-capability", 30*time.Second, session.IsolatedControlSocketCapability)
}

// TestReadinessOverrunIsAttributedToTheStalledDependency drives the whole wait
// against a flow that never becomes ready, which is the shape a consumer hits
// when one dependency's image pull overruns the suite's readiness budget. The
// failure has to reach the caller as a per-service view, not as a flow-wide
// string it would have to parse.
func TestReadinessOverrunIsAttributedToTheStalledDependency(t *testing.T) {
	binary := buildControlServer(t)
	enterSessionWorkspace(t)
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", t.TempDir())
	t.Setenv("FAKE_CONTROL_MODE", "stalled-dependency")

	_, err := WithDependencies(context.Background(), WithTimeout(5*time.Second))
	if err == nil {
		t.Fatal("WithDependencies() accepted a flow that never became ready")
	}
	var overrun *ReadinessTimeout
	if !errors.As(err, &overrun) {
		t.Fatalf("WithDependencies() error = %v, want a *ReadinessTimeout the caller can inspect", err)
	}
	pending := overrun.Pending()
	if len(pending) != 2 || pending[0].GetService() != "infra/postgres" {
		t.Fatalf("Pending() = %v, want the dependencies the CLI reported as unready", pending)
	}
	if got := pending[0].GetLifecycle(); got != v0.ServiceLifecycle_SERVICE_LIFECYCLE_ACQUIRING_IMAGE {
		t.Fatalf("stalled dependency lifecycle = %s, want the image acquisition stage", got)
	}
	if !strings.Contains(err.Error(), "infra/postgres acquiring image") {
		t.Fatalf("WithDependencies() error = %v, want it to name the stalled dependency", err)
	}
}

// TestFailedDependencyIsNotWaitedOut covers the verdict a budget cannot
// change. A dependency the flow reports as failed will not become ready, so
// spending the remaining budget on it both delays the answer and reports a
// hard failure as a timeout. The generous budget here is the assertion: the
// call has to return long before it expires.
func TestFailedDependencyIsNotWaitedOut(t *testing.T) {
	binary := buildControlServer(t)
	enterSessionWorkspace(t)
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", t.TempDir())
	t.Setenv("FAKE_CONTROL_MODE", "failed-dependency")

	started := time.Now()
	_, err := WithDependencies(context.Background(), WithTimeout(30*time.Second))
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("WithDependencies() accepted a flow with a failed dependency")
	}
	var failure *ReadinessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("WithDependencies() error = %v, want a *ReadinessFailure rather than a timeout", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("a failed dependency was waited on for %s of a 30s budget", elapsed)
	}
	if len(failure.Services) != 1 || failure.Services[0].GetService() != "app/api" {
		t.Fatalf("Services = %v, want only the dependency that failed", failure.Services)
	}
	if !strings.Contains(err.Error(), "app/api failed") || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("WithDependencies() error = %v, want it to name the failure and its diagnosis", err)
	}
}

// TestLegacyFlowStatusStillReportsTheAggregateVerdict pins the degraded path
// on purpose rather than leaving it to whichever fixture happens to be stale:
// an orchestrator that reports no per-service entries must still produce the
// flow-wide verdict, with nothing invented to fill the gap.
func TestLegacyFlowStatusStillReportsTheAggregateVerdict(t *testing.T) {
	binary := buildControlServer(t)
	enterSessionWorkspace(t)
	t.Setenv("CODEFLY_BINARY", binary)
	t.Setenv("FAKE_CONTROL_RECORD_DIR", t.TempDir())
	t.Setenv("FAKE_CONTROL_MODE", "legacy-flow")

	_, err := WithDependencies(context.Background(), WithTimeout(5*time.Second))
	if err == nil {
		t.Fatal("WithDependencies() accepted a flow that never became ready")
	}
	var overrun *ReadinessTimeout
	if !errors.As(err, &overrun) {
		t.Fatalf("WithDependencies() error = %v, want a *ReadinessTimeout", err)
	}
	if len(overrun.Pending()) != 0 {
		t.Fatalf("Pending() = %v, want nothing attributed when the flow reported no entries", overrun.Pending())
	}
	if got := err.Error(); got != "timeout waiting for flow to be ready after 5s" {
		t.Fatalf("error = %q, want the unadorned flow-wide message", got)
	}
}

// testSessionDirectory is the directory these tests anchor a session to. They
// predate WithDirectory and were written against the process working
// directory, which enterSessionWorkspace moves into a real workspace.
func testSessionDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	return dir
}

func mustControlChannel(t *testing.T, ctx context.Context, opt *Option) *controlChannel {
	t.Helper()
	channel, err := newControlChannel(ctx, testSessionDirectory(t), opt)
	if err != nil {
		t.Fatalf("newControlChannel() error = %v", err)
	}
	t.Cleanup(channel.discard)
	return channel
}

// enterSessionWorkspace runs the test from a real service inside a real
// workspace. The resource cache the SDK keeps is keyed by the directory it was
// resolved from, so entering and leaving this workspace re-resolves on its own
// and a prior test's workspace cannot leak into this one.
func enterSessionWorkspace(t *testing.T) {
	t.Helper()
	t.Chdir("testdata/session-workspace/modules/app/services/api")
	t.Setenv("CODEFLY__RUNTIME_CONTEXT", "native")
}

func buildControlServer(t *testing.T) string {
	t.Helper()
	return buildFixture(t, "./testdata/controlserver")
}

func buildFixture(t *testing.T, pkg string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), filepath.Base(pkg))
	build := exec.Command("go", "build", "-o", binary, pkg)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s fixture: %v\n%s", pkg, err, output)
	}
	return binary
}

// countControlDirectories counts the private directories disposable sessions
// create, so a test can prove an error path released the one it made.
func countControlDirectories(t *testing.T) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "codefly-cli-*"))
	if err != nil {
		t.Fatalf("scan control directories: %v", err)
	}
	return len(matches)
}

// copyWorkspace makes an independent checkout of the fixture. Both copies keep
// the same workspace name, which is the state that used to collapse two
// sessions onto one control channel.
func copyWorkspace(t *testing.T) string {
	t.Helper()
	destination := t.TempDir()
	command := exec.Command("cp", "-R", "testdata/session-workspace/.", destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("copy workspace fixture: %v\n%s", err, output)
	}
	return filepath.Join(destination, "modules", "app", "services", "api")
}

// sessionProcess is one driver process holding a live dependency session, with
// its own record directory so the control server's log is unambiguous.
type sessionProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	records string
}

func startSessionProcess(t *testing.T, driver, controlServer, serviceDir string) *sessionProcess {
	t.Helper()
	records := t.TempDir()
	cmd := exec.Command(driver)
	cmd.Dir = serviceDir
	cmd.Env = append(os.Environ(),
		"CODEFLY_BINARY="+controlServer,
		"FAKE_CONTROL_RECORD_DIR="+records,
		"FAKE_CONTROL_MODE=",
		"CODEFLY__RUNTIME_CONTEXT=native")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("driver stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("driver stdout: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start driver: %v", err)
	}
	process := &sessionProcess{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), records: records}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line := process.line(t); line != "READY" {
		t.Fatalf("driver said %q, want READY", line)
	}
	return process
}

func (p *sessionProcess) line(t *testing.T) string {
	t.Helper()
	line, err := p.stdout.ReadString('\n')
	if err != nil {
		t.Fatalf("read from driver: %v", err)
	}
	return strings.TrimSpace(line)
}

func (p *sessionProcess) stop(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, "STOP\n"); err != nil {
		t.Fatalf("signal driver: %v", err)
	}
	if line := p.line(t); line != "STOPPED" {
		t.Fatalf("driver said %q, want STOPPED", line)
	}
}

func (p *sessionProcess) record(t *testing.T) string {
	t.Helper()
	return readRecord(t, onlyRecord(t, p.records))
}

type sessionIdentity struct {
	session string
	socket  string
	scope   string
}

// identity reads what the SDK actually handed this driver's child: the session
// it minted, the socket it told the child to bind, and the naming scope it put
// on the command line.
func (p *sessionProcess) identity(t *testing.T) sessionIdentity {
	t.Helper()
	for line := range strings.SplitSeq(p.record(t), "\n") {
		if !strings.HasPrefix(line, "identity ") {
			continue
		}
		identity := sessionIdentity{}
		for _, field := range strings.Fields(strings.TrimPrefix(line, "identity ")) {
			key, value, _ := strings.Cut(field, "=")
			switch key {
			case "session":
				identity.session = value
			case "socket":
				identity.socket = value
			case "scope":
				identity.scope = value
			}
		}
		return identity
	}
	t.Fatalf("the control server recorded no session identity in %s", p.records)
	return sessionIdentity{}
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

// TestConcurrentProcessesShareOneWarmStack is the parallel-package case: four
// processes keyed to one reusable stack, started at once with no coordination
// of their own. Exactly one may spawn it and the rest must attach to what it
// published, which is the outcome `go test -p 1` used to buy.
//
// Each driver reports the endpoints it resolved, and the fixture CLI binds an
// ephemeral port: four processes that agreed on one stack report one address,
// and four that each spawned their own report four.
func TestConcurrentProcessesShareOneWarmStack(t *testing.T) {
	// A binary of this test's own, so the cleanup below cannot reach a stack
	// another test in this package started.
	binary := buildFixture(t, "./testdata/testcli")
	driver := buildFixture(t, "./testdata/sessiondriver")
	service := fixtureDir(t, "alpha", "modules", "shop", "services", "web")
	home := warmCacheHome(t)
	scope := uniqueScope(t)
	// The warm stack outlives every driver — that is what keep-running means —
	// so this test owns killing the CLI its drivers left running.
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", binary).Run() })

	const drivers = 4
	type outcome struct {
		endpoints string
		err       error
	}
	outcomes := make([]outcome, drivers)
	var wait sync.WaitGroup
	for i := range outcomes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			outcomes[i].endpoints, outcomes[i].err = runWarmDriver(driver, binary, service, home, scope)
		}()
	}
	wait.Wait()

	for i, got := range outcomes {
		if got.err != nil {
			t.Fatalf("driver %d: %v", i, got.err)
		}
		if got.endpoints == "" {
			t.Fatalf("driver %d resolved no endpoint", i)
		}
		if got.endpoints != outcomes[0].endpoints {
			t.Fatalf("driver %d resolved %q and driver 0 resolved %q: they did not share one warm stack",
				i, got.endpoints, outcomes[0].endpoints)
		}
	}
}

// runWarmDriver runs one driver against the reusable stack named by scope and
// returns the endpoints it resolved. It takes no *testing.T because several of
// these run at once and only the calling goroutine may fail the test.
func runWarmDriver(driver, binary, service, home, scope string) (string, error) {
	cmd := exec.Command(driver)
	cmd.Dir = service
	cmd.Env = append(os.Environ(),
		"CODEFLY_BINARY="+binary,
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"SESSION_SCOPE="+scope,
		"CODEFLY__RUNTIME_CONTEXT=native")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)
	// A driver that attached to the warm stack announces it before READY.
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return "", fmt.Errorf("read from driver: %w", readErr)
		}
		if strings.TrimSpace(line) == "READY" {
			break
		}
	}
	endpoints, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read endpoints from driver: %w", err)
	}
	if _, err := io.WriteString(stdin, "STOP\n"); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "STOPPED" {
		return "", fmt.Errorf("driver said %q (%v), want STOPPED", strings.TrimSpace(line), err)
	}
	return strings.TrimSpace(endpoints), nil
}

// warmCacheHome is a private cache root for a reusable session, kept short: a
// warm control directory hangs off the user cache directory, and a per-user
// temporary path is long enough on macOS to push the socket inside it past the
// platform's sun_path budget.
func warmCacheHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "cfhome-")
	if err != nil {
		t.Fatalf("create warm cache home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return home
}
