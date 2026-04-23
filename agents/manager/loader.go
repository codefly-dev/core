package manager

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
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
}

// GRPCConn returns the shared gRPC connection to the agent.
func (c *AgentConn) GRPCConn() *grpc.ClientConn { return c.conn }

// ProcessInfo returns the agent's process metadata.
func (c *AgentConn) ProcessInfo() *ProcessInfo { return c.info }

// gracefulShutdownTimeout is how long Close waits after SIGTERM for the
// agent to exit cleanly before falling back to SIGKILL. Must be larger
// than the agent's own internal stop timeout (5s in agents.Serve) plus
// the NativeProc.Stop SIGTERM/SIGKILL grace window (~7s) so the agent
// has a real chance to reap its children before we force-kill it.
const gracefulShutdownTimeout = 15 * time.Second

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
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}

	// Step 1: SIGTERM. The agent's signal handler in agents.Serve will
	// call Runtime.Stop on its child NativeProcs and then GracefulStop
	// the gRPC server. The reaper's cmd.Wait returns once the agent
	// process exits, closing `done`.
	_ = c.cmd.Process.Signal(os.Interrupt)
	if c.done == nil {
		// No reaper to wait on — best-effort kill and bail.
		killAgentGroup(c.cmd.Process.Pid)
		return
	}

	select {
	case <-c.done:
		return
	case <-time.After(gracefulShutdownTimeout):
		// Agent didn't respond to SIGTERM in time — force kill the whole
		// pgroup (agent + any still-running user binaries it spawned) and
		// wait for the reaper so we don't leave zombies behind.
		killAgentGroup(c.cmd.Process.Pid)
		<-c.done
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

// Load spawns an agent binary, reads the gRPC port from its stdout,
// and establishes a gRPC connection. The agent binary is downloaded
// if not already present.
func Load(ctx context.Context, p *resources.Agent, opts ...LoadOption) (*AgentConn, error) {
	// Check nil BEFORE constructing the wool field — p.Identifier()
	// would panic on a nil receiver and obscure the real diagnostic.
	if p == nil {
		return nil, fmt.Errorf("%w: nil receiver passed to Load", ErrAgentNil)
	}
	w := wool.Get(ctx).In("manager.Load", wool.Field("agent", p.Identifier()))

	cfg := defaultLoadConfig()
	for _, o := range opts {
		o(&cfg)
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

	if err := cmd.Start(); err != nil {
		return nil, w.Wrapf(fmt.Errorf("%w: %v", ErrAgentSpawn, err),
			"cannot start agent binary: %s", bin)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	w.Debug("agent process started", wool.Field("pid", pid), wool.Path(bin))

	// killAndDescribe kills the agent and returns an error wrapping the
	// supplied sentinel + reason + the captured stderr tail. Callers
	// switch on the sentinel via errors.Is; the message preserves
	// reason + stderr for human readers.
	killAndDescribe := func(sentinel error, reason string) error {
		if cmd.Process != nil {
			killAgentGroup(cmd.Process.Pid)
		}
		_ = cmd.Wait()
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

	// --- Parse handshake: "VERSION|PORT" ---
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return nil, killAndDescribe(ErrAgentHandshakeMalformed,
			fmt.Sprintf("invalid agent handshake line: %q", line))
	}

	version, err := strconv.Atoi(parts[0])
	if err != nil || version != agents.ProtocolVersion {
		return nil, killAndDescribe(ErrAgentVersionMismatch,
			fmt.Sprintf("unsupported agent protocol version: %q (expected %d)",
				parts[0], agents.ProtocolVersion))
	}

	port, err := strconv.Atoi(parts[1])
	if err != nil || port <= 0 || port > 65535 {
		return nil, killAndDescribe(ErrAgentHandshakeMalformed,
			fmt.Sprintf("invalid agent port: %q", parts[1]))
	}

	// --- Connect via gRPC with a health check ---
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
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
		conn:      conn,
		cmd:       cmd,
		info:      &ProcessInfo{PID: pid},
		stderrBuf: stderrBuf,
		done:      make(chan struct{}),
	}

	// Reaper goroutine: waits for the process to exit and logs unexpected
	// terminations. This prevents zombie processes when the agent dies on
	// its own.
	go func() {
		defer close(agentConn.done)
		waitErr := cmd.Wait()
		if waitErr != nil {
			// Log at debug – the consumer will observe errors through the
			// gRPC connection or explicit health checks.
			rw := wool.Get(ctx).In("manager.reaper", wool.Field("pid", pid))
			rw.Warn("agent process exited unexpectedly",
				wool.Field("error", waitErr.Error()),
				wool.Field("stderr_tail", stderrBuf.String()))
		}
	}()

	activeMu.Lock()
	active[p.Unique()] = agentConn
	activeMu.Unlock()

	w.Debug("connected to agent", wool.Field("addr", addr), wool.Field("pid", pid))

	return agentConn, nil
}

// waitForReady blocks until conn reaches connectivity.Ready or ctx expires.
func waitForReady(ctx context.Context, conn *grpc.ClientConn) bool {
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return true
		}
		if !conn.WaitForStateChange(ctx, state) {
			// Context expired or was cancelled.
			return false
		}
	}
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
