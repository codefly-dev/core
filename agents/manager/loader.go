package manager

import (
	"bufio"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	runnersbase "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/wool"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// DefaultStartupTimeout is how long Load waits for the agent to print
// its handshake line before giving up.
const DefaultStartupTimeout = 30 * time.Second

// DefaultDialTimeout is how long Load waits for the gRPC connection
// to become ready after dialing.
const DefaultDialTimeout = 10 * time.Second

// stderrCapacity is the maximum number of bytes kept from the agent's
// stderr stream. Only the tail is retained so memory stays bounded. The
// default (64KB) is large enough to capture a Go panic traceback plus a
// few hundred log lines from a misbehaving plugin — the common cases
// callers need for post-mortem diagnosis. Previously 4KB, which was
// short enough to truncate any real stack trace.
const stderrCapacity = 64 * 1024

// ProcessInfo carries metadata about the spawned agent process.
type ProcessInfo struct {
	PID int
}

// AgentConn is a connection to a running agent process.
// It owns the gRPC connection and the child process.
type AgentConn struct {
	conn *grpc.ClientConn
	cmd  *exec.Cmd
	info *ProcessInfo

	// stderrBuf holds the last stderrCapacity bytes of the agent's
	// stderr output for inclusion in error messages.
	stderrBuf *ringBuffer

	// done is closed when the process exits (via the reaper goroutine).
	done chan struct{}

	// permissionsCallback is the host-side server that answers
	// the plugin's Authorized() calls. Non-nil only when the
	// caller passed WithPermissionsCallback. Close shuts it down
	// alongside the gRPC connection so the UDS file is removed.
	permissionsCallback *policy.PermissionsCallbackServer
}

// GRPCConn returns the shared gRPC connection to the agent.
func (c *AgentConn) GRPCConn() *grpc.ClientConn { return c.conn }

// ProcessInfo returns the agent's process metadata.
func (c *AgentConn) ProcessInfo() *ProcessInfo { return c.info }

// gracefulShutdownTimeout is how long Close waits after SIGTERM for the
// agent to exit cleanly before falling back to SIGKILL. Must be larger
// than the agent's own internal stop timeout (5s in agents.Serve) plus
// the NativeProc.Stop SIGTERM/SIGKILL grace window (~7s) times the
// number of children the agent may be supervising. Agents orchestrating
// 3+ services can hit ~21s of cascading stops; 30s gives real headroom
// without making Ctrl-C feel unresponsive.
const gracefulShutdownTimeout = 30 * time.Second

// Close shuts down the gRPC connection, asks the agent to exit
// gracefully (SIGTERM) so it can reap its child processes (user
// binaries, Docker containers) via its agents.Serve signal handler,
// then falls back to SIGKILL if the agent is unresponsive.
//
// The previous implementation jumped straight to SIGKILL, which won the
// race against the agent's own SIGTERM handler and orphaned every
// child process as a PPID=1 zombie — exactly what the agent's signal
// handler was written to prevent.
//
// cmd.Wait must only be called once — the reaper owns it. We observe
// completion via the `done` channel the reaper closes.
func (c *AgentConn) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	// Shut down the permissions callback BEFORE killing the plugin —
	// in-flight Authorized() calls fail cleanly with "callback closed"
	// rather than blocking on a server we're about to terminate.
	if c.permissionsCallback != nil {
		_ = c.permissionsCallback.Close()
		c.permissionsCallback = nil
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}

	// Step 1: SIGTERM. The agent's signal handler in agents.Serve will
	// call Runtime.Stop on its child NativeProcs and then GracefulStop
	// the gRPC server. The reaper's cmd.Wait returns once the agent
	// process exits, closing `done`.
	startedAt := time.Now()
	pid := c.cmd.Process.Pid
	// Use a global wool logger so the long-running stop sequence shows
	// progress instead of looking like a hang. AgentConn intentionally
	// doesn't carry per-instance state so we fall back to wool.Get(bg).
	w := wool.Get(context.Background()).In("AgentConn.Close", wool.Field("pid", pid))
	// Trace, not Debug: routine per-agent shutdown steps. At Debug they flood
	// every normal `codefly run` teardown (one pair per agent) and, because the
	// TUI leaves the terminal in raw mode during shutdown, the lines staircase.
	// The notable cases (SIGTERM timeout → SIGKILL) stay at Warn/Info below.
	w.Trace("agent SIGTERM sent")
	_ = c.cmd.Process.Signal(os.Interrupt)
	if c.done == nil {
		// No reaper to wait on — best-effort kill and bail.
		killAgentGroup(pid)
		return
	}

	select {
	case <-c.done:
		w.Trace(fmt.Sprintf("agent exited gracefully in %s", time.Since(startedAt).Round(time.Millisecond)))
		return
	case <-time.After(gracefulShutdownTimeout):
		// Agent didn't respond to SIGTERM in time — force kill the whole
		// pgroup (agent + any still-running user binaries it spawned) and
		// wait for the reaper so we don't leave zombies behind.
		w.Warn(fmt.Sprintf("agent did not exit within %s of SIGTERM — sending SIGKILL", gracefulShutdownTimeout))
		killAgentGroup(pid)
		<-c.done
		w.Info(fmt.Sprintf("agent killed after %s", time.Since(startedAt).Round(time.Millisecond)))
	}
}

// killAgentGroup SIGKILLs the agent's entire process group. Relies on the
// agent having been started with Setpgid: true so its pgid equals its pid.
// Falls back to killing just the agent PID if the group signal fails
// (e.g. the group is already empty because the agent already exited).
func killAgentGroup(pid int) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// StderrTail returns the last captured bytes from the agent's stderr.
// Useful for diagnostics after a crash or handshake failure.
func (c *AgentConn) StderrTail() string {
	if c.stderrBuf == nil {
		return ""
	}
	return c.stderrBuf.String()
}

// active tracks running agent connections for cleanup.
var (
	activeMu sync.Mutex
	active   = make(map[string]*AgentConn)
)

// sandboxWarnOnce / principalWarnOnce gate the audit-of-defaults security
// warnings to a single emission per process. Each warning is identical
// regardless of which agent triggered it, so emitting one per Load floods a
// multi-service run; once is enough to surface the missing explicit choice (#19).
var (
	sandboxWarnOnce   sync.Once
	principalWarnOnce sync.Once
)

// Cleanup kills an agent process by its unique key and removes it from
// the active map.
func Cleanup(unique string) {
	activeMu.Lock()
	conn, ok := active[unique]
	if ok {
		delete(active, unique)
	}
	activeMu.Unlock()

	if ok {
		conn.Close()
	}
}

// CleanupAll closes every active agent connection. Call this during
// graceful daemon shutdown.
func CleanupAll() {
	activeMu.Lock()
	snapshot := make(map[string]*AgentConn, len(active))
	for k, v := range active {
		snapshot[k] = v
	}
	active = make(map[string]*AgentConn)
	activeMu.Unlock()

	for _, conn := range snapshot {
		conn.Close()
	}
}

// LoadOption configures optional behaviour of Load.
type LoadOption func(*loadConfig)

type loadConfig struct {
	startupTimeout time.Duration
	dialTimeout    time.Duration
	logWriter      io.Writer // if set, agent stderr is teed to this writer in real time
	workDir        string    // working directory for the agent process
	extraEnv       []string  // additional env vars (KEY=VALUE) appended after os.Environ
	sandbox        sandbox.Sandbox
	useUDS         bool // spawn plugin on a Unix domain socket instead of TCP loopback

	// principal is the authority context the spawned plugin runs
	// under. When non-nil, the plugin's tool calls are PDP-gated
	// against this principal's permissions. nil means "no principal
	// binding" — plugin runs with no authority claim, falling back
	// to the codefly-host process's own scope (legacy behavior).
	principal *policy.Principal

	// principalChoiceMade mirrors sandboxChoiceMade: WithPrincipal
	// or WithoutPrincipal flip this true. Calls to Load that DON'T
	// pick get a stderr security warning. Phase 2 will hard-fail on
	// missing choice. Same audit-of-defaults pattern as the sandbox
	// surface — every callsite makes an explicit, greppable choice.
	principalChoiceMade bool

	// permissionsCallback is the host's Decider for plugin-side
	// Authorized() calls. When non-nil, Load:
	//   1. Stands up a UDS-bound HTTP server (policy.PermissionsCallbackServer)
	//      backed by this Decider
	//   2. Sets CODEFLY_PERMISSIONS_SOCKET=<path> in the plugin env
	//   3. Tracks the server on the AgentConn so Close shuts it down
	// Plugin's policy.NewCallbackAuthorizerFromEnv() picks up the
	// socket path and dials lazily.
	permissionsCallback policy.Decider

	// scopedAuthSecret is the HMAC key shared between host and
	// plugin for scoped-authz token verification. When non-nil
	// (and ≥32 bytes), Load:
	//   1. base64url-encodes the secret
	//   2. Sets CODEFLY_SCOPED_AUTHZ_SECRET=<encoded> in the plugin env
	// Plugin's policyguard.Guard reads the env, decodes, uses
	// for HMAC verify of incoming ScopedAuthorization tokens.
	//
	// Hosts that DON'T need the two-level fast path leave this
	// nil — the plugin's Guard falls back to the PDP-via-callback
	// path on every call (single-level model, unchanged).
	//
	// SECURITY: secret length < 32 bytes is rejected at Load
	// time with an explicit error. HMAC strength below 32 bytes
	// is borderline; we refuse rather than silently weaken.
	scopedAuthSecret []byte

	// sandboxChoiceMade is set by WithSandbox or WithoutSandbox.
	// When false (no explicit choice), Load logs a warning to stderr
	// pointing at the security review. Phase 2 (post-CLI migration)
	// flips this to fail-loud — every callsite must make an explicit
	// security decision rather than inheriting silent ambient
	// authority.
	sandboxChoiceMade bool
}

func defaultLoadConfig() loadConfig {
	return loadConfig{
		startupTimeout: DefaultStartupTimeout,
		dialTimeout:    DefaultDialTimeout,
	}
}

// WithStartupTimeout overrides the default time Load waits for the
// agent handshake.
func WithStartupTimeout(d time.Duration) LoadOption {
	return func(c *loadConfig) { c.startupTimeout = d }
}

// WithDialTimeout overrides the default time Load waits for the gRPC
// connection to become ready.
func WithDialTimeout(d time.Duration) LoadOption {
	return func(c *loadConfig) { c.dialTimeout = d }
}

// WithLogWriter tees the agent's stderr to w in real time, in addition
// to buffering in the ring buffer. Useful for debug mode where the
// gateway wants to surface agent logs.
func WithLogWriter(w io.Writer) LoadOption {
	return func(c *loadConfig) { c.logWriter = w }
}

// WithWorkDir sets the working directory for the agent process and
// exports CODEFLY_AGENT_WORKDIR so the agent can resolve file paths.
func WithWorkDir(dir string) LoadOption {
	return func(c *loadConfig) { c.workDir = dir }
}

// WithEnv appends KEY=VALUE strings to the agent process's environment.
// They land AFTER os.Environ so callers' values override the parent's.
// Multiple WithEnv calls accumulate. Used by toolbox launchers to pass
// CODEFLY_TOOLBOX_* configuration; equally usable by any caller that
// needs per-spawn env without polluting the parent process's environ.
func WithEnv(vars ...string) LoadOption {
	return func(c *loadConfig) { c.extraEnv = append(c.extraEnv, vars...) }
}

// WithUDS makes Load spawn the plugin on a Unix domain socket instead
// of TCP loopback. The host picks a unique path under /tmp/codefly/,
// passes it to the plugin via CODEFLY_AGENT_UDS_PATH, and dials over
// `unix:<path>`.
//
// Why prefer UDS:
//   - No port allocation (TCP loopback collisions are rare but real
//     under heavy parallel plugin spawns).
//   - Lower latency than loopback (no TCP/IP stack roundtrip).
//   - Access control is filesystem permissions on the socket file —
//     a random LAN client can't even speculatively dial.
//
// Why this is opt-in (for now): the plugin protocol still emits a
// numeric TCP port when CODEFLY_AGENT_UDS_PATH isn't set, so old
// hosts and old plugins keep working. New hosts that want the
// performance/security wins flip this option on per-spawn.
//
// Cleanup: the plugin removes the socket file on graceful shutdown;
// the host removes any stale path before listening (belt and
// suspenders against crashed prior runs).
//
// Not supported on Windows; if asked for on Windows the option is a
// no-op (TCP fallback) — UDS support there requires Windows 10+ and
// has different permission semantics.
func WithUDS() LoadOption {
	return func(c *loadConfig) { c.useUDS = true }
}

// WithSandbox attaches an OS-level sandbox to the spawned agent
// process. The sandbox is applied via sandbox.Wrap on the prepared
// exec.Cmd before Start; the resulting plugin runs under bwrap
// (Linux) or sandbox-exec (macOS) with the policy the caller set
// up — read paths, write paths, network policy, unix sockets.
//
// This is the load-bearing security wire: until WithSandbox is
// passed, the plugin process inherits the parent's full ambient
// authority. Toolbox manifests declare a SandboxPolicy; the
// launch layer translates the policy to a sandbox.Sandbox and
// passes it here.
//
// Counterpart: WithoutSandbox() to make an explicit "skip the
// sandbox" decision. Calling Load with neither option logs a
// warning pointing at the security review — Phase 2 will fail
// loud. Don't rely on the silent-default behavior; every callsite
// should pick.
func WithSandbox(sb sandbox.Sandbox) LoadOption {
	return func(c *loadConfig) {
		c.sandbox = sb
		c.sandboxChoiceMade = true
	}
}

// WithoutSandbox is the explicit "skip OS-level confinement" choice.
// Use it ONLY when:
//   - The agent legitimately needs ambient authority (the
//     orchestration agents that spawn user binaries, build
//     containers, etc. — bounded by the user's own permissions).
//   - You're a test that intentionally bypasses confinement to
//     isolate a non-security property.
//
// Every other callsite should use WithSandbox with a real policy.
//
// This is a separate option (not "pass nil to WithSandbox") so the
// security choice is auditable in source — grep for WithoutSandbox
// surfaces exactly the callers that opted out.
func WithoutSandbox() LoadOption {
	return func(c *loadConfig) {
		c.sandbox = nil
		c.sandboxChoiceMade = true
	}
}

// WithPrincipal binds an authority context (the Principal) to the
// spawned plugin. Tool calls dispatched by the plugin go through the
// PDP, which uses the Principal's identity to resolve role grants
// in saas-starter and decide allow/deny.
//
// **What this enables.** Without WithPrincipal, the plugin runs
// "anonymously" — sandbox-confined but with no authority claim.
// With WithPrincipal, the plugin's actions are attributed to the
// Principal in audit logs and authorized against that Principal's
// permissions in saas-starter. This is the load-bearing wire for
// the M3+ permission system.
//
// **What this is NOT.** It's not authentication of the codefly
// host; that's the existing per-spawn token (CODEFLY_AGENT_TOKEN).
// It's not the sandbox; the sandbox stays orthogonal. It's the
// AUTHORITY layer — what's the principal allowed to do, separate
// from what the binary CAN do at the syscall level.
//
// Counterpart: WithoutPrincipal() to make an explicit "no authority
// binding" choice. Calling Load with neither option logs a security
// warning pointing at the migration. Greppable in source: every
// callsite picks; the choice itself is auditable.
//
// The Principal MUST already be validated and have a non-expired
// token. Load itself does not re-verify; that's done upstream where
// the credential was minted.
func WithPrincipal(p *policy.Principal) LoadOption {
	return func(c *loadConfig) {
		c.principal = p
		c.principalChoiceMade = true
	}
}

// WithoutPrincipal is the explicit "spawn this plugin without a
// principal binding" choice. Plugin runs sandboxed but without
// authority claims; tool calls fall back to legacy authorization
// (or none, depending on the PDP wiring at the toolbox level).
//
// Use ONLY when:
//   - The agent is system infrastructure that doesn't act on a
//     specific principal's behalf (orchestration agents, plumbing).
//   - You're a test that intentionally bypasses principal binding
//     to isolate non-permission behavior.
//
// Every other callsite should use WithPrincipal with a real value.
//
// Like WithoutSandbox, this is a separate option (not "pass nil to
// WithPrincipal") so the choice is greppable for security review.
func WithoutPrincipal() LoadOption {
	return func(c *loadConfig) {
		c.principal = nil
		c.principalChoiceMade = true
	}
}

// WithScopedAuthSecret enables the two-level scoped-authz fast
// path on this plugin spawn. The secret is the HMAC key shared
// between the host's GatewayEvaluator (which mints tokens) and
// the plugin's policyguard.Guard (which verifies them).
//
// **Lifecycle:**
//   - Host generates the secret with policy.NewSpawnSecret() (or
//     supplies its own, ≥32 bytes).
//   - Host constructs a GatewayEvaluator with the same secret.
//   - Host calls manager.Load(WithScopedAuthSecret(secret), ...).
//   - manager.Load base64url-encodes the secret, sets the env var
//     CODEFLY_SCOPED_AUTHZ_SECRET on the plugin process.
//   - Plugin's policyguard.Guard reads the env at startup,
//     decodes, uses for HMAC verify on every CallTool.
//
// **What "two-level" buys:**
//   - Hot path: gateway pre-evaluates and mints; plugin verifies
//     locally (microseconds) — no UDS round-trip to the PDP for
//     the outer authorization.
//   - Defense in depth: plugin's PDP is still wired (via
//     WithPermissionsCallback). Token-missing or token-invalid
//     calls fall back to the PDP path. Three independent
//     enforcement layers.
//
// **Security:** secrets shorter than 32 bytes are rejected at
// Load time. HMAC-SHA256's birthday-bound makes <32B secrets a
// real attack surface; refuse rather than silently weaken.
//
// Pass nil/empty to NOT enable the fast path. The plugin's Guard
// will fall back to PDP-via-callback for every call (the
// single-level model — unchanged from current behavior).
//
// See TWO_LEVEL_AUTHZ.md for the full design.
func WithScopedAuthSecret(secret []byte) LoadOption {
	return func(c *loadConfig) {
		c.scopedAuthSecret = secret
	}
}

// WithPermissionsCallback registers the Decider that the spawned
// plugin will call back into for runtime Authorized() checks.
//
// **What this enables.** Inside a plugin handler:
//
//	authorizer := policy.AuthorizerFromContext(ctx)
//	allowed, reason, err := authorizer.Authorized(ctx,
//	    "github.read_secrets", "repo:codefly/x")
//	if !allowed {
//	    // skip secret-dependent behavior gracefully
//	}
//
// The plugin's Authorized() turns into an HTTP/UDS round-trip to
// the host process; the host evaluates against `decider`, returns
// the verdict.
//
// **Why a callback rather than the PDP-on-Toolbox pattern.** The
// PluginRegistration.PDP wraps the OUTER tool dispatch — one
// decision per CallTool. WithPermissionsCallback enables an
// arbitrary number of FINE-GRAINED checks WITHIN a single tool
// invocation. Different concerns; both apply.
//
// **Security model.** The host's PermissionsCallbackServer uses
// the spawn-time principal as the authoritative subject (set via
// WithPrincipal); the plugin cannot impersonate a different
// principal even if compromised. The socket file is owner-only
// (0600) so other users on the host cannot dial.
//
// **Lifecycle.** The callback server is created in Load() before
// process spawn; the socket path goes into the plugin's env. On
// AgentConn.Close, the callback server is shut down and the
// socket file is removed. Orphaned sockets only happen on host
// crash; restart cleans them up.
//
// Pass nil decider → no callback wired (plugin's Authorized()
// fails closed with "callback not configured" — the safe
// default).
func WithPermissionsCallback(decider policy.Decider) LoadOption {
	return func(c *loadConfig) {
		c.permissionsCallback = decider
	}
}

// Load spawns an agent binary, reads the gRPC port from its stdout,
// and establishes a gRPC connection. The agent binary is downloaded
// if not already present.
func Load(ctx context.Context, p *resources.Agent, opts ...LoadOption) (*AgentConn, error) {
	// Check nil BEFORE constructing the wool field — p.Identifier()
	// would panic on a nil receiver and obscure the real diagnostic.
	if p == nil {
		return nil, fmt.Errorf("%w: nil receiver passed to Load", ErrAgentNil)
	}
	// Tidy UDS sockets left behind by previously-crashed CLIs (once per process).
	sweepStaleAgentSocketsOnce()
	w := wool.Get(ctx).In("manager.Load", wool.Field("agent", p.Identifier()))

	cfg := defaultLoadConfig()
	for _, o := range opts {
		o(&cfg)
	}
	// Process-wide audit warnings: emitted once per process and identical
	// for every agent, so they must NOT carry the per-agent field — otherwise
	// whichever agent hits Load first permanently owns the message.
	warnW := wool.Get(ctx).In("manager.Load")
	if !cfg.sandboxChoiceMade {
		// Audit-visible warning: caller didn't pick WithSandbox or
		// WithoutSandbox, so the plugin runs with parent ambient
		// authority by accident-of-default. Phase 2 will hard-fail
		// here. Until every dependent caller is migrated, log loud
		// and continue. Emitted once per process: the message is
		// identical for every agent, so a 15-service run printing it
		// 15× is pure noise (#19).
		sandboxWarnOnce.Do(func() {
			warnW.Warn("security: manager.Load missing explicit sandbox choice",
				wool.Field("default", "native"),
			)
		})
	}
	if !cfg.principalChoiceMade {
		// Same audit-of-defaults pattern as sandbox: every callsite
		// MUST pick WithPrincipal or WithoutPrincipal. Until the M3
		// rollout completes (every caller migrated), log + continue.
		// Deduped to once per process for the same reason as above.
		principalWarnOnce.Do(func() {
			warnW.Warn("security: manager.Load missing explicit principal choice",
				wool.Field("default", "without authority binding"),
			)
		})
	}

	bin, err := p.Path(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot compute agent path")
	}

	// Resolve binary: local cache → Nix flake → OCI registry → GitHub release.
	if _, err := exec.LookPath(bin); err != nil {
		pulled := false
		// Try Nix store first (AGENT_NIX_FLAKE env var). Content-addressed,
		// cross-platform, no manual version tag management.
		if store := NewNixStoreFromEnv(slog.Default()); store != nil {
			if pullPath, pullErr := store.Pull(ctx, p); pullErr == nil {
				bin = pullPath
				pulled = true
				w.Debug("agent realized via nix flake", wool.Path(bin))
			} else {
				w.Debug("nix pull failed, trying OCI", wool.Field("error", pullErr.Error()))
			}
		}
		// Then OCI store (AGENT_REGISTRY env var).
		if !pulled {
			if store := NewOCIStoreFromEnv(slog.Default()); store != nil {
				if pullPath, pullErr := store.Pull(ctx, p); pullErr == nil {
					bin = pullPath
					pulled = true
					w.Debug("agent pulled from OCI registry", wool.Path(bin))
				} else {
					w.Debug("OCI pull failed, trying GitHub", wool.Field("error", pullErr.Error()))
				}
			}
		}
		if !pulled {
			if err := Download(ctx, p); err != nil {
				return nil, w.Wrapf(fmt.Errorf("%w: %v", ErrAgentBinaryNotFound, err),
					"cannot download agent (tried Nix + OCI + GitHub)")
			}
		}
	}

	// --- Spawn the agent binary ---
	// Use exec.Command (NOT CommandContext) because the agent process must
	// outlive the load context. The caller may pass a short-lived context
	// (e.g. 60s timeout) for the load handshake, but the agent should keep
	// running until explicitly closed. Lifecycle is managed via Close().
	cmd := exec.Command(bin)
	// Put the agent (and everything it spawns) in its own process group.
	// Without this the agent inherits codefly's pgid, which in turn
	// inherits the caller's (e.g. Claude Code's Bash tool) — a single
	// stray signal to that pgroup then cascades into every agent and
	// every user binary. Own pgid also lets Close() send SIGTERM to
	// the negative pid and reap the whole agent subtree atomically.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	if cfg.workDir != "" {
		cmd.Dir = cfg.workDir
		cmd.Env = append(cmd.Env, "CODEFLY_AGENT_WORKDIR="+cfg.workDir)
	}
	if len(cfg.extraEnv) > 0 {
		// Caller-supplied env wins over parent inheritance — exec
		// honors the last KEY=VALUE for any duplicate key.
		cmd.Env = append(cmd.Env, cfg.extraEnv...)
	}
	// UDS path setup: pick a unique socket path and pass it to the
	// plugin via env. Plugin-side: agents.Serve listens on the path
	// instead of TCP. Host-side: we already accept "unix:<path>" in
	// the handshake parser. Cleanup on the host is a defensive
	// pre-Listen Remove on the plugin side; the host doesn't need to
	// race the plugin to clear stale files.
	udsPath := ""
	if cfg.useUDS && runtime.GOOS != "windows" {
		// Per-spawn dir keeps paths short (UDS limit is ~104 chars on
		// macOS) and lets the host clean up the whole dir if needed.
		dir := filepath.Join(os.TempDir(), "codefly-uds")
		_ = os.MkdirAll(dir, 0o700)
		udsPath = filepath.Join(dir, fmt.Sprintf("agent-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
		cmd.Env = append(cmd.Env, "CODEFLY_AGENT_UDS_PATH="+udsPath)
	}
	_ = udsPath // referenced below for diagnostics; kept for future cleanup hook

	// Per-spawn auth token. Random 32 bytes hex-encoded → 64-char
	// bearer in CODEFLY_AGENT_TOKEN env. Plugin's gRPC server
	// requires a matching token in metadata on every call; without
	// it, anyone who can connect to our UDS / loopback port can
	// drive the plugin (UDS file-permission ACL is defense-in-depth,
	// not the only line).
	//
	// crypto/rand → guaranteed unbiased. Hex encoding (not base64)
	// avoids any case where shell quoting / env-var escaping could
	// mangle the bearer in transit.
	authToken, err := mintAgentToken()
	if err != nil {
		return nil, w.Wrapf(err, "mint agent token")
	}
	cmd.Env = append(cmd.Env, "CODEFLY_AGENT_TOKEN="+authToken)

	// Principal token (M3). Distinct from CODEFLY_AGENT_TOKEN: the
	// agent token authenticates the host-to-plugin connection; the
	// principal token authorizes the plugin's actions. Plugin
	// reads the token from the env, attaches it as the x-codefly-
	// principal metadata on the way back when emitting tool calls
	// downstream. The PDP verifies it.
	//
	// Format until M6 lands: a JWT-shaped token signed by saas-
	// starter's auth server. M6 swaps to Biscuit. Until M3's
	// SaasPDP wires the verifier, we pass the *raw* Principal
	// fields encoded — this lets unit tests work against FakePDP
	// without a JWT signer in scope. Production wiring overrides
	// the encoding at the manager-init time.
	if cfg.principal != nil {
		token, terr := policy.EncodePrincipalToken(cfg.principal)
		if terr != nil {
			return nil, w.Wrapf(terr, "encode principal token")
		}
		cmd.Env = append(cmd.Env, "CODEFLY_PRINCIPAL_TOKEN="+token)
		cmd.Env = append(cmd.Env, "CODEFLY_PRINCIPAL_ID="+cfg.principal.ID)
	}

	// Scoped-authz secret. When the host enables the two-level
	// fast path, base64url-encode the secret and pass via env.
	// Plugin's policyguard.Guard reads it at startup. Refuse
	// short secrets — HMAC strength below 32 bytes is a real
	// risk we don't silently accept.
	if len(cfg.scopedAuthSecret) > 0 {
		if len(cfg.scopedAuthSecret) < 32 {
			return nil, w.NewError("scoped-auth secret must be >= 32 bytes (got %d)", len(cfg.scopedAuthSecret))
		}
		encoded := base64.RawURLEncoding.EncodeToString(cfg.scopedAuthSecret)
		cmd.Env = append(cmd.Env, "CODEFLY_SCOPED_AUTHZ_SECRET="+encoded)
	}

	// Permissions callback (M-callback). When the host registered a
	// Decider via WithPermissionsCallback, stand up the UDS server
	// HERE — before cmd.Start so the path is in env, but after env is
	// otherwise prepared.
	//
	// Cleanup discipline: if anything past this point fails (cmd.Start,
	// pipe wiring, gRPC dial), we MUST close the callback server so
	// the socket doesn't leak. Defer-on-error handles it.
	var permsCallback *policy.PermissionsCallbackServer
	if cfg.permissionsCallback != nil {
		permsCallback, err = policy.NewPermissionsCallbackServer(cfg.permissionsCallback)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create permissions callback server")
		}
		// Bind the spawn-time principal as the trusted subject — the
		// plugin cannot impersonate a different principal even if it
		// claims one in the request body. Captured by closure so a
		// later WithPrincipal would have to be re-issued at load.
		boundPrincipal := cfg.principal
		permsCallback.WithPrincipalProvider(func() *policy.Principal {
			return boundPrincipal
		})
		cmd.Env = append(cmd.Env, "CODEFLY_PERMISSIONS_SOCKET="+permsCallback.SocketPath())
	}
	// Cleanup discipline: any subsequent error path in Load must close the
	// callback server so the UDS file and goroutine don't leak. We flip
	// loadSucceeded right before the successful return; until then, the
	// defer below closes the server. Don't rely on a scoped `err` here —
	// downstream sites use `if err := …; err != nil` (shadowed) and
	// `killAndDescribe(...)` which returns without touching outer state,
	// so an `err != nil` check would silently miss every failure path.
	loadSucceeded := false
	defer func() {
		if !loadSucceeded && permsCallback != nil {
			_ = permsCallback.Close()
		}
	}()

	if ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); ep != "" {
		cmd.Env = append(cmd.Env,
			"OTEL_EXPORTER_OTLP_ENDPOINT="+ep,
			"OTEL_SERVICE_NAME=codefly-agent-"+string(p.Kind),
		)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, w.Wrapf(err, "cannot create stdout pipe")
	}

	// Capture stderr into a bounded ring buffer so we can include the
	// tail in error messages without unbounded memory growth.
	stderrBuf := newRingBuffer(stderrCapacity)
	if cfg.logWriter != nil {
		cmd.Stderr = io.MultiWriter(stderrBuf, cfg.logWriter)
	} else {
		cmd.Stderr = stderrBuf
	}

	// Sandbox application — must happen AFTER the stdout pipe + stderr
	// buffer are wired but BEFORE Start. sandbox.Wrap mutates cmd.Path
	// and cmd.Args (it rewrites the invocation as `bwrap <flags> --
	// <original cmd>` on Linux or `sandbox-exec -p <profile> <original
	// cmd>` on macOS). Stdin/Stdout/Stderr/Env/Dir/SysProcAttr are
	// preserved by the wrap; the previously-attached pipes survive.
	//
	// If the wrap fails (backend missing, malformed policy), surface
	// the error before any process is created — fail loud + clear,
	// don't silently skip the sandbox.
	if cfg.sandbox != nil {
		if err := cfg.sandbox.Wrap(cmd); err != nil {
			return nil, w.Wrapf(err, "cannot wrap agent in sandbox (%s)", cfg.sandbox.Backend())
		}
	}

	if err := cmd.Start(); err != nil {
		return nil, w.Wrapf(fmt.Errorf("%w: %v", ErrAgentSpawn, err),
			"cannot start agent binary: %s", bin)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
		// Track the agent's pgroup so an ungraceful CLI death (SIGKILL on
		// parent, terminal force-closed) doesn't leave the whole agent
		// subtree orphaned at PPID=1. The next `codefly run` sweep reaps
		// any pgroup whose owning CLI is dead.
		if perr := runnersbase.WritePgidFile(pid, cmd.Dir, []string{bin}); perr != nil {
			w.Warn("could not persist agent pgid", wool.Field("err", perr))
		}
	}

	w.Trace("agent process started", wool.Field("pid", pid), wool.Path(bin))

	// killAndDescribe kills the agent and returns an error wrapping the
	// supplied sentinel + reason + the captured stderr tail. Callers
	// switch on the sentinel via errors.Is; the message preserves
	// reason + stderr for human readers.
	killAndDescribe := func(sentinel error, reason string) error {
		if cmd.Process != nil {
			killAgentGroup(cmd.Process.Pid)
		}
		_ = cmd.Wait()
		// Remove the pgid tracking file written right after Start. The reaper
		// goroutine (which would remove it) is only started on the success
		// path, so every failure path leaked the file. A stale file can later
		// make the orphan sweep SIGKILL a recycled, unrelated process group.
		_ = runnersbase.RemovePgidFile(pid)
		tail := stderrBuf.String()
		if tail != "" {
			return w.Wrapf(fmt.Errorf("%w: %s", sentinel, reason),
				"stderr tail: %s", tail)
		}
		return w.Wrapf(fmt.Errorf("%w: %s", sentinel, reason),
			"no stderr output captured")
	}

	// --- Read handshake with timeout ---
	type handshakeResult struct {
		line string
		err  error
	}
	handshakeCh := make(chan handshakeResult, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		if !scanner.Scan() {
			scanErr := scanner.Err()
			if scanErr != nil {
				handshakeCh <- handshakeResult{err: scanErr}
			} else {
				handshakeCh <- handshakeResult{err: io.EOF}
			}
			return
		}
		handshakeCh <- handshakeResult{line: strings.TrimSpace(scanner.Text())}
	}()

	var line string
	select {
	case res := <-handshakeCh:
		if res.err != nil {
			return nil, killAndDescribe(ErrAgentHandshakeMalformed,
				fmt.Sprintf("agent did not complete handshake: %v", res.err))
		}
		line = res.line
	case <-time.After(cfg.startupTimeout):
		return nil, killAndDescribe(ErrAgentHandshakeTimeout,
			fmt.Sprintf("agent did not print handshake within %s", cfg.startupTimeout))
	case <-ctx.Done():
		return nil, killAndDescribe(ErrAgentHandshakeTimeout,
			fmt.Sprintf("context cancelled while waiting for agent handshake: %v", ctx.Err()))
	}

	// --- Parse handshake: "VERSION|<endpoint>" ---
	addr, parseErr := parseAgentHandshake(line)
	if parseErr != nil {
		// Distinguish version mismatch from malformed shape.
		if errors.Is(parseErr, errAgentVersionMismatch) {
			return nil, killAndDescribe(ErrAgentVersionMismatch, parseErr.Error())
		}
		return nil, killAndDescribe(ErrAgentHandshakeMalformed, parseErr.Error())
	}

	// --- Connect via gRPC with a health check ---
	// Attach the per-spawn token to every outgoing call via a
	// per-RPC-credentials provider. The plugin's auth interceptor
	// rejects calls without it (Unauthenticated). Health checks are
	// exempt on the server side, so the readiness probe below works
	// even if metadata propagation has a corner case.
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithPerRPCCredentials(bearerCreds{token: authToken}),
	)
	if err != nil {
		return nil, killAndDescribe(ErrAgentDialTimeout,
			fmt.Sprintf("cannot create gRPC client for %s: %v", addr, err))
	}

	// Verify the connection actually becomes ready within the dial timeout.
	dialCtx, dialCancel := context.WithTimeout(ctx, cfg.dialTimeout)
	defer dialCancel()

	conn.Connect()
	if !waitForReady(dialCtx, conn) {
		_ = conn.Close()
		return nil, killAndDescribe(ErrAgentDialTimeout,
			fmt.Sprintf("gRPC connection to %s did not become ready within %s",
				addr, cfg.dialTimeout))
	}

	// --- Build result and register ---
	agentConn := &AgentConn{
		conn:                conn,
		cmd:                 cmd,
		info:                &ProcessInfo{PID: pid},
		stderrBuf:           stderrBuf,
		done:                make(chan struct{}),
		permissionsCallback: permsCallback, // nil when WithPermissionsCallback wasn't passed
	}

	// Reaper goroutine: waits for the process to exit and logs unexpected
	// terminations. This prevents zombie processes when the agent dies on
	// its own. Uses a fresh background context so the reaper doesn't
	// hold onto the caller's (likely timed-out) context for the entire
	// life of the agent process.
	reaperCtx := context.Background()
	go func() {
		defer close(agentConn.done)
		waitErr := cmd.Wait()
		// Agent process is confirmed dead — drop its pgid tracking file.
		// Only the sweep's orphan check depends on this file; missing it
		// just means the next sweep treats it as an already-dead group.
		if pid > 0 {
			_ = runnersbase.RemovePgidFile(pid)
		}
		if waitErr != nil {
			// Log at debug – the consumer will observe errors through the
			// gRPC connection or explicit health checks.
			rw := wool.Get(reaperCtx).In("manager.reaper", wool.Field("pid", pid))
			rw.Warn("agent process exited unexpectedly",
				wool.Field("error", waitErr.Error()),
				wool.Field("stderr_tail", stderrBuf.String()))
		}
	}()

	activeMu.Lock()
	active[p.Unique()] = agentConn
	activeMu.Unlock()

	w.Trace("connected to agent", wool.Field("addr", addr), wool.Field("pid", pid))

	loadSucceeded = true
	return agentConn, nil
}

// waitForReady blocks until conn reaches connectivity.Ready AND the
// agent's grpc.health.v1 endpoint reports SERVING, or ctx expires.
//
// The two-step matters: connectivity.Ready means the TCP connection
// is up and TLS (if any) succeeded — but the server can still be in
// the middle of registering services. The health Check is the agent
// telling us "all my services are wired and I'm accepting RPCs."
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
	// Real health check on top. Empty service name = the wildcard
	// "this server is up" check that core/agents/agents.go's Serve()
	// registers via SetServingStatus("", SERVING).
	hc := healthpb.NewHealthClient(conn)
	resp, err := hc.Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		// Older agents (before health-server registration landed) won't
		// have the health endpoint; treat that as "ready" so we don't
		// regress them. Newer agents that ARE failing health get caught
		// by the Status check below.
		return ctx.Err() == nil
	}
	return resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
}

// ---------------------------------------------------------------------------
// ringBuffer – a simple fixed-capacity circular byte buffer that keeps
// only the most recent bytes written. It implements io.Writer so it can
// be plugged directly into cmd.Stderr.
// ---------------------------------------------------------------------------

type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	pos  int
	full bool
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, capacity)}
}

// Write appends p to the ring buffer, overwriting the oldest bytes when
// the buffer is full.
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	cap := len(r.buf)

	// If the incoming data is larger than the buffer, only keep the tail.
	if n >= cap {
		copy(r.buf, p[n-cap:])
		r.pos = 0
		r.full = true
		return n, nil
	}

	// Copy as much as fits from pos forward.
	first := copy(r.buf[r.pos:], p)
	if first < n {
		// Wrapped around.
		copy(r.buf, p[first:])
		r.full = true
	}
	r.pos = (r.pos + n) % cap
	if r.pos < n && !r.full {
		r.full = true
	}
	return n, nil
}

// String returns the buffered bytes in chronological order.
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		return strings.TrimSpace(string(r.buf[:r.pos]))
	}

	var out bytes.Buffer
	out.Write(r.buf[r.pos:])
	out.Write(r.buf[:r.pos])
	return strings.TrimSpace(out.String())
}

// errAgentVersionMismatch is a sentinel for handshake-version errors;
// Load distinguishes these from malformed-line errors so it can map
// to the right exported error (ErrAgentVersionMismatch vs
// ErrAgentHandshakeMalformed).
var errAgentVersionMismatch = errors.New("agent protocol version mismatch")

// parseAgentHandshake parses a "VERSION|<endpoint>" line emitted by
// agents.Serve and returns the gRPC dial address.
//
// Endpoint forms:
//   - numeric port (legacy TCP):    "54321"        → "127.0.0.1:54321"
//   - UDS URI (preferred):          "unix:/path"   → "unix:/path" (verbatim;
//     grpc.NewClient resolves)
//
// Both forms are accepted so a new host can dial both old (TCP-only)
// plugins and new (UDS-capable) plugins.
func parseAgentHandshake(line string) (addr string, err error) {
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid agent handshake line: %q", line)
	}
	version, verr := strconv.Atoi(parts[0])
	if verr != nil || version != agents.ProtocolVersion {
		return "", fmt.Errorf("%w: %q (expected %d)", errAgentVersionMismatch,
			parts[0], agents.ProtocolVersion)
	}
	endpoint := parts[1]
	if strings.HasPrefix(endpoint, "unix:") {
		// gRPC's unix resolver accepts the URI verbatim.
		return endpoint, nil
	}
	port, perr := strconv.Atoi(endpoint)
	if perr != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("invalid agent endpoint: %q (expected numeric port or unix:<path>)", endpoint)
	}
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

// mintAgentToken returns 32 random bytes hex-encoded. 256 bits of
// entropy from crypto/rand — overwhelmingly more than the auth-
// against-local-attacker threat model demands, but cheap.
//
// Hex (not base64) so the token survives env-var quoting through
// every shell + exec layer between host and plugin without any
// special-character footguns.
func mintAgentToken() (string, error) {
	var buf [32]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// bearerCreds is a grpc.PerRPCCredentials that attaches the per-
// spawn token to every outgoing RPC. RequireTransportSecurity
// returns false because we run over UDS / loopback — the token
// is the auth, the transport doesn't need TLS.
type bearerCreds struct {
	token string
}

func (b bearerCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{agents.AuthMetadataKey: b.token}, nil
}

func (b bearerCreds) RequireTransportSecurity() bool { return false }
