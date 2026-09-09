package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
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

	// mu guards the lazily resolved identity and the resolved environment.
	mu          sync.Mutex
	identity    *resolvedIdentity
	environment *sessionEnvironment

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
	dir, err := sessionDirectory(opt)
	if err != nil {
		return nil, err
	}
	if hasManagedDependencyEnvironment(os.Environ()) {
		if hasInvocationConfigurationOverrides(opt) {
			return nil, fmt.Errorf("invocation-scoped configurations cannot replace values in dependencies owned by the parent Codefly runtime")
		}
		wool.Get(ctx).In("sdk.WithDependencies").
			Debug("reusing dependencies injected by the managed Codefly runtime")
		return &Dependencies{
			runtimeContext: resources.RuntimeContextFromEnv(),
			inherited:      true,
			dir:            dir,
		}, nil
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
	args := dependencyCommandArguments(opt, channel.scope)

	if opt.KeepRunning {
		if deps, err := attachDependencies(ctx, channel, dir, opt); err == nil {
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
		if err := channel.control.RemoveReceipt(); err != nil {
			return nil, err
		}
		if err := channel.control.ClearStaleSocket(); err != nil {
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
	runtimeContext := resources.RuntimeContextFromEnv()
	l := &Dependencies{
		proc:           proc,
		cli:            cli,
		conn:           conn,
		control:        channel.control,
		runtimeContext: runtimeContext,
		keepRunning:    opt.KeepRunning,
		dir:            dir,
	}
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

// sessionDirectory is the absolute directory a session is anchored to: the one
// the caller pinned, or the working directory at the moment the session starts.
func sessionDirectory(opt *Option) (string, error) {
	if opt.Directory != "" {
		return opt.Directory, nil
	}
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
	return nil
}

func hasInvocationConfigurationOverrides(opt *Option) bool {
	return len(opt.WorkspaceConfigurations) > 0 || len(opt.ServiceConfigurations) > 0
}

// validateConsumedMappingVisibility fails closed when the consuming module is
// not permitted to reach an endpoint it actually depends on. It scopes to the
// declared dependencies so an unrelated sibling endpoint surfaced by the graph
// never produces a false rejection, and it refuses a mapping with no endpoint
// rather than dereferencing a nil.
func validateConsumedMappingVisibility(consumerModule string, deps []*resources.ServiceDependency, mappings []*basev0.NetworkMapping) error {
	for _, mapping := range mappings {
		ep := mapping.GetEndpoint()
		if ep == nil {
			return fmt.Errorf("dependency network mapping is missing its endpoint")
		}
		if !dependenciesConsumeMapping(deps, ep) {
			continue
		}
		if err := resources.ValidateEndpointVisibility(consumerModule, ep.Module, ep.Service, ep.Name, ep.Visibility, ep.AllowModules); err != nil {
			return err
		}
	}
	return nil
}

// dependenciesConsumeMapping reports whether any declared dependency consumes
// the given producer endpoint. A dependency matches by producer service (and
// module when both are known) and then by its endpoint selector.
func dependenciesConsumeMapping(deps []*resources.ServiceDependency, ep *basev0.Endpoint) bool {
	for _, dep := range deps {
		if dep.Name != ep.Service {
			continue
		}
		if dep.Module != "" && ep.Module != "" && dep.Module != ep.Module {
			continue
		}
		if dep.ConsumesEndpoint(ep.Name, ep.Api) {
			return true
		}
	}
	return false
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

func attachDependencies(ctx context.Context, channel *controlChannel, dir string, opt *Option) (*Dependencies, error) {
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
	l := &Dependencies{
		cli:            cli,
		conn:           conn,
		runtimeContext: resources.RuntimeContextFromEnv(),
		keepRunning:    true,
		attached:       true,
		dir:            dir,
	}
	if err := l.WaitForReady(ctx, opt); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := l.installEnvironment(ctx, opt); err != nil {
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
				return fmt.Errorf("timeout waiting for flow to be ready after %s", opt.Timeout)
			}
			return err
		}
		if status.Ready {
			return nil
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("timeout waiting for flow to be ready after %s", opt.Timeout)
		case <-time.After(500 * time.Millisecond):
		}
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
	identity, err := resolveSessionIdentity(context.Background(), dir)
	if err != nil {
		return nil, nil, err
	}
	running.dir = dir
	running.module = identity.module
	running.service = identity.service
	return identity.module, identity.service, nil
}

// Service resolves the service owning the current working directory.
//
// Deprecated: use Dependencies.Service, which is anchored to the directory its
// session was created in and therefore survives a change of working directory.
func Service() (*resources.Service, error) {
	_, svc, err := currentIdentity()
	return svc, err
}

// Module resolves the module owning the current working directory.
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
	l.ReleaseEnvironment()
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
	l.ReleaseEnvironment()
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
