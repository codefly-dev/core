package dependencies

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/sdk/session"
	"github.com/codefly-dev/core/wool"

	"github.com/gofrs/flock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Dependencies manages a running set of codefly-managed service
// dependencies. The underlying CLI subprocess runs in its own process
// group via managedProcess, so Destroy can tear down the whole native
// tree — the CLI and its spawned agents — in one bounded pass. Docker
// containers those agents created are not part of that group: they are
// owned by the Docker daemon and only the CLI's own DestroyFlow removes
// them, which is why Destroy sends that RPC before killing the group.
// Without the group, `go test` also hangs on WaitDelay waiting for
// inherited stdout/stderr FDs.
type Dependencies struct {
	proc           *managedProcess
	cli            v0.CLIClient
	conn           *grpc.ClientConn
	control        *session.Control
	runtimeContext *basev0.RuntimeContext
	keepRunning    bool
	attached       bool
	inherited      bool

	// dir anchors the session: identity is resolved from it rather than from
	// the process working directory, which a caller may change at any time.
	dir string

	// controlAddress is the CLI control channel this session spawned a server
	// on and holds against other sessions in this process. Empty for a session
	// that attached to a server someone else owns.
	controlAddress string

	// startDone is the caller's context while the session is still starting,
	// and nil once it is live. A caller that abandons a start — a test that
	// times out, a cancelled context — leaves a session that will never own
	// anything, and it must not hold the process environment against the next
	// one. Guarded by mu.
	startDone <-chan struct{}

	// mu guards the lazily resolved identity, the resolved environment and the
	// last readiness snapshot.
	mu          sync.Mutex
	identity    *resolvedIdentity
	environment *sessionEnvironment

	// readiness is the per-service view the CLI reported on the most recent
	// poll, kept so a caller can attribute a readiness overrun to a dependency
	// after WaitForReady has returned.
	readiness []*v0.ServiceReadiness

	// owned records the variables this session installed into os.Environ.
	// It is guarded by globalEnvironment.mu, the same lock that serializes
	// SDK-owned mutation of the process environment.
	owned []ownedVariable
}

type Option struct {
	Debug                    bool
	Timeout                  time.Duration
	CodeflyBinary            string
	NamingScope              string
	Fixture                  string
	RunProfile               string
	Silents                  []string
	ExcludedDependencies     []string
	WorkspaceConfigurations  []resources.WorkspaceConfigurationOverride
	ServiceConfigurations    []resources.ServiceConfigurationOverride
	DependencyHome           string
	KeepRunning              bool
	SharedControlChannel     bool
	Directory                string
	Service                  string
	CommandScopedEnvironment bool
}

type OptionFunc func(*Option)

func WithDebug() OptionFunc {
	return func(o *Option) {
		o.Debug = true
	}
}

func WithTimeout(timeout time.Duration) OptionFunc {
	return func(o *Option) {
		o.Timeout = timeout
	}
}

// WithCodeflyBinary pins the executable used to own the dependency flow.
// Process-composition boundaries such as the Codefly CLI use this option to
// guarantee that nested startup cannot silently select a different release
// from PATH. SDK callers that omit it retain the CODEFLY_BINARY override and
// the normal PATH lookup.
func WithCodeflyBinary(path string) OptionFunc {
	return func(o *Option) {
		o.CodeflyBinary = path
	}
}

func WithNamingScope(scope string) OptionFunc {
	return func(o *Option) {
		o.NamingScope = scope
	}
}

// WithFixture selects a real Codefly module fixture for the dependency stack.
// The CLI propagates the selection to every service through the standard
// CODEFLY__FIXTURE runtime configuration.
func WithFixture(fixture string) OptionFunc {
	return func(o *Option) {
		o.Fixture = fixture
	}
}

// WithRunProfile forwards a workspace profile name to Codefly, which owns
// profile validation and resolution for the dependency flow.
func WithRunProfile(profile string) OptionFunc {
	return func(o *Option) {
		o.RunProfile = profile
	}
}

func WithSilence(uniques ...string) OptionFunc {
	return func(o *Option) {
		o.Silents = uniques
	}
}

func WithExcludedDependencies(uniques ...string) OptionFunc {
	return func(o *Option) {
		o.ExcludedDependencies = append(o.ExcludedDependencies, uniques...)
	}
}

// WithDependencyHome selects HOME only for the spawned Codefly dependency
// process and the infrastructure agents it owns. The caller keeps its own
// HOME, PATH, and developer toolchain environment unchanged. This is useful
// when a native dependency stores runtime state through os.UserCacheDir and
// concurrent dependency stacks need separate cache roots.
//
// The directory must be absolute. Codefly creates no directory implicitly;
// callers own its lifecycle and permissions.
func WithDependencyHome(home string) OptionFunc {
	return func(o *Option) {
		o.DependencyHome = home
	}
}

// WithWorkspaceConfiguration supplies one non-secret workspace value for this
// dependency invocation without changing the developer's persisted Codefly
// configuration profile.
func WithWorkspaceConfiguration(name, key, value string) OptionFunc {
	return withWorkspaceConfigurationValue(name, key, value, false)
}

// WithWorkspaceSecret supplies one secret workspace value for this dependency
// invocation without changing the developer's persisted Codefly configuration
// profile. The value is carried only in the spawned CLI environment and is not
// included in command arguments or logs.
func WithWorkspaceSecret(name, key, value string) OptionFunc {
	return withWorkspaceConfigurationValue(name, key, value, true)
}

func withWorkspaceConfigurationValue(name, key, value string, secret bool) OptionFunc {
	return func(o *Option) {
		o.WorkspaceConfigurations = append(o.WorkspaceConfigurations, resources.WorkspaceConfigurationOverride{
			Name: name, Key: key, Value: value, Secret: secret,
		})
	}
}

// WithServiceConfiguration supplies one non-secret value owned by a concrete
// module/service for this dependency invocation without changing the
// developer's persisted Codefly configuration profile.
func WithServiceConfiguration(service, name, key, value string) OptionFunc {
	return withServiceConfigurationValue(service, name, key, value, false)
}

// WithServiceSecret supplies one secret value owned by a concrete
// module/service for this dependency invocation. The value is carried only in
// the spawned CLI environment and is not included in command arguments or
// logs.
func WithServiceSecret(service, name, key, value string) OptionFunc {
	return withServiceConfigurationValue(service, name, key, value, true)
}

func withServiceConfigurationValue(service, name, key, value string, secret bool) OptionFunc {
	return func(o *Option) {
		o.ServiceConfigurations = append(o.ServiceConfigurations, resources.ServiceConfigurationOverride{
			Service: service, Name: name, Key: key, Value: value, Secret: secret,
		})
	}
}

// WithKeepRunning keeps the spawned Codefly dependency stack alive when
// Dependencies.Stop or Dependencies.Destroy is called. A later WithDependencies
// call using the same naming scope first tries to attach to that warm CLI
// server before starting a new stack.
func WithKeepRunning() OptionFunc {
	return func(o *Option) {
		o.KeepRunning = true
	}
}

// WithSharedControlChannel opts out of session isolation and puts the control
// channel back on the workspace-derived port that every invocation of the same
// workspace name computes. Concurrent test packages and same-name worktrees
// then contend for one port, and the SDK cannot prove the server answering it
// is the child it started.
//
// It exists for one case: driving a Codefly CLI that predates the isolated
// session contract. Isolated sessions are the default, and an old CLI is
// reported as an explicit error rather than silently downgraded.
func WithSharedControlChannel() OptionFunc {
	return func(o *Option) {
		o.SharedControlChannel = true
	}
}

// WithDirectory anchors the session to an absolute directory instead of the
// process working directory. The module and service owning that directory
// become the session's identity, the spawned Codefly process runs there, and
// neither follows a later chdir. Callers that drive several dependency stacks
// from one process use it to keep each stack on its own service.
func WithDirectory(dir string) OptionFunc {
	return func(o *Option) {
		o.Directory = dir
	}
}

// WithService anchors the session to a service named the way the CLI names it,
// "<module>/<service>", resolved through the workspace found up from the
// working directory. The module half is required: a bare service name is
// refused rather than guessed at across modules.
//
// It is the option for a caller that sits outside any service — a
// solution-level test package, which owns no service.codefly.yaml of its own —
// and would otherwise have to compute the on-disk path of the service it
// drives and pass it to WithDirectory.
func WithService(unique string) OptionFunc {
	return func(o *Option) {
		o.Service = unique
	}
}

// WithCommandScopedEnvironment keeps the session out of os.Environ. The
// resolved values are reachable through Dependencies.Environ, which produces a
// child-process environment, and through Dependencies.Connection. This is the
// way to run several dependency sessions in one process — parallel tests, in
// particular — because the process environment holds one session at a time.
func WithCommandScopedEnvironment() OptionFunc {
	return func(o *Option) {
		o.CommandScopedEnvironment = true
	}
}

// WithDependencies starts all dependencies declared in the current service's
// service.codefly.yaml using the codefly CLI. This handles arbitrarily deep
// dependency graphs — the CLI resolves and starts everything in order.
//
// Connection strings are injected as environment variables (the standard
// codefly pattern). Use Connection() or os.Getenv() to retrieve them.
//
// Usage:
//
//	deps, err := sdk.WithDependencies(ctx)
//	deps, err := sdk.WithDependencies(ctx, sdk.WithDebug())
//	deps, err := sdk.WithDependencies(ctx, sdk.WithTimeout(30*time.Second))
func WithDependencies(ctx context.Context, opts ...OptionFunc) (*Dependencies, error) {
	opt := &Option{
		Debug:   false,
		Timeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(opt)
	}
	if err := validateDependencyOptions(opt); err != nil {
		return nil, err
	}
	// Borrowing the parent runtime's dependencies is decided before the session
	// is anchored: that path spawns nothing, so it must not be refused by a
	// directory lookup made on behalf of a stack it will never start.
	if hasManagedDependencyEnvironment(os.Environ()) {
		if hasInvocationConfigurationOverrides(opt) {
			return nil, fmt.Errorf("invocation-scoped configurations cannot replace values in dependencies owned by the parent Codefly runtime")
		}
		wool.Get(ctx).In("sdk.WithDependencies").
			Debug("reusing dependencies injected by the managed Codefly runtime")
		dir, err := inheritedDirectory(ctx, opt)
		if err != nil {
			return nil, err
		}
		return &Dependencies{
			runtimeContext: resources.RuntimeContextFromEnv(),
			inherited:      true,
			dir:            dir,
		}, nil
	}
	dir, err := sessionDirectory(ctx, opt)
	if err != nil {
		return nil, err
	}
	channel, err := newControlChannel(ctx, dir, opt)
	if err != nil {
		return nil, err
	}
	// Own the private directory from the moment it exists. Every error path
	// below has to release it, including the ones that return before a child
	// is ever spawned — otherwise a rejected option set orphans a directory in
	// the temporary root on every call.
	success := false
	defer func() {
		if !success {
			channel.discard()
		}
	}()
	// Held across the attach-or-spawn decision below, so concurrent processes
	// keyed to one warm stack take turns deciding rather than racing to the
	// same conclusion.
	unlockSetup, err := channel.lockSetup(ctx, setupLockWait(opt.Timeout))
	if err != nil {
		return nil, err
	}
	defer unlockSetup()
	args := dependencyCommandArguments(opt, channel.scope)

	if opt.KeepRunning {
		if deps, err := attachDependencies(ctx, channel, dir, opt, unlockSetup); err == nil {
			success = true
			return deps, nil
		} else {
			// Attach miss is the common case (no warm server yet) but
			// can also mask a real transport problem (stale socket,
			// permission, dial timeout). Log so operators can see why
			// keep-running spawned a fresh stack instead of attaching.
			wool.Get(ctx).In("sdk.WithDependencies").
				Debug("attach to existing CLI server failed; starting new stack",
					wool.Field("target", channel.target),
					wool.Field("error", err.Error()))
		}
		// The receipt and socket left by the previous session belong to the
		// SDK, which owns this directory — not to the child. Dropping the
		// receipt bounds how long its secret survives on disk; clearing the
		// socket refuses outright when a server still answers there, so a
		// second stack can never come up over the first one's containers.
		//
		// A shared control channel owns no directory: there is no receipt and
		// no socket of ours to clear, and a server still holding the workspace
		// port is refused by the child's own failure to bind it.
		if channel.isolated() {
			if err := channel.control.RemoveReceipt(); err != nil {
				return nil, err
			}
			if err := channel.control.ClearStaleSocket(); err != nil {
				return nil, err
			}
		}
	}

	l := &Dependencies{
		runtimeContext: resources.RuntimeContextFromEnv(),
		keepRunning:    opt.KeepRunning,
		dir:            dir,
		controlAddress: channel.target,
		startDone:      ctx.Done(),
	}
	// Claim what this session must own exclusively before provisioning anything.
	// A competing session then fails in milliseconds instead of starting a full
	// dependency stack — containers included — only to be refused and torn down.
	if err = claimControlAddress(channel.target, dir); err != nil {
		return nil, err
	}
	// Registered before the second claim so a failure there — or anywhere
	// below — cannot leave this session holding the control channel.
	defer func() {
		if !success {
			l.ReleaseEnvironment()
			releaseControlAddress(channel.target)
		}
	}()
	if !opt.CommandScopedEnvironment {
		if err = claimGlobalEnvironment(l); err != nil {
			return nil, err
		}
	}

	cmd := exec.CommandContext(ctx, codeflyBinary(opt), args...)
	cmd.Dir = dir
	// ARCHITECTURE: the SDK owns the control channel for the child it starts.
	// It hands the child the exact endpoint to bind instead of asking two
	// separately versioned binaries to reproduce the same hash algorithm. An
	// isolated session hands over a socket path inside a directory only this
	// process can write, so the child cannot lose a bind race and no unrelated
	// server can be mistaken for it.
	processEnvironment, err := withWorkspaceConfigurationOverrides(os.Environ(), opt.WorkspaceConfigurations)
	if err != nil {
		return nil, err
	}
	processEnvironment, err = withServiceConfigurationOverrides(processEnvironment, opt.ServiceConfigurations)
	if err != nil {
		return nil, err
	}
	processEnvironment = withDependencyHome(processEnvironment, opt.DependencyHome)
	if channel.isolated() {
		cmd.Env = channel.session.Inject(processEnvironment, channel.control.Socket)
	} else {
		cmd.Env = withCLIServerPort(processEnvironment, channel.target)
	}
	wool.Get(ctx).In("sdk.WithDependencies").Debug("starting CLI subprocess", wool.Field("cmd", cmd.String()))

	proc, err := startManaged(ctx, cmd)
	if err != nil {
		return nil, err
	}
	// Echo the CLI's output to the parent's stdout/stderr in drain
	// goroutines. This both keeps the pipes unblocked AND makes the
	// child's FDs independent of os.Stdout so `go test` can exit.
	proc.Echo()

	// Tear down the spawned CLI process group (and its supervise/signal/echo
	// goroutines) plus any open connection on every post-spawn error path.
	// Disarmed once we return the live Dependencies to the caller.
	var conn *grpc.ClientConn
	defer func() {
		if !success {
			if conn != nil {
				_ = conn.Close()
			}
			if killErr := proc.Kill(); killErr != nil {
				wool.Get(ctx).In("sdk.WithDependencies").
					Warn("could not tear down the CLI process group", wool.Field("error", killErr))
			}
		}
	}()

	if channel.isolated() {
		if err := waitForControlSocket(ctx, proc, channel.control.Socket, opt.Timeout); err != nil {
			return nil, err
		}
	}

	conn, err = grpc.NewClient(channel.target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cannot create gRPC client for %s: %w", channel.target, err)
	}

	// grpc.NewClient is lazy — explicitly trigger the connection and wait
	// for it to become ready, matching the pattern in agents/manager/loader.go.
	connectCtx, connectCancel := context.WithTimeout(ctx, opt.Timeout)
	defer connectCancel()
	go func() {
		select {
		case <-proc.Done():
			connectCancel()
		case <-connectCtx.Done():
		}
	}()

	conn.Connect()
	if !waitForReady(connectCtx, conn) {
		select {
		case <-proc.Done():
			if exitErr := proc.WaitError(); exitErr != nil {
				return nil, fmt.Errorf("CLI subprocess exited before its gRPC server became ready: %w", exitErr)
			}
			return nil, fmt.Errorf("CLI subprocess exited before its gRPC server became ready")
		default:
		}
		return nil, fmt.Errorf("gRPC connection to CLI server at %s did not become ready within %s", channel.target, opt.Timeout)
	}

	cli := v0.NewCLIClient(conn)
	// Ownership before anything else: readiness polling, environment reads and
	// the Stop/Destroy RPCs all act on whatever answers this channel, so the
	// peer proves it is this session's child before any of them run.
	if err := channel.verifyOwnership(ctx, cli, channel.session, opt.Timeout); err != nil {
		return nil, err
	}
	_, err = cli.Ping(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("CLI server ping failed: %w", err)
	}
	l.proc = proc
	l.cli = cli
	l.conn = conn
	l.control = channel.control
	l.started()
	err = l.WaitForReady(ctx, opt)
	if err != nil {
		return nil, err
	}
	if err = l.installEnvironment(ctx, opt); err != nil {
		return nil, err
	}
	if opt.KeepRunning && channel.isolated() {
		// Record ownership only once the stack is actually usable, so a later
		// invocation never attaches to the remains of a failed start.
		if err := channel.control.WriteReceipt(session.Receipt{
			ID:          channel.session.ID,
			Secret:      channel.session.Secret,
			Fingerprint: channel.fingerprint,
		}); err != nil {
			return nil, err
		}
	}
	success = true
	return l, nil
}

// controlAddresses are the CLI control addresses this process has spawned a
// server on. network.CLIServerPort derives the address from the workspace name
// alone — and returns CODEFLY_CLI_SERVER_PORT verbatim when the caller's
// environment pins one, which collapses every session onto a single address —
// so two sessions in one process can resolve the same address. The second
// spawn's CLI cannot bind it and the SDK would silently drive the first
// session's server instead, destroying its flow on Stop. Claiming the address
// turns that into an explicit failure before anything is spawned.
//
// Only spawned stacks claim: attaching to a kept-running server (WithKeepRunning)
// is deliberate sharing of one server between sessions.
var controlAddresses struct {
	mu    sync.Mutex
	inUse map[string]string
}

func claimControlAddress(addr string, dir string) error {
	controlAddresses.mu.Lock()
	defer controlAddresses.mu.Unlock()
	if owner, held := controlAddresses.inUse[addr]; held {
		return fmt.Errorf("dependency session %s already owns the Codefly control channel at %s; "+
			"give this session its own scope with WithNamingScope, or attach to the existing stack with WithKeepRunning",
			owner, addr)
	}
	if controlAddresses.inUse == nil {
		controlAddresses.inUse = make(map[string]string)
	}
	controlAddresses.inUse[addr] = dir
	return nil
}

func releaseControlAddress(addr string) {
	if addr == "" {
		return
	}
	controlAddresses.mu.Lock()
	defer controlAddresses.mu.Unlock()
	delete(controlAddresses.inUse, addr)
}

// sessionDirectory is the absolute directory a session is anchored to: the
// directory of the service the caller named, the one it pinned, or the working
// directory at the moment the session starts.
func sessionDirectory(ctx context.Context, opt *Option) (string, error) {
	if opt.Service != "" {
		return serviceDirectory(ctx, opt.Service)
	}
	if opt.Directory != "" {
		return opt.Directory, nil
	}
	return os.Getwd()
}

// serviceDirectory resolves "<module>/<service>" through the workspace owning
// the working directory. Only the workspace is found by walking up, so a caller
// that owns no service — which is the whole point of naming one — resolves the
// same identity from anywhere inside the workspace.
func serviceDirectory(ctx context.Context, unique string) (string, error) {
	reference, err := resources.ParseServiceWithOptionalModule(unique)
	if err != nil {
		return "", err
	}
	if reference.Module == "" {
		return "", fmt.Errorf("service %q must name its module, as <module>/<service>", unique)
	}
	from, err := os.Getwd()
	if err != nil {
		return "", err
	}
	workspace, err := resources.FindWorkspaceUpFrom(ctx, from)
	if err != nil {
		return "", err
	}
	if workspace == nil {
		return "", fmt.Errorf("no Codefly workspace found from %s, so the service %s cannot be resolved", from, unique)
	}
	service, err := workspace.LoadService(ctx, reference)
	if err != nil {
		return "", fmt.Errorf("cannot resolve service %s in workspace %s: %w", unique, workspace.Name, err)
	}
	return service.Dir(), nil
}

// inheritedDirectory anchors a borrowed session. It spawns nothing, so the
// directory only names the identity Service and Module report: a service name
// that does not resolve here — a parent runtime may run the test outside the
// workspace that name belongs to — falls back to the working directory rather
// than failing a session whose dependencies are already live.
func inheritedDirectory(ctx context.Context, opt *Option) (string, error) {
	dir, err := sessionDirectory(ctx, opt)
	if err == nil {
		return dir, nil
	}
	if opt.Service == "" {
		return "", err
	}
	wool.Get(ctx).In("sdk.WithDependencies").
		Debug("named service does not resolve here; anchoring the borrowed session to the working directory",
			wool.Field("service", opt.Service), wool.Field("error", err.Error()))
	return os.Getwd()
}

// installEnvironment resolves everything the session projects and, unless the
// caller asked for a command-scoped environment, injects it into os.Environ.
func (l *Dependencies) installEnvironment(ctx context.Context, opt *Option) error {
	env, err := l.resolveEnvironment(ctx)
	if err != nil {
		return err
	}
	l.setResolved(env)
	if opt.CommandScopedEnvironment {
		return nil
	}
	return l.apply(env)
}

func validateDependencyOptions(opt *Option) error {
	if opt.KeepRunning && hasInvocationConfigurationOverrides(opt) {
		return fmt.Errorf("invocation-scoped configurations cannot be combined with a reusable dependency stack")
	}
	if opt.DependencyHome != "" && !filepath.IsAbs(opt.DependencyHome) {
		return fmt.Errorf("dependency home must be absolute: %s", opt.DependencyHome)
	}
	if opt.CodeflyBinary != "" && !filepath.IsAbs(opt.CodeflyBinary) {
		return fmt.Errorf("Codefly binary must be absolute: %s", opt.CodeflyBinary)
	}
	if opt.Directory != "" && !filepath.IsAbs(opt.Directory) {
		return fmt.Errorf("session directory must be absolute: %s", opt.Directory)
	}
	if opt.Service != "" && opt.Directory != "" {
		return fmt.Errorf("WithService(%s) and WithDirectory(%s) both anchor the session: pass one", opt.Service, opt.Directory)
	}
	return nil
}

func hasInvocationConfigurationOverrides(opt *Option) bool {
	return len(opt.WorkspaceConfigurations) > 0 || len(opt.ServiceConfigurations) > 0
}

func dependencyCommandArguments(opt *Option, scope string) []string {
	args := []string{"run", "service"}
	if opt.Debug {
		args = append(args, "-d")
	}
	if scope != "" {
		args = append(args, "--naming-scope", scope)
	}
	if opt.Fixture != "" {
		args = append(args, "--fixture", opt.Fixture)
	}
	if opt.RunProfile != "" {
		args = append(args, "--profile", opt.RunProfile)
	}
	if len(opt.Silents) > 0 {
		args = append(args, "--silent", strings.Join(opt.Silents, ","))
	}
	if len(opt.ExcludedDependencies) > 0 {
		args = append(args, "--exclude-dependency", strings.Join(opt.ExcludedDependencies, ","))
	}

	// Test-owned dependency stacks run in independent package processes, so
	// their in-memory RuntimeManagers cannot coordinate deterministic named
	// port allocations. Ask the CLI to use OS-probed ephemeral ports for these
	// short-lived flows. Normal CLI stable/dev flows retain named ports.
	//
	// --headless prevents the CLI from trying to open /dev/tty for
	// interactive context selection. Always needed when running as a
	// subprocess (go test, CI, MCP, pipes).
	args = append(args, "--temporary-ports", "--exclude-root", "--cli-server", "--headless")
	return args
}

// hasManagedDependencyEnvironment recognizes the environment installed by a
// parent Codefly service runner. Tests executed through a service agent already
// have live dependency endpoints; starting another nested flow would duplicate
// containers and contend for the parent's assigned ports.
//
// CODEFLY__RUNNING alone is intentionally insufficient because --stand-alone
// services may not have dependencies. Reuse is allowed only when the runtime
// also injected at least one non-empty typed endpoint. Direct `go test` remains
// unchanged: SetEnvironment does not synthesize the parent-runner marker.
func hasManagedDependencyEnvironment(environment []string) bool {
	running := false
	hasEndpoint := false
	endpointPrefix := resources.EndpointPrefix + "__"
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch {
		case name == resources.RunningPrefix:
			running = strings.EqualFold(strings.TrimSpace(value), "true")
		case strings.HasPrefix(name, endpointPrefix) && strings.TrimSpace(value) != "":
			hasEndpoint = true
		}
	}
	return running && hasEndpoint
}

// controlChannel is the transport the SDK drives the child CLI over, together
// with the identity it holds the peer to and the naming scope the child must
// stamp onto the resources it creates.
type controlChannel struct {
	target      string
	scope       string
	session     *session.Session
	control     *session.Control
	fingerprint string
}

// newControlChannel selects the control endpoint for one invocation.
//
// A disposable session — the default — mints a fresh identity from
// crypto/rand and owns a private directory for its socket. Nothing about the
// endpoint is derived from the workspace name, so two test packages in one
// workspace, or two worktrees that share a workspace name, cannot select the
// same channel even with no naming flags. The identity also becomes the naming
// scope, which is how it reaches state directories, container names and log
// roots: the caller's own scope stays in front of it as a human label.
//
// A reusable session (WithKeepRunning) is the explicit stable-developer mode.
// Its directory is keyed by a reuse fingerprint rather than by randomness, and
// its naming scope stays the developer's label so a warm stack keeps the
// containers and state it had.
func newControlChannel(ctx context.Context, dir string, opt *Option) (*controlChannel, error) {
	if opt.SharedControlChannel {
		return &controlChannel{
			target: cliServerAddress(ctx, dir, opt.NamingScope),
			scope:  opt.NamingScope,
		}, nil
	}
	invocation, err := session.New()
	if err != nil {
		return nil, err
	}
	if opt.KeepRunning {
		fingerprint := reuseFingerprint(ctx, dir, opt)
		control, err := session.OpenControl(fingerprint[:16])
		if err != nil {
			return nil, err
		}
		return &controlChannel{
			target:      control.Target(),
			scope:       opt.NamingScope,
			session:     invocation,
			control:     control,
			fingerprint: fingerprint,
		}, nil
	}
	control, err := session.NewControl()
	if err != nil {
		return nil, err
	}
	return &controlChannel{
		target:  control.Target(),
		scope:   scopeWithSession(opt.NamingScope, invocation),
		session: invocation,
		control: control,
	}, nil
}

func (c *controlChannel) isolated() bool {
	return c.control != nil
}

// discard removes a disposable session's private directory. A reusable
// session's directory is keyed by fingerprint and survives for the next
// attach, so it is left alone.
func (c *controlChannel) discard() {
	if c.control != nil && c.fingerprint == "" {
		_ = c.control.Remove()
	}
}

// setupLockRetry is how often a waiting session re-tries the reusable
// directory's lock. Startup of a full dependency stack is measured in seconds,
// so a poll this cheap is invisible next to it.
const setupLockRetry = 50 * time.Millisecond

// setupLockTimeout is the floor on how long a session waits for another process
// to finish deciding. It has to cover a cold start of the shared stack, which
// runs into minutes when that stack pulls images, so it is far longer than the
// per-phase opt.Timeout.
const setupLockTimeout = 5 * time.Minute

// setupLockWait bounds the wait, and is finite because the alternative is
// worse: a holder that wedges mid-start — a CLI that accepts an RPC it never
// answers — would otherwise hang every other process until the test binary's
// own panic timeout, leaving a stack trace that points at this lock rather than
// at the process that stalled. A caller that budgeted more than the floor for
// its own start is waiting on a stack that slow, so its timeout wins.
func setupLockWait(timeout time.Duration) time.Duration {
	if timeout > setupLockTimeout {
		return timeout
	}
	return setupLockTimeout
}

// lockSetup serializes the attach-or-spawn decision for a reusable session
// against every other process keyed to the same warm directory. Only that
// decision is held, not the session: once the owner has published its receipt
// the lock is released and every waiter attaches to the one warm stack, which
// is what lets test packages share it while still running concurrently.
//
// Without it the window between "no receipt yet" and "receipt written" is open:
// a second process reads no receipt, concludes there is nothing to attach to,
// and either refuses because the first child has since bound the socket, or
// spawns a second CLI over the first one's containers — which the SDK then
// sees as an EOF on the first RPC it makes.
//
// A disposable session owns a directory nothing else can name, so it takes no
// lock. A holder that dies releases the lock along with its file descriptors;
// one that wedges is bounded by wait.
func (c *controlChannel) lockSetup(ctx context.Context, wait time.Duration) (func(), error) {
	if c.fingerprint == "" {
		return func() {}, nil
	}
	lock := flock.New(c.control.SetupLockPath(), flock.SetPermissions(0o600))
	bounded, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	locked, err := lock.TryLockContext(bounded, setupLockRetry)
	if err != nil {
		_ = lock.Close()
		// The caller's own context expiring is its business; this deadline
		// expiring means the process that holds the lock never let go.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("timed out after %s waiting for another process to finish starting the reusable dependency session at %s: the process holding it may be wedged", wait, c.control.Directory)
		}
		return nil, fmt.Errorf("waiting for the reusable dependency session at %s: %w", c.control.Directory, err)
	}
	if !locked {
		_ = lock.Close()
		return nil, fmt.Errorf("could not lock the reusable dependency session at %s", c.control.Directory)
	}
	// Released once: the attach path hands the lock back as soon as the
	// decision is made, and the deferred release then has nothing left to do.
	var released sync.Once
	return func() {
		released.Do(func() {
			_ = lock.Unlock()
			_ = lock.Close()
		})
	}, nil
}

// verifyOwnership makes the peer prove it holds this invocation's secret. A
// shared control channel has no identity to check, so it is accepted as-is —
// that is precisely the guarantee WithSharedControlChannel gives up.
func (c *controlChannel) verifyOwnership(ctx context.Context, cli v0.CLIClient, owner *session.Session, timeout time.Duration) error {
	if !c.isolated() {
		return nil
	}
	challenge, err := session.Challenge()
	if err != nil {
		return err
	}
	// The handshake is the one call made to a peer that has not yet been
	// judged trustworthy, so it carries its own deadline: a server that
	// completes the health check and then never answers must not be able to
	// wedge the caller indefinitely.
	handshakeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := cli.SessionHandshake(handshakeCtx, &v0.SessionHandshakeRequest{
		SessionId:       owner.ID,
		Challenge:       challenge,
		ProtocolVersion: session.ProtocolVersion,
	})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return fmt.Errorf("the codefly CLI serving %s does not implement the session handshake, so this dependency session cannot be isolated: upgrade codefly, or pass sdk.WithSharedControlChannel() to accept a shared, unauthenticated control channel", c.target)
		}
		return fmt.Errorf("session handshake with the CLI server at %s failed: %w", c.target, err)
	}
	// Version first: the proof is domain-separated by protocol version, so
	// skew fails verification too. Reporting it as an upgrade problem keeps a
	// routine version mismatch from being read as tampering.
	if got := resp.GetProtocolVersion(); got != session.ProtocolVersion {
		return fmt.Errorf("the CLI server at %s speaks session protocol version %d and this SDK speaks %d: upgrade whichever side is behind", c.target, got, session.ProtocolVersion)
	}
	if resp.GetSessionId() != owner.ID || !owner.VerifyProof(challenge, resp.GetProof()) {
		return fmt.Errorf("the server at %s does not own this dependency session: refusing to drive it", c.target)
	}
	if !slices.Contains(resp.GetCapabilities(), session.IsolatedControlSocketCapability) {
		return fmt.Errorf("the CLI server at %s proved its identity but did not advertise %s, so it does not guarantee an isolated control channel: upgrade codefly, or pass sdk.WithSharedControlChannel()", c.target, session.IsolatedControlSocketCapability)
	}
	return nil
}

// scopeWithSession keeps the caller's naming scope readable while making the
// invocation identity the part that actually guarantees uniqueness.
func scopeWithSession(label string, invocation *session.Session) string {
	if label == "" {
		return invocation.Scope()
	}
	return label + "-" + invocation.Scope()
}

// reuseFingerprint covers everything that changes what a dependency stack
// contains: the workspace and service being run, the dependencies the service
// declares, and the fixture, profile, exclusions and home the invocation asked
// for. Attaching to a warm stack requires a match, so a run with a different
// fixture or profile starts its own stack instead of silently inheriting one
// built from another plan.
func reuseFingerprint(ctx context.Context, dir string, opt *Option) string {
	parts := []string{
		"codefly-session-v" + fmt.Sprint(session.ProtocolVersion),
		workspaceName(ctx, dir),
		opt.NamingScope,
		opt.Fixture,
		opt.RunProfile,
		opt.DependencyHome,
		codeflyBinary(opt),
	}
	// The session's own service, resolved from the directory it is anchored
	// to. Reading it from the process working directory would fingerprint a
	// different service than the one this session runs, and two sessions on
	// different services would then share — or wrongly refuse — a warm stack.
	if identity, err := resolveSessionIdentity(ctx, dir); err == nil && identity != nil {
		svc := identity.service
		parts = append(parts, svc.Reference().String())
		for _, dep := range svc.ServiceDependencies {
			parts = append(parts, dep.Unique())
		}
	}
	parts = append(parts, sortedCopy(opt.ExcludedDependencies)...)
	parts = append(parts, sortedCopy(opt.Silents)...)

	hash := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(hash, "%d:%s\x00", len(part), part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func sortedCopy(values []string) []string {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted
}

func workspaceName(ctx context.Context, dir string) string {
	if ws, err := resources.FindWorkspaceUpFrom(ctx, dir); err == nil && ws != nil {
		return ws.Name
	}
	return ""
}

// waitForControlSocket blocks until the child binds the socket the SDK gave
// it. A CLI that never binds it is reported as a missing capability rather
// than left to time out against a channel nothing will ever answer.
func waitForControlSocket(ctx context.Context, proc *managedProcess, socket string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		select {
		case <-proc.Done():
			if exitErr := proc.WaitError(); exitErr != nil {
				return fmt.Errorf("CLI subprocess exited before it bound its control socket: %w", exitErr)
			}
			return fmt.Errorf("CLI subprocess exited before it bound its control socket")
		case <-ctx.Done():
			return fmt.Errorf("cancelled while waiting for the CLI subprocess to bind its control socket: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("the codefly CLI did not bind the isolated control socket %s within %s: this CLI build does not honor %s, so upgrade codefly or pass sdk.WithSharedControlChannel()", socket, timeout, session.SocketEnvironment)
		case <-ticker.C:
		}
	}
}

func cliServerAddress(ctx context.Context, dir string, namingScope string) string {
	// The CLI derives its gRPC port from the workspace name via
	// network.CLIServerPort. When a naming scope is set (parallel tests),
	// we include it in the name so each scope gets a unique port.
	wsName := ""
	if ws, err := resources.FindWorkspaceUpFrom(ctx, dir); err == nil && ws != nil {
		wsName = ws.Name
	}
	if namingScope != "" {
		wsName = wsName + "-" + namingScope
	}
	port := int(network.CLIServerPort(wsName))
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// withCLIServerPort pins the spawned CLI to the control port already selected
// by the SDK. CODEFLY_CLI_SERVER_PORT is the shared, backwards-compatible
// override understood by every supported CLI; replacing any inherited value
// also guarantees the child receives exactly one authoritative setting.
func withCLIServerPort(environment []string, address string) []string {
	const key = "CODEFLY_CLI_SERVER_PORT"
	prefix := key + "="
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		// cliServerAddress always returns a valid loopback host:port. Keep this
		// helper total so an unexpected future address shape cannot strip an
		// explicit operator override from the child environment.
		return environment
	}
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, prefix+port)
}

func withDependencyHome(environment []string, home string) []string {
	if home == "" {
		return environment
	}
	const prefix = "HOME="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, prefix+home)
}

func withWorkspaceConfigurationOverrides(
	environment []string,
	overrides []resources.WorkspaceConfigurationOverride,
) ([]string, error) {
	if len(overrides) == 0 {
		return environment, nil
	}
	encoded, err := resources.EncodeWorkspaceConfigurationOverrides(overrides)
	if err != nil {
		return nil, fmt.Errorf("prepare invocation-scoped workspace configurations: %w", err)
	}
	key := resources.WorkspaceConfigurationOverridesEnvironment
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, prefix+encoded), nil
}

func withServiceConfigurationOverrides(
	environment []string,
	overrides []resources.ServiceConfigurationOverride,
) ([]string, error) {
	if len(overrides) == 0 {
		return environment, nil
	}
	encoded, err := resources.EncodeServiceConfigurationOverrides(overrides)
	if err != nil {
		return nil, fmt.Errorf("prepare invocation-scoped service configurations: %w", err)
	}
	key := resources.ServiceConfigurationOverridesEnvironment
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, prefix+encoded), nil
}

func codeflyBinary(opt *Option) string {
	if opt != nil {
		if path := strings.TrimSpace(opt.CodeflyBinary); path != "" {
			return path
		}
	}
	if path := strings.TrimSpace(os.Getenv("CODEFLY_BINARY")); path != "" {
		return path
	}
	return "codefly"
}

// attachDependencies joins the stack a reusable control directory already has.
// It calls attached as soon as the peer is proven, which is where the caller
// hands back the setup lock: everything after that point is this session's own
// work, and holding the lock through it would serialize every waiter's
// readiness wait behind the one in front of it.
func attachDependencies(ctx context.Context, channel *controlChannel, dir string, opt *Option, attached func()) (*Dependencies, error) {
	owner, err := warmSessionOwner(channel)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(channel.target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cannot create gRPC client for %s: %w", channel.target, err)
	}
	connectCtx, connectCancel := context.WithTimeout(ctx, attachExistingTimeout(opt.Timeout))
	defer connectCancel()
	conn.Connect()
	if !waitForReady(connectCtx, conn) {
		_ = conn.Close()
		return nil, fmt.Errorf("existing CLI server at %s did not become ready within %s", channel.target, opt.Timeout)
	}
	cli := v0.NewCLIClient(conn)
	if err := channel.verifyOwnership(ctx, cli, owner, attachExistingTimeout(opt.Timeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := cli.Ping(ctx, &emptypb.Empty{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("existing CLI server ping failed: %w", err)
	}
	// A live stack that proved it is ours settles the attach-or-spawn question.
	// A process that takes the lock next reads the same receipt and attaches
	// too; one whose attach fails cannot displace this stack, because clearing
	// a socket a live server still answers is refused.
	attached()
	l := &Dependencies{
		cli:            cli,
		conn:           conn,
		runtimeContext: resources.RuntimeContextFromEnv(),
		keepRunning:    true,
		attached:       true,
		dir:            dir,
	}
	if !opt.CommandScopedEnvironment {
		if err := claimGlobalEnvironment(l); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if err := l.WaitForReady(ctx, opt); err != nil {
		l.ReleaseEnvironment()
		_ = conn.Close()
		return nil, err
	}
	if err := l.installEnvironment(ctx, opt); err != nil {
		l.ReleaseEnvironment()
		_ = conn.Close()
		return nil, err
	}
	fmt.Printf("Attached to existing codefly dependencies at %s\n", channel.target)
	return l, nil
}

// warmSessionOwner returns the identity recorded by the session that owns the
// reusable control directory. Reuse requires both a live receipt and a
// fingerprint match: a warm stack built from another fixture, profile or
// dependency set is not a stack this invocation asked for.
func warmSessionOwner(channel *controlChannel) (*session.Session, error) {
	if !channel.isolated() {
		return nil, nil
	}
	receipt, err := channel.control.ReadReceipt()
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("no reusable dependency session is recorded at %s", channel.control.Directory)
	}
	if receipt.Fingerprint != channel.fingerprint {
		return nil, fmt.Errorf("the reusable dependency session at %s was started from a different plan", channel.control.Directory)
	}
	return &session.Session{ID: receipt.ID, Secret: receipt.Secret}, nil
}

func attachExistingTimeout(startTimeout time.Duration) time.Duration {
	if startTimeout <= 0 || startTimeout > 2*time.Second {
		return 2 * time.Second
	}
	return startTimeout
}

// Connection returns a connection string from environment variables.
func Connection(service, name string) string {
	patterns := []string{
		fmt.Sprintf("CODEFLY__SERVICE_%s__%s__CONNECTION", normalize(service), normalize(name)),
		fmt.Sprintf("CODEFLY__%s__%s__CONNECTION", normalize(service), normalize(name)),
	}
	for _, p := range patterns {
		if v := os.Getenv(p); v != "" {
			return v
		}
	}
	return ""
}

func (l *Dependencies) WaitForReady(ctx context.Context, opt *Option) error {
	if l.inherited {
		return nil
	}
	readyCtx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	for {
		status, err := l.cli.GetFlowStatus(readyCtx, &emptypb.Empty{})
		if err != nil {
			if readyCtx.Err() != nil {
				return l.readinessTimeout(opt.Timeout)
			}
			return err
		}
		l.recordReadiness(status.GetServices())
		if status.GetReady() {
			return nil
		}
		// A failed dependency will not become ready without intervention, so
		// spending the rest of the budget on it only delays the verdict and
		// reports it as a timeout rather than as the failure it is.
		if failed := failedServices(status.GetServices()); len(failed) > 0 {
			return &ReadinessFailure{Services: failed, observedAt: time.Now()}
		}
		select {
		case <-readyCtx.Done():
			return l.readinessTimeout(opt.Timeout)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Readiness returns the per-service view the CLI reported on the most recent
// readiness poll. It is empty when the flow reported none, which is what an
// orchestrator predating per-service status sends.
func (l *Dependencies) Readiness() []*v0.ServiceReadiness {
	l.mu.Lock()
	defer l.mu.Unlock()
	// The entries are copied, not just the slice holding them: a caller that
	// edits what it was handed must not reach the snapshot this session keeps,
	// nor the copy an already-returned error carries.
	snapshot := make([]*v0.ServiceReadiness, 0, len(l.readiness))
	for _, service := range l.readiness {
		snapshot = append(snapshot, proto.Clone(service).(*v0.ServiceReadiness))
	}
	return snapshot
}

func (l *Dependencies) recordReadiness(services []*v0.ServiceReadiness) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.readiness = services
}

func (l *Dependencies) readinessTimeout(timeout time.Duration) error {
	return &ReadinessTimeout{Timeout: timeout, Services: l.Readiness(), observedAt: time.Now()}
}

// ReadinessTimeout reports a flow that did not become ready within its budget.
// It carries the last per-service view so a caller can charge the overrun to
// the dependency that was still starting rather than to the flow as a whole.
type ReadinessTimeout struct {
	Timeout  time.Duration
	Services []*v0.ServiceReadiness

	// observedAt is when the budget ran out. Stage durations are measured
	// against it rather than against the clock at rendering time: a caller
	// that tears the flow down before logging would otherwise read back an
	// elapsed time inflated by its own teardown.
	observedAt time.Time
}

// Pending returns the services that had not reached ready when the budget ran
// out: the ones the overrun is attributable to. A service that reported a hard
// failure never appears here, because WaitForReady stops at that point and
// returns a ReadinessFailure instead of waiting out the budget.
func (e *ReadinessTimeout) Pending() []*v0.ServiceReadiness {
	var pending []*v0.ServiceReadiness
	for _, service := range e.Services {
		if service.GetLifecycle() != v0.ServiceLifecycle_SERVICE_LIFECYCLE_READY {
			pending = append(pending, service)
		}
	}
	return pending
}

func (e *ReadinessTimeout) Error() string {
	message := fmt.Sprintf("timeout waiting for flow to be ready after %s", e.Timeout)
	pending := e.Pending()
	if len(pending) == 0 {
		return message
	}
	return fmt.Sprintf("%s: %s", message, describeStages(pending, reference(e.observedAt)))
}

// ReadinessFailure reports a dependency the flow declared it cannot start.
// Waiting out the rest of the budget cannot change that verdict, so it is
// returned as soon as the flow reports one — and as a failure rather than as
// the timeout it would otherwise be mistaken for.
type ReadinessFailure struct {
	// Services holds only the dependencies that failed.
	Services []*v0.ServiceReadiness

	// observedAt is when the failure was read, for the same reason
	// ReadinessTimeout carries one.
	observedAt time.Time
}

func (e *ReadinessFailure) Error() string {
	return fmt.Sprintf("dependency failed to start: %s", describeStages(e.Services, reference(e.observedAt)))
}

func failedServices(services []*v0.ServiceReadiness) []*v0.ServiceReadiness {
	var failed []*v0.ServiceReadiness
	for _, service := range services {
		if service.GetLifecycle() == v0.ServiceLifecycle_SERVICE_LIFECYCLE_FAILED {
			failed = append(failed, service)
		}
	}
	return failed
}

// reference is the instant stage durations are measured against. A zero stamp
// belongs to a value built outside WaitForReady, where the current clock is
// the only reference there is.
func reference(observed time.Time) time.Time {
	if observed.IsZero() {
		return time.Now()
	}
	return observed
}

func describeStages(services []*v0.ServiceReadiness, at time.Time) string {
	described := make([]string, 0, len(services))
	for _, service := range services {
		described = append(described, describeStage(service, at))
	}
	return strings.Join(described, ", ")
}

// describeStage names one dependency, the stage it is stuck in, and how long
// it had been there as of at. The elapsed time is charged to the stage rather
// than to the whole wait, which is what separates a slow image pull from a
// slow boot.
func describeStage(service *v0.ServiceReadiness, at time.Time) string {
	description := fmt.Sprintf("%s %s", service.GetService(), lifecycleLabel(service.GetLifecycle()))
	// A nil timestamp reads as the Unix epoch rather than as absent, so it is
	// checked here instead of through IsValid.
	if entered := service.GetEnteredAt(); entered != nil {
		description = fmt.Sprintf("%s for %s", description, at.Sub(entered.AsTime()).Truncate(time.Millisecond))
	}
	if detail := service.GetMessage(); detail != "" {
		description = fmt.Sprintf("%s (%s)", description, detail)
	}
	return description
}

func lifecycleLabel(lifecycle v0.ServiceLifecycle) string {
	switch lifecycle {
	case v0.ServiceLifecycle_SERVICE_LIFECYCLE_PENDING:
		return "pending"
	case v0.ServiceLifecycle_SERVICE_LIFECYCLE_ACQUIRING_IMAGE:
		return "acquiring image"
	case v0.ServiceLifecycle_SERVICE_LIFECYCLE_STARTING:
		return "starting"
	case v0.ServiceLifecycle_SERVICE_LIFECYCLE_READY:
		return "ready"
	case v0.ServiceLifecycle_SERVICE_LIFECYCLE_FAILED:
		return "failed"
	default:
		return "not ready"
	}
}

// running caches the identity of the working directory it was resolved from,
// keyed by that directory so a later chdir re-resolves instead of serving a
// stale identity. It is read from concurrent goroutines — callers legitimately
// start dependency sessions in parallel — so the lazy fill is synchronised.
//
// A dependency session does not use it: sessions resolve their own identity
// from the directory they are anchored to. It serves only the convenience
// package functions below.
var running struct {
	mu      sync.Mutex
	dir     string
	module  *resources.Module
	service *resources.Service
}

func currentIdentity() (*resources.Module, *resources.Service, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	running.mu.Lock()
	defer running.mu.Unlock()
	if running.dir == dir {
		return running.module, running.service, nil
	}
	identity, err := findSessionIdentity(context.Background(), dir)
	if err != nil {
		return nil, nil, err
	}
	if identity == nil {
		// A directory that owns no service is not an error here: these
		// functions have always answered it with a nil module and service, and
		// callers branch on that rather than on an error.
		return nil, nil, nil
	}
	running.dir = dir
	running.module = identity.module
	running.service = identity.service
	return identity.module, identity.service, nil
}

// Service resolves the service owning the current working directory, or nil
// when the directory belongs to no service.
//
// Deprecated: use Dependencies.Service, which is anchored to the directory its
// session was created in and therefore survives a change of working directory.
func Service() (*resources.Service, error) {
	_, svc, err := currentIdentity()
	return svc, err
}

// Module resolves the module owning the current working directory, or nil when
// the directory belongs to no module.
//
// Deprecated: use Dependencies.Module, which is anchored to the directory its
// session was created in and therefore survives a change of working directory.
func Module() (*resources.Module, error) {
	mod, _, err := currentIdentity()
	return mod, err
}

// Inject writes one variable straight into the process environment. It is
// unmanaged: no dependency session owns the value and nothing restores it.
//
// Deprecated: use Dependencies.SetEnvironment, which is transactional and
// restores what it owns, or Dependencies.Environ for a child-process
// environment.
func Inject(env *resources.EnvironmentVariable) {
	os.Setenv(env.Key, fmt.Sprintf("%v", env.Value))
}

// SetEnvironment injects this session's dependencies into the process
// environment. It is the compatibility path for callers that read connection
// strings with os.Getenv; new code should prefer Environ, which needs no
// process-global state.
//
// os.Environ is a single process-wide resource, so exactly one session owns it
// at a time: a second session calling SetEnvironment is rejected before any
// value changes, and must run command-scoped instead (see
// WithCommandScopedEnvironment). Every value is resolved before the first
// write, and a write that fails rolls the round back.
//
// Calling it again on the same session re-resolves and re-applies; the values
// captured before the session's first injection are the ones ReleaseEnvironment
// restores. Stop and Destroy release the session's ownership.
func (l *Dependencies) SetEnvironment(ctx context.Context) error {
	if l.inherited {
		return nil
	}
	env, err := l.resolveEnvironment(ctx)
	if err != nil {
		return err
	}
	l.setResolved(env)
	return l.apply(env)
}

// Stop gracefully stops all running dependencies. Sends StopFlow to the
// CLI (which triggers the agents' own cleanup), then kills the CLI
// subprocess group — belt and suspenders so we always take the whole
// tree down.
func (l *Dependencies) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.Stop")
	if l.inherited {
		w.Debug("leaving dependencies owned by the parent Codefly runtime running")
		return nil
	}
	// Deferred, so this session keeps its control channel until the flow is
	// actually torn down. Releasing it first would let another session in this
	// process claim the endpoint while the old CLI still holds it.
	defer l.ReleaseEnvironment()
	defer releaseControlAddress(l.controlAddress)
	if l.keepRunning {
		if l.conn != nil {
			_ = l.conn.Close()
		}
		if l.proc != nil {
			l.proc.Release()
		}
		if l.attached {
			w.Debug("released existing kept-running flow")
		} else {
			w.Debug("released spawned flow because keep-running is enabled")
		}
		return nil
	}
	_, err := l.cli.StopFlow(ctx, &v0.StopFlowRequest{})
	if err != nil {
		w.Warn("failed to stop flow", wool.Field("error", err))
	}
	l.close(w)
	return err
}

// Destroy tears down all running dependencies. Same guarantee as Stop
// but instructs the CLI to DestroyFlow (which removes state, not just
// stopping processes) before killing the subprocess group.
func (l *Dependencies) Destroy(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.Destroy")
	if l.inherited {
		w.Debug("leaving dependencies owned by the parent Codefly runtime running")
		return nil
	}
	// Deferred, so this session keeps its control channel until the flow is
	// actually torn down. Releasing it first would let another session in this
	// process claim the endpoint while the old CLI still holds it.
	defer l.ReleaseEnvironment()
	defer releaseControlAddress(l.controlAddress)
	if l.keepRunning {
		if l.conn != nil {
			_ = l.conn.Close()
		}
		if l.proc != nil {
			l.proc.Release()
		}
		if l.attached {
			w.Debug("released existing kept-running flow")
		} else {
			w.Debug("released spawned flow because keep-running is enabled")
		}
		return nil
	}
	_, err := l.cli.DestroyFlow(ctx, &v0.DestroyFlowRequest{})
	if err != nil {
		w.Warn("failed to destroy flow", wool.Field("error", err))
	}
	l.close(w)
	return err
}

// close releases everything this invocation owns: the control connection, the
// spawned process group, and the private directory holding its control socket.
// A group teardown that fails is reported rather than discarded — a surviving
// agent tree is exactly what the caller needs to hear about.
func (l *Dependencies) close(w *wool.Wool) {
	if l.conn != nil {
		_ = l.conn.Close()
	}
	if l.proc != nil {
		if killErr := l.proc.Kill(); killErr != nil {
			w.Warn("could not tear down the CLI process group", wool.Field("error", killErr))
		}
	}
	if l.control != nil {
		_ = l.control.Remove()
	}
}

// waitForReady blocks until conn reaches connectivity.Ready AND the
// peer's grpc.health.v1 endpoint reports SERVING (or the peer doesn't
// advertise health, in which case we fall through), or ctx expires.
//
// connectivity.Ready proves the TCP+TLS handshake completed; the
// health Check on top proves the peer is past service registration
// and ready to handle RPCs. Without this, callers race against the
// CLI sub-process between port-bind and registration.
func waitForReady(ctx context.Context, conn *grpc.ClientConn) bool {
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			break
		}
		if !conn.WaitForStateChange(ctx, state) {
			return false
		}
	}
	hc := healthpb.NewHealthClient(conn)
	resp, err := hc.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		// Older peers without a registered health server — accept the
		// connection as ready since connectivity.Ready already passed.
		return ctx.Err() == nil
	}
	return resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
}

func normalize(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, "/", "__")
	return s
}
