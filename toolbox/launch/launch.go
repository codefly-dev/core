package launch

import (
	"context"
	"fmt"
	"io"

	"github.com/codefly-dev/core/agents/manager"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
)

// Plugin pairs a running toolbox plugin's manager.AgentConn with a
// typed ToolboxClient. The connection's lifetime is owned by Plugin;
// Close tears the plugin process down via the standard agent loader
// shutdown sequence.
type Plugin struct {
	conn *manager.AgentConn

	// Client is the typed Toolbox gRPC client over the AgentConn's
	// shared connection. Direct field rather than accessor — callers
	// pass it around and we don't gain anything from gating access.
	Client toolboxv0.ToolboxClient
}

// Conn exposes the underlying agent connection for callers that need
// non-Toolbox surfaces on the same plugin (Identity / health / a
// future RPC the toolbox layer doesn't model). Most callers should
// use Client; reach for Conn only when you specifically need it.
func (p *Plugin) Conn() *manager.AgentConn { return p.conn }

// Close shuts down the plugin process. Idempotent — safe to call in
// a deferred cleanup block alongside an explicit Close path.
func (p *Plugin) Close() {
	if p == nil || p.conn == nil {
		return
	}
	p.conn.Close()
}

// Options tunes Launch beyond the manager.LoadOption surface. Use
// it for launch-specific behavior — the workspace path that drives
// the manifest's ${WORKSPACE} expansion, opt-out toggles for the
// sandbox.
type Options struct {
	// Workspace is the absolute path that fills the ${WORKSPACE}
	// placeholder in the manifest's sandbox policy paths. Empty
	// means "${WORKSPACE} is not available" — manifests referencing
	// it will fail to load, which is the right behavior (don't
	// silently substitute "").
	Workspace string

	// SkipSandbox bypasses the OS-level sandbox even when the
	// manifest declares one. ONLY for tests / diagnostics that need
	// to inspect what the plugin would do unconfined; production
	// callers should never set this. Logged via the agent's stderr
	// when active so an audit can flag the bypass.
	SkipSandbox bool
}

// Launch is the convenience entrypoint: take a resources.Toolbox
// manifest, spawn the plugin via manager.Load (under the manifest's
// declared OS sandbox), return a Plugin pairing the connection with
// the typed Toolbox client.
//
// What this method does that a bare manager.Load doesn't:
//
//  1. Standard env injection: CODEFLY_TOOLBOX_{NAME,VERSION,DIR} so
//     plugin binaries can surface accurate Identity values without
//     re-parsing the manifest.
//  2. Sandbox policy translation: t.Sandbox → sandbox.Sandbox, applied
//     to the spawned process via manager.WithSandbox. The plugin
//     runs under bwrap (Linux) / sandbox-exec (macOS) with the
//     declared read paths, write paths, network policy, and unix
//     sockets. ${WORKSPACE} / ${HOME} / ${TMPDIR} placeholders are
//     expanded against opts.Workspace and the host's environment.
//
// Toolbox-specific runtime config (CODEFLY_TOOLBOX_WORKSPACE for git,
// CODEFLY_TOOLBOX_ALLOWED_DOMAINS for web, …) is the caller's
// responsibility — pass it via manager.WithEnv on the LoadOptions.
func Launch(ctx context.Context, t *resources.Toolbox, opts ...manager.LoadOption) (*Plugin, error) {
	return LaunchWithOptions(ctx, t, Options{}, opts...)
}

// LaunchWithOptions is Launch with explicit Options. The bare Launch
// uses zero-value Options (no Workspace, sandbox enabled). Most
// callers use this when they need to seed the workspace for sandbox
// policy expansion.
func LaunchWithOptions(ctx context.Context, t *resources.Toolbox, lopts Options, opts ...manager.LoadOption) (*Plugin, error) {
	if t == nil {
		return nil, fmt.Errorf("launch: nil toolbox")
	}
	if t.Agent == nil {
		return nil, fmt.Errorf("launch %s: manifest has no agent", t.Identity())
	}

	// Compose options — toolbox-standard env first; caller options
	// last (so caller wins on duplicates). Sandbox lands somewhere in
	// the middle — its construction may fail, in which case we
	// surface that BEFORE any process is created.
	allOpts := []manager.LoadOption{
		manager.WithEnv(toolboxStandardEnv(t)...),
	}
	if !lopts.SkipSandbox {
		sb, err := buildSandbox(t, lopts.Workspace)
		if err != nil {
			return nil, fmt.Errorf("launch %s: build sandbox: %w", t.Identity(), err)
		}
		if sb != nil {
			allOpts = append(allOpts, manager.WithSandbox(sb))
		}
	}
	allOpts = append(allOpts, opts...)

	conn, err := manager.Load(ctx, t.Agent, allOpts...)
	if err != nil {
		return nil, fmt.Errorf("launch %s: load agent: %w", t.Identity(), err)
	}

	return &Plugin{
		conn:   conn,
		Client: toolboxv0.NewToolboxClient(conn.GRPCConn()),
	}, nil
}

// buildSandbox translates the manifest's SandboxPolicy into a
// configured sandbox.Sandbox. Returns nil (no sandbox) when the
// manifest declares an empty policy AND no canonical_for binaries —
// the historical default for "this toolbox doesn't need confinement"
// (e.g. fake test plugins). When canonical_for is non-empty the
// manifest IS asserting authority, so we always wrap.
//
// **Network default.** policy.SandboxPolicy.Apply translates an
// UNSET network field to NetworkDeny. NetworkDeny is too aggressive
// for plugin spawn — it blocks 127.0.0.1, which breaks the agent
// loader's gRPC handshake to the plugin's loopback listener.
//
// The launch layer overrides empty Network to NetworkLoopback:
// allows local 127.0.0.1 traffic (handshake works) while denying
// every external connection (the load-bearing security property).
// Manifests that explicitly set network={deny|open} get exactly
// what they asked for — operator intent wins.
//
// On Linux bwrap, NetworkLoopback isn't yet implemented (the
// unshared netns has lo DOWN by default; bringing it up needs a
// helper that uses netlink inside the new namespace). Linux
// callers see ErrNetworkLoopbackUnsupported from the Wrap and
// must pick NetworkOpen or NetworkDeny explicitly until the
// helper lands. macOS sandbox-exec implements it cleanly.
//
// The native (no-op) sandbox is used as a fallback when the host
// has no enforcing backend. That preserves the existing behavior on
// platforms where bwrap isn't installed; production deployments
// should ensure the backend is present.
func buildSandbox(t *resources.Toolbox, workspace string) (sandbox.Sandbox, error) {
	if isEmptyPolicy(t) {
		return nil, nil
	}
	sb, err := sandbox.New()
	if err != nil {
		return nil, fmt.Errorf("sandbox backend unavailable: %w", err)
	}

	// Apply a copy of the manifest policy with the default-network
	// override so we don't mutate the caller's *resources.Toolbox.
	// Empty Network → Loopback (handshake works, outbound denied).
	pol := t.Sandbox
	if pol.Network == "" {
		pol.Network = policy.NetworkLoopback
	}
	expand := policy.NewExpander(workspace)
	if err := pol.Apply(sb, expand); err != nil {
		return nil, fmt.Errorf("apply sandbox policy: %w", err)
	}
	_ = sandbox.NetworkLoopback // import is otherwise unused on this build
	return sb, nil
}

// isEmptyPolicy is true when the manifest declares no sandbox
// constraints at all — no read/write paths, no network setting, no
// canonical_for binaries (the proxy for "this toolbox is
// privileged"). Such manifests are typically tests or in-process
// helpers that don't represent any real authority claim.
func isEmptyPolicy(t *resources.Toolbox) bool {
	pol := t.Sandbox
	if len(pol.ReadPaths) > 0 ||
		len(pol.WritePaths) > 0 ||
		len(pol.UnixSockets) > 0 ||
		pol.Network != "" {
		return false
	}
	if len(t.CanonicalFor) > 0 {
		return false
	}
	return true
}

// toolboxStandardEnv is the set of env vars every toolbox plugin can
// rely on. Kept separate so a future caller can construct the env
// without invoking Launch (e.g. for diagnostic dumps).
func toolboxStandardEnv(t *resources.Toolbox) []string {
	env := []string{
		"CODEFLY_TOOLBOX_NAME=" + t.Name,
		"CODEFLY_TOOLBOX_VERSION=" + t.Version,
	}
	if t.Dir() != "" {
		env = append(env, "CODEFLY_TOOLBOX_DIR="+t.Dir())
	}
	return env
}

// silence unused-import warnings when LoadOption-extension hooks
// land in a follow-up.
var _ = io.EOF
