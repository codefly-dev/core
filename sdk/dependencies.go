package sdk

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Dependencies manages a running set of codefly-managed service
// dependencies. The underlying CLI subprocess runs in its own process
// group via managedProcess so Destroy can tear down the entire tree —
// the CLI, its spawned agents, and their containers — with a single
// group kill. Without this, `go test` leaks containers and hangs on
// WaitDelay waiting for inherited stdout/stderr FDs.
type Dependencies struct {
	proc           *managedProcess
	cli            v0.CLIClient
	conn           *grpc.ClientConn
	runtimeContext *basev0.RuntimeContext
	keepRunning    bool
	attached       bool
}

type Option struct {
	Debug                bool
	Timeout              time.Duration
	NamingScope          string
	Fixture              string
	Silents              []string
	ExcludedDependencies []string
	KeepRunning          bool
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

// WithKeepRunning keeps the spawned Codefly dependency stack alive when
// Dependencies.Stop or Dependencies.Destroy is called. A later WithDependencies
// call using the same naming scope first tries to attach to that warm CLI
// server before starting a new stack.
func WithKeepRunning() OptionFunc {
	return func(o *Option) {
		o.KeepRunning = true
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
	args := []string{"run", "service"}
	if opt.Debug {
		args = append(args, "-d")
	}
	if opt.NamingScope != "" {
		args = append(args, "--naming-scope", opt.NamingScope)
	}
	if opt.Fixture != "" {
		args = append(args, "--fixture", opt.Fixture)
	}
	if len(opt.Silents) > 0 {
		args = append(args, "--silent", strings.Join(opt.Silents, ","))
	}
	if len(opt.ExcludedDependencies) > 0 {
		args = append(args, "--exclude-dependency", strings.Join(opt.ExcludedDependencies, ","))
	}

	addr := cliServerAddress(ctx, opt.NamingScope)
	if opt.KeepRunning {
		if deps, err := attachDependencies(ctx, addr, opt); err == nil {
			return deps, nil
		} else {
			// Attach miss is the common case (no warm server yet) but
			// can also mask a real transport problem (stale socket,
			// permission, dial timeout). Log so operators can see why
			// keep-running spawned a fresh stack instead of attaching.
			wool.Get(ctx).In("sdk.WithDependencies").
				Debug("attach to existing CLI server failed; starting new stack",
					wool.Field("addr", addr),
					wool.Field("error", err.Error()))
		}
	}

	// --headless prevents the CLI from trying to open /dev/tty for
	// interactive context selection. Always needed when running as a
	// subprocess (go test, CI, MCP, pipes).
	args = append(args, "--exclude-root", "--cli-server", "--headless")
	cmd := exec.CommandContext(ctx, codeflyBinary(), args...)
	// ARCHITECTURE: the SDK owns the control channel for the child it starts.
	// Pass the exact selected port to the CLI instead of asking two separately
	// versioned binaries to reproduce the same hash algorithm. This keeps
	// headless test communication stable while core and the CLI roll forward
	// independently.
	cmd.Env = withCLIServerPort(os.Environ(), addr)
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
	success := false
	defer func() {
		if !success {
			if conn != nil {
				_ = conn.Close()
			}
			_ = proc.Kill()
		}
	}()

	conn, err = grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cannot create gRPC client for %s: %w", addr, err)
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
		return nil, fmt.Errorf("gRPC connection to CLI server at %s did not become ready within %s", addr, opt.Timeout)
	}

	cli := v0.NewCLIClient(conn)
	_, err = cli.Ping(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("CLI server ping failed: %w", err)
	}
	runtimeContext := resources.RuntimeContextFromEnv()
	l := &Dependencies{proc: proc, cli: cli, conn: conn, runtimeContext: runtimeContext, keepRunning: opt.KeepRunning}
	err = l.WaitForReady(ctx, opt)
	if err != nil {
		return nil, err
	}
	err = l.SetEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	success = true
	return l, nil
}

func cliServerAddress(ctx context.Context, namingScope string) string {
	// The CLI derives its gRPC port from the workspace name via
	// network.CLIServerPort. When a naming scope is set (parallel tests),
	// we include it in the name so each scope gets a unique port.
	wsName := ""
	if ws, err := resources.FindWorkspaceUp(ctx); err == nil && ws != nil {
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

func codeflyBinary() string {
	if path := strings.TrimSpace(os.Getenv("CODEFLY_BINARY")); path != "" {
		return path
	}
	return "codefly"
}

func attachDependencies(ctx context.Context, addr string, opt *Option) (*Dependencies, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cannot create gRPC client for %s: %w", addr, err)
	}
	connectCtx, connectCancel := context.WithTimeout(ctx, attachExistingTimeout(opt.Timeout))
	defer connectCancel()
	conn.Connect()
	if !waitForReady(connectCtx, conn) {
		_ = conn.Close()
		return nil, fmt.Errorf("existing CLI server at %s did not become ready within %s", addr, opt.Timeout)
	}
	cli := v0.NewCLIClient(conn)
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
	}
	if err := l.WaitForReady(ctx, opt); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := l.SetEnvironment(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	fmt.Printf("Attached to existing codefly dependencies at %s\n", addr)
	return l, nil
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

var runningModule *resources.Module
var runningService *resources.Service

func Service() (*resources.Service, error) {
	if runningService == nil {
		mod, svc, err := resources.LoadModuleAndServiceFromCurrentPath(context.Background())
		if err != nil {
			return nil, err
		}
		runningService = svc
		runningModule = mod
	}
	return runningService, nil
}

func Module() (*resources.Module, error) {
	if runningModule == nil {
		ctx := context.Background()
		workspace, err := resources.FindWorkspaceUp(ctx)
		if err != nil {
			return nil, err
		}
		if workspace.Layout == resources.LayoutKindFlat {
			module, err := workspace.LoadModuleFromName(ctx, workspace.Name)
			if err != nil {
				return nil, err
			}
			runningModule = module
		} else {
			mod, err := resources.LoadModuleFromCurrentPath(ctx)
			if err != nil {
				return nil, err
			}
			runningModule = mod
		}
	}
	return runningModule, nil
}

func Inject(env *resources.EnvironmentVariable) {
	os.Setenv(env.Key, fmt.Sprintf("%v", env.Value))
}

func (l *Dependencies) SetEnvironment(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.SetEnvironment")
	svc, err := Service()
	if err != nil {
		return err
	}
	mod, err := Module()
	if err != nil {
		return err
	}

	var envs []*resources.EnvironmentVariable
	envs = append(envs,
		resources.ServiceAsEnvironmentVariable(svc.Name),
		resources.ModuleAsEnvironmentVariable(mod.Name),
		resources.VersionAsEnvironmentVariable(svc.Version))
	for _, env := range envs {
		Inject(env)
	}
	// Setup Networking
	{
		networkAccess := resources.NetworkAccessFromRuntimeContext(l.runtimeContext)
		if networkAccess == nil {
			return w.NewError("no network access found")
		}
		req := &v0.GetNetworkMappingsRequest{Module: mod.Name, Service: svc.Name}

		resp, err := l.cli.GetDependenciesNetworkMappings(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get dependencies network mappings")
		}
		for _, np := range resp.NetworkMappings {
			inst := resources.FilterNetworkInstance(ctx, np.Instances, networkAccess)
			if inst == nil {
				return w.NewError("no network instance found")
			}
			access := &resources.EndpointAccess{
				Endpoint:        np.Endpoint,
				NetworkInstance: inst,
			}
			Inject(resources.EndpointAsEnvironmentVariable(access))
		}
	}
	// Setup Configuration
	{
		req := &v0.GetConfigurationRequest{
			Module:  mod.Name,
			Service: svc.Name,
		}
		resp, err := l.cli.GetConfiguration(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get configuration")
		}
		conf := resp.Configuration
		if conf != nil {
			envs := resources.ConfigurationAsEnvironmentVariables(conf, false)
			secrets := resources.ConfigurationAsEnvironmentVariables(conf, true)
			envs = append(envs, secrets...)
			for _, env := range envs {
				Inject(env)
			}
		}
	}
	// Setup Dependencies Configurations
	{
		req := &v0.GetConfigurationRequest{
			Module:  mod.Name,
			Service: svc.Name,
		}
		resp, err := l.cli.GetDependenciesConfigurations(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get dependencies configurations")
		}
		dependenciesConfigurations := resources.FilterConfigurations(resp.Configurations, l.runtimeContext)
		for _, conf := range dependenciesConfigurations {
			envs := resources.ConfigurationAsEnvironmentVariables(conf, false)
			secrets := resources.ConfigurationAsEnvironmentVariables(conf, true)
			envs = append(envs, secrets...)
			for _, env := range envs {
				Inject(env)
			}
		}
	}
	return nil
}

// Stop gracefully stops all running dependencies. Sends StopFlow to the
// CLI (which triggers the agents' own cleanup), then kills the CLI
// subprocess group — belt and suspenders so we always take the whole
// tree down.
func (l *Dependencies) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.Stop")
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
	if l.conn != nil {
		_ = l.conn.Close()
	}
	if l.proc != nil {
		_ = l.proc.Kill()
	}
	return err
}

// Destroy tears down all running dependencies. Same guarantee as Stop
// but instructs the CLI to DestroyFlow (which removes state, not just
// stopping processes) before killing the subprocess group.
func (l *Dependencies) Destroy(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.Destroy")
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
	if l.conn != nil {
		_ = l.conn.Close()
	}
	if l.proc != nil {
		_ = l.proc.Kill()
	}
	return err
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
