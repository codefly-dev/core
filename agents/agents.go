package agents

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/toolbox/policyguard"
	"github.com/codefly-dev/core/wool"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ProtocolVersion is the strict stdout handshake contract. Version 2 removes
// the ambiguous numeric-port endpoint used by pre-UDS agents.
const ProtocolVersion = 2

// agentShutdownHardDeadline bounds how long the agent may take to exit AFTER a
// shutdown signal before it force-exits itself. Larger than the graceful budget
// (5s Runtime.Stop + 3s GracefulStop) so a clean shutdown finishes on its own, but
// comfortably under the parent loader's gracefulShutdownTimeout (30s) so the parent
// never has to SIGKILL the process group (which orphans children).
const agentShutdownHardDeadline = 12 * time.Second

// PluginRegistration holds the gRPC servers a plugin wants to expose.
// All registration is handled by core -- plugins never import grpc directly.
//
// Plugins implement the capabilities they need:
//   - Infrastructure (redis, postgres): Agent + Runtime
//   - Application (go-grpc, python-fastapi): Agent + Runtime + Builder + Tooling/Toolbox
//   - Capability toolboxes (git, docker, nix, web, grpc): Agent + Toolbox only
//   - Tooling-only (go-analyzer): Agent + Tooling
//
// Separation of concerns:
//   - Runtime: service lifecycle (Load/Init/Start/Stop/Destroy)
//   - Builder: Docker build + k8s deploy + scaffolding
//   - Code: file/git/LSP operations (deprecated — use Tooling/Toolbox for language-specific ops)
//   - Tooling: language-specific typed analysis (LSP, callgraph, fix, deps, build/test/lint).
//     To be collapsed into Toolbox via the conventional `lang.*` tool set;
//     remains for transition while consumers (Mind) migrate to the typed wrapper
//     over CallTool.
//   - Toolbox: MCP-shape callable surface — Identity / ListTools / CallTool / Resources /
//     Prompts. The unified contract going forward. Capability plugins (git, docker,
//     nix, web, grpc) expose only this; language plugins expose it alongside Tooling
//     until the migration completes.
type PluginRegistration struct {
	Agent   agentv0.AgentServer
	Runtime runtimev0.RuntimeServer
	Builder builderv0.BuilderServer
	Code    codev0.CodeServer       // Deprecated: use Tooling/Toolbox for language-specific operations.
	Tooling toolingv0.ToolingServer // Transitional: collapses into Toolbox via lang.* convention.
	Toolbox toolboxv0.ToolboxServer // The unified callable contract (MCP-shape).

	// PDP gates Toolbox tool calls when non-nil. Wires the
	// policyguard.Guard around the registered Toolbox before gRPC
	// registration.
	//
	// nil is interpreted by Serve through CODEFLY_PDP_MODE:
	//   - enforce: Serve refuses to start (os.Exit(1)) — the
	//     operator opted into fail-closed authorization and a
	//     missing PDP would silently default-allow every CallTool.
	//   - shadow: Serve logs a loud WARN and proceeds with the
	//     raw Toolbox (no Guard). Use during the rollout window
	//     so the un-gated state is visible in logs.
	//   - off / unset: Serve silently registers the raw Toolbox
	//     (the historical M3 shadow-rollout default).
	//
	// Production wiring: codefly host constructs a SaasPDP that
	// calls saas-starter's Decide RPC, threads it here. Plugins
	// don't construct PDPs themselves — the host owns policy.
	//
	// Identity attribution: the Guard reads the principal from the
	// stamped context (see principalUnaryInterceptor); the static
	// "Toolbox" name on the Guard is the toolbox identifier from
	// the manifest, set by the host that registers the toolbox.
	PDP policy.PDP

	// PDPToolboxName is the toolbox identity surfaced in
	// PDPRequest.Toolbox when the Guard fires. Typically the
	// canonical agent identifier (publisher/name:version) so PDP
	// rules can target a specific plugin. Optional; defaults to
	// "" which the JSON PDP treats as "any toolbox".
	PDPToolboxName string
}

// agentRPCInterceptor logs incoming RPCs with method name and duration
// to stderr (which is captured by the gateway's ring buffer).
// High-frequency polling RPCs (Information) are suppressed unless they error.
//
// In addition to the per-line stderr log, every call updates the
// in-process latency histogram (RPCStats). Callers can read it via
// SnapshotRPCStats — useful for daemons that want to expose p50/p99
// per RPC without standing up a separate metrics endpoint.
// AuthMetadataKey is the gRPC metadata key carrying the per-spawn
// auth token. Lowercase per gRPC convention — metadata keys are
// case-insensitive but the wire form is lowercase.
const AuthMetadataKey = "x-codefly-token"

// authUnaryInterceptor verifies the per-spawn token on every unary gRPC call.
// An empty expected token is a server misconfiguration and fails closed.
//
// The health-check service is exempt: the host's readiness probe
// uses grpc.health.v1.Health/Check which fires BEFORE the host has
// established any client metadata interceptor (it's part of the
// agent-loader's connection setup). Letting Check through unauthed
// is safe — it returns only SERVING/NOT_SERVING, no privileged data.
func authUnaryInterceptor(expectedToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isHealthMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		if expectedToken == "" {
			return nil, status.Error(codes.Unauthenticated, "agent auth token is not configured")
		}
		if err := verifyAuthToken(ctx, expectedToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// runtimeLoadTracker records that this agent process has actually entered the
// Runtime lifecycle. Builder-only invocations still register a Runtime server,
// but must not call Runtime.Stop during process shutdown: many agents cannot
// safely tear down runtime state that was never loaded.
func runtimeLoadTracker(loaded *atomic.Bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == runtimev0.Runtime_Load_FullMethodName {
			loaded.Store(true)
		}
		return handler(ctx, req)
	}
}

// authStreamInterceptor mirrors authUnaryInterceptor for streaming
// RPCs. Same exemption for health checks.
func authStreamInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isHealthMethod(info.FullMethod) {
			return handler(srv, ss)
		}
		if expectedToken == "" {
			return status.Error(codes.Unauthenticated, "agent auth token is not configured")
		}
		if err := verifyAuthToken(ss.Context(), expectedToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// verifyAuthToken extracts the bearer from metadata and constant-time
// compares against the expected value. Returns codes.Unauthenticated
// on any failure path so the model sees a clear refusal.
func verifyAuthToken(ctx context.Context, expected string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no metadata")
	}
	values := md.Get(AuthMetadataKey)
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "missing auth token")
	}
	// subtle.ConstantTimeCompare returns 1 on equality. Comparing as
	// byte slices avoids the timing side-channel of `==`.
	if subtle.ConstantTimeCompare([]byte(values[0]), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "bad auth token")
	}
	return nil
}

// isHealthMethod returns true for gRPC health-check method names.
// The set is small enough to enumerate — no need for a string-prefix
// hack that could accidentally match plugin methods.
func isHealthMethod(fullMethod string) bool {
	switch fullMethod {
	case "/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch":
		return true
	}
	return false
}

// panicRecoveryInterceptor converts a panic in any handler (or inner
// interceptor) into a gRPC error the host can see, instead of letting it crash
// the agent process or — worse — be swallowed into a log line while the RPC
// returns a zero value as if it succeeded. Paired with wool.SetRethrowAfterCatch
// (enabled in Serve): a handler's `defer Wool.Catch()` logs the friendly panic
// line and re-raises, and this is where the re-raised panic lands.
//
// The panic value is surfaced in the error message (so codefly SHOWS what blew
// up), and the goroutine stack is written to the agent log for debugging.
func panicRecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				method := info.FullMethod
				if idx := strings.LastIndex(method, "/"); idx >= 0 {
					method = method[idx+1:]
				}
				fmt.Fprintf(os.Stderr, "[agent] %s PANIC (value redacted)\n%s\n", method, debug.Stack())
				err = status.Errorf(codes.Internal, "agent panicked in %s", method)
			}
		}()
		return handler(ctx, req)
	}
}

func agentRPCInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		dur := time.Since(start)

		method := info.FullMethod
		if idx := strings.LastIndex(method, "/"); idx >= 0 {
			method = method[idx+1:]
		}

		recordRPCDuration(method, dur, err)

		if method == "Information" && err == nil {
			return resp, err
		}

		errStr := ""
		if err != nil {
			errStr = " error=" + err.Error()
		}
		fmt.Fprintf(os.Stderr, "[agent] %s %dms%s\n", method, dur.Milliseconds(), errStr)
		return resp, err
	}
}

// rpcStats keeps per-method counters + a running histogram. The
// implementation is intentionally simple: we accumulate count/sum/max
// and bucket counts. No external metrics SDK to keep the agent binary
// small. Daemons that want richer telemetry can wrap the snapshot.
//
// Thread-safe via a single mutex — RPC throughput on a single agent is
// modest (Test/Build/Init are bursty but never high-rate), so contention
// is not a concern.
type rpcMethodStats struct {
	Count   uint64        // total invocations
	Errors  uint64        // count where err != nil
	Sum     time.Duration // sum of all durations
	Max     time.Duration // longest call seen
	Buckets [9]uint64     // histogram, see latencyBuckets
}

// latencyBuckets are upper-inclusive ms boundaries. Final bucket is
// implicit (everything over 30s).
var latencyBuckets = [9]time.Duration{
	1 * time.Millisecond,
	5 * time.Millisecond,
	25 * time.Millisecond,
	100 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	1 * time.Hour, // overflow sink
}

var (
	rpcStatsMu sync.Mutex
	rpcStats   = make(map[string]*rpcMethodStats)
)

func recordRPCDuration(method string, dur time.Duration, err error) {
	rpcStatsMu.Lock()
	defer rpcStatsMu.Unlock()

	s, ok := rpcStats[method]
	if !ok {
		s = &rpcMethodStats{}
		rpcStats[method] = s
	}
	s.Count++
	s.Sum += dur
	if err != nil {
		s.Errors++
	}
	if dur > s.Max {
		s.Max = dur
	}
	for i, b := range latencyBuckets {
		if dur <= b {
			s.Buckets[i]++
			return
		}
	}
}

// SnapshotRPCStats returns a deep copy of the current per-method stats.
// Safe to call concurrently with live RPCs.
func SnapshotRPCStats() map[string]rpcMethodStats {
	rpcStatsMu.Lock()
	defer rpcStatsMu.Unlock()
	out := make(map[string]rpcMethodStats, len(rpcStats))
	for k, v := range rpcStats {
		out[k] = *v
	}
	return out
}

// ResetRPCStats clears all counters. Test-only — production agents
// should never call this (loses telemetry).
func ResetRPCStats() {
	rpcStatsMu.Lock()
	defer rpcStatsMu.Unlock()
	rpcStats = make(map[string]*rpcMethodStats)
}

// Serve starts a gRPC server, registers the plugin's services,
// signals its endpoint to the CLI via stdout, and blocks until the
// process is terminated.
//
// Plugin binaries call this from main():
//
//	agents.Serve(agents.PluginRegistration{
//	    Agent:   svc,
//	    Runtime: NewRuntime(),
//	    Builder: NewBuilder(),
//	    Code:    NewCode(),
//	})
//
// Transport selection (env vars set by the host):
//
//   - CODEFLY_AGENT_UDS_PATH=/tmp/codefly/foo.sock — listen on a Unix
//     domain socket at that path. Preferred for performance and for
//     filesystem-permission–based access control. Removes the socket
//     file on graceful shutdown; the host removes stale files before
//     respawn after crashes.
//   - No UDS path: TCP on 127.0.0.1:0 (random loopback port), represented as
//     an explicit dns:/// endpoint. Remote agent transport requires a separate
//     authenticated/TLS protocol; Serve never opens an ambient 0.0.0.0 socket.
//
// Handshake on stdout: "<ProtocolVersion>|<endpoint>" where endpoint
// is either an explicit dns:/// loopback target or "unix:<path>" (UDS).
func Serve(reg PluginRegistration) {
	udsPath := os.Getenv("CODEFLY_AGENT_UDS_PATH")
	var (
		lis      net.Listener
		err      error
		endpoint string
	)
	if udsPath != "" {
		// Best-effort cleanup of stale socket from a prior crashed
		// run — Listen("unix", path) fails with "address already in
		// use" otherwise. Remove on exit too (defer below).
		_ = os.Remove(udsPath)
		lis, err = net.Listen("unix", udsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents.Serve: cannot listen on unix:%s: %v\n", udsPath, err)
			os.Exit(1)
		}
		// Use the gRPC unix scheme so the dialing side can use the
		// same URI verbatim.
		endpoint = "unix:" + udsPath
		defer func() { _ = os.Remove(udsPath) }()
	} else {
		bindHost := "127.0.0.1"
		lis, err = net.Listen("tcp", bindHost+":0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents.Serve: cannot listen on %s:0: %v\n", bindHost, err)
			os.Exit(1)
		}
		endpoint = fmt.Sprintf("dns:///%s:%d", bindHost, lis.Addr().(*net.TCPAddr).Port)
	}

	// Per-spawn auth token. The host generates a random token and
	// passes it via CODEFLY_AGENT_TOKEN; we require it as a bearer
	// in gRPC metadata on every call. Without this, anyone who can
	// connect to our UDS / loopback port can drive the plugin.
	//
	// UDS already has filesystem-permission ACL — token is
	// belt-and-braces there. TCP loopback has nothing else, so this
	// is load-bearing on that path.
	//
	expectedToken := os.Getenv("CODEFLY_AGENT_TOKEN")
	if strings.TrimSpace(expectedToken) == "" {
		fmt.Fprintln(os.Stderr, "agents.Serve: CODEFLY_AGENT_TOKEN is required; launch agents through manager.Load")
		os.Exit(1)
	}

	// A handler panic should be a clean RPC error the host can SEE, never a
	// process crash or a silently-swallowed log line. Catch re-raises (instead
	// of swallowing) so the recovery interceptor below turns it into one.
	wool.SetRethrowAfterCatch(true)

	var runtimeLoaded atomic.Bool
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			// panicRecovery is OUTERMOST so it catches panics from the
			// handler AND from the auth/principal interceptors, turning any
			// of them into a gRPC error rather than a crashed goroutine.
			panicRecoveryInterceptor(),
			// Order matters: auth proves the connection (we trust
			// the caller is the codefly host), THEN principal
			// extracts the authority claim (who the caller is
			// acting as). The PDP downstream branches on the
			// stamped Principal — wrong order = no principal +
			// insecure default.
			authUnaryInterceptor(expectedToken),
			principalUnaryInterceptor(),
			runtimeLoadTracker(&runtimeLoaded),
			agentRPCInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			authStreamInterceptor(expectedToken),
			principalStreamInterceptor(),
		),
	)

	if reg.Agent != nil {
		agentv0.RegisterAgentServer(s, reg.Agent)
	}
	if reg.Runtime != nil {
		runtimev0.RegisterRuntimeServer(s, reg.Runtime)
	}
	if reg.Builder != nil {
		builderv0.RegisterBuilderServer(s, reg.Builder)
	}
	if reg.Code != nil {
		codev0.RegisterCodeServer(s, reg.Code)
	}
	if reg.Tooling != nil {
		toolingv0.RegisterToolingServer(s, reg.Tooling)
	}
	if reg.Toolbox != nil {
		// Wrap with policyguard.Guard when a PDP is configured. The
		// Guard intercepts CallTool/ReadResource/GetPrompt and routes
		// each through the PDP for an Allow/Deny/RequireApproval
		// decision. Pass-through for non-side-effecting RPCs
		// (Identity/ListTools/etc.) — see Guard documentation.
		//
		// PDP=nil selects the explicit local raw-server path. CODEFLY_PDP_MODE
		// gates this:
		//   - enforce: a nil PDP with a registered Toolbox is a
		//     misconfiguration and we exit(1) at startup. Operators
		//     have explicitly opted into fail-closed authorization.
		//   - shadow: warn loudly so the rollout state is visible
		//     in logs but proceed.
		//   - off / unset: explicit local policy-off mode.
		if reg.PDP == nil {
			mode, modeErr := policy.ResolvePDPMode()
			switch {
			case modeErr != nil:
				fmt.Fprintf(os.Stderr, "agents.Serve: %v\n", modeErr)
				os.Exit(1)
			case mode == policy.PDPModeEnforce:
				fmt.Fprintf(os.Stderr,
					"agents.Serve: %s=enforce but plugin registered a Toolbox "+
						"with PluginRegistration.PDP=nil — refusing to start "+
						"(would default-allow every CallTool). Wire a PDP or "+
						"set %s=shadow/off.\n",
					policy.EnvPDPMode, policy.EnvPDPMode)
				os.Exit(1)
			case mode == policy.PDPModeShadow:
				fmt.Fprintf(os.Stderr,
					"agents.Serve: %s=shadow but PluginRegistration.PDP=nil "+
						"— every CallTool will pass unchecked. Wire a PDP "+
						"before flipping to enforce.\n",
					policy.EnvPDPMode)
			}
			toolboxv0.RegisterToolboxServer(s, reg.Toolbox)
		} else {
			guarded := policyguard.New(reg.Toolbox, reg.PDP, reg.PDPToolboxName)
			toolboxv0.RegisterToolboxServer(s, guarded)
		}
	}

	// Standard grpc.health.v1 server. Lets the CLI replace the old
	// "TCP port is open" wait-loop with a real readiness probe — fixes
	// the class of races where the port is bound but the server hasn't
	// finished registering services yet.
	//
	// SetServingStatus("", SERVING) registers the empty service name
	// (the wildcard "this server is up" check, per the gRPC spec).
	// Per-service statuses can be added later if specific RPCs need
	// to gate independently (e.g. Tooling NOT_SERVING until LSP boots).
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s, healthSrv)

	// Signal the endpoint to the parent (CLI) using the handshake
	// protocol: "VERSION|<endpoint>\n". Endpoint is an explicit
	// dns:/// loopback target or "unix:<path>" (UDS).
	fmt.Fprintf(os.Stdout, "%d|%s\n", ProtocolVersion, endpoint)

	// Graceful shutdown on SIGTERM/SIGINT.
	//
	// Critical: call the Runtime's Stop FIRST so spawned child processes
	// (user binaries, Docker containers, Nix shells) get torn down. Without
	// this, killing the parent codefly leaks every child as a PPID=1 orphan
	// because GracefulStop only stops the gRPC server — it does NOT touch
	// whatever the agent has running.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		// HARD BACKSTOP: once a shutdown signal arrives, the agent MUST exit well
		// within the parent's SIGTERM→SIGKILL window (loader.go: 30s). The graceful
		// path below is bounded in theory, but any single unbounded step — a Runtime
		// Stop that ignores its ctx, a wedged in-flight RPC, a blocked log forwarder —
		// would let the parent fall through to SIGKILL, which kills the whole process
		// GROUP and orphans/forcibly-kills children (the bug we saw: 30s then SIGKILL).
		// This watchdog makes a clean exit unconditional: whatever wedges, we exit
		// ourselves first. Only armed AFTER the signal, so it never affects a running
		// agent.
		go func() {
			time.Sleep(agentShutdownHardDeadline)
			fmt.Fprintln(os.Stderr, "[agent] shutdown exceeded deadline — forcing exit (avoids the parent's SIGKILL)")
			os.Exit(0)
		}()
		if reg.Runtime != nil && runtimeLoaded.Load() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = reg.Runtime.Stop(stopCtx, &runtimev0.StopRequest{})
			cancel()
		}
		// Bounded graceful shutdown: GracefulStop blocks until every
		// in-flight RPC returns, which is unbounded — a stuck Start
		// (e.g. agent's WaitForReady looping over a failed container)
		// holds the gRPC server open forever. After a short grace we
		// force-stop, so the agent exits in seconds instead of 30s+
		// of the parent's SIGTERM-to-SIGKILL window.
		stopped := make(chan struct{})
		go func() {
			s.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			s.Stop()
		}
		signal.Stop(ch)
	}()

	if err := s.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "agents.Serve: %v\n", err)
		os.Exit(1)
	}
}

// ServeToolbox is the one-capability startup path for toolbox plugins.
// Service plugins should continue to use Serve with PluginRegistration.
func ServeToolbox(server toolboxv0.ToolboxServer) {
	Serve(toolboxRegistration(server))
}

// toolboxRegistration is the secure composition shared by every standalone
// toolbox. A host that supplied scoped authorization or a permissions callback
// gets a callback-backed Guard automatically; an enforce-mode process without
// that wiring reaches Serve with PDP=nil and is rejected at startup. With no
// security env, local development mode keeps the raw server explicit.
func toolboxRegistration(server toolboxv0.ToolboxServer) PluginRegistration {
	hasCallback := os.Getenv(policy.EnvPermissionsSocket) != ""
	hasScopedAuth := os.Getenv("CODEFLY_SCOPED_AUTHZ_SECRET") != ""
	if !hasCallback && !hasScopedAuth {
		return PluginRegistration{Toolbox: server}
	}

	audience := os.Getenv("CODEFLY_TOOLBOX_AUDIENCE")
	if audience == "" {
		audience = os.Getenv("CODEFLY_TOOLBOX_NAME")
	}
	return PluginRegistration{
		Toolbox:        server,
		PDP:            policy.NewCallbackPDPFromEnv(),
		PDPToolboxName: audience,
	}
}
