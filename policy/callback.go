package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// =====================================================================
// Permission callback channel: plugin → host
// =====================================================================
//
// The codefly host spawns plugins; the plugin needs a way to ask
// "is this principal authorized for action X on resource Y?" at
// runtime (e.g. for sub-operation gating within a tool that's
// already outer-authorized).
//
// **Why a callback channel rather than baking the PDP into the plugin.**
//   - Plugins must NOT depend on saas-starter's gen client. The
//     core/policy package keeps no compile-time tie to saas-starter;
//     the host owns the wire to the auth backend.
//   - Permission state is mutable. A grant revoked between calls
//     must be visible to the next Authorized() check; the host's
//     PDP cache (with TTL) is the canonical place for that
//     reasoning.
//   - One PDP per host means one place to instrument, monitor,
//     audit. N plugins each calling saas-starter directly means
//     N copies of the cache and the metrics.
//
// **Why UDS + HTTP+JSON rather than gRPC.**
//   - Zero new proto definitions; no codefly generate roundtrip.
//   - One file shapes the entire surface (this one).
//   - HTTP is universal — bindings exist for every language. If
//     a non-Go plugin SDK ever lands, it speaks the same wire.
//   - UDS gives us file-permission ACL for the socket — the
//     callback is bounded to processes the OS lets see the path.
//   - We cache aggressively in the plugin-side Authorizer; the
//     callback is rare on the hot path.
//
// **Why HTTP+JSON specifically (not net/rpc, gob, etc.).**
//   - JSON is debuggable. `nc -U /tmp/codefly-perms.sock` + raw
//     curl-style requests work for incident debugging.
//   - HTTP gives us readable status codes (200 / 403 / 500) that
//     map cleanly to (allow / deny / fail-closed).
//   - Forward-compat: future fields added to AuthorizeRequest
//     don't break older plugins; JSON ignores unknown keys.
//
// The callback is INTERNAL machinery. Plugin authors interact via
// the Authorizer interface (above), not this file's primitives.

// EnvPermissionsSocket carries the path to the host's permission
// callback UDS. Set by manager.Load when spawning a plugin if the
// host has a Decider configured. Plugin-side AuthorizerFromEnv
// reads it to dial.
//
// Empty/unset → the plugin runs without a callback channel.
// Authorized() then fails closed (the safe default — no ambient
// allow).
const EnvPermissionsSocket = "CODEFLY_PERMISSIONS_SOCKET"

// AuthorizeRequest is the JSON payload from plugin → host. Field
// names map to saas-starter's Decide RPC for trivial pass-through.
type AuthorizeRequest struct {
	PrincipalID string `json:"principal_id"`
	Action      string `json:"action"`
	Resource    string `json:"resource,omitempty"`
	OrgID       string `json:"org_id,omitempty"`
}

// AuthorizeResponse is the JSON payload from host → plugin.
//
// HTTP semantics:
//   - 200 + Allowed=true  → plugin proceeds
//   - 200 + Allowed=false → policy deny; plugin surfaces Reason
//   - 5xx                 → fail-closed; plugin treats as deny
//     (never as allow)
type AuthorizeResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// =====================================================================
// Host-side server
// =====================================================================

// PermissionsCallbackServer is the host-side HTTP server that
// answers Authorize requests from spawned plugins. One server per
// plugin spawn; the socket path is unique per spawn so concurrent
// plugins don't share a listener (and can't cross-query each
// other's PDPs).
//
// **Lifecycle:**
//
//	srv, err := policy.NewPermissionsCallbackServer(decider)
//	defer srv.Close()
//	// pass srv.SocketPath() into env, spawn plugin
//
// Close removes the socket file and shuts the listener cleanly. If
// the plugin process is killed, defer cleanup catches it; an
// orphaned socket on disk only happens on host crash and is
// removed on next start (NewPermissionsCallbackServer unlinks
// existing files at the path).
type PermissionsCallbackServer struct {
	decider    Decider
	socketPath string
	listener   net.Listener
	server     *http.Server
	closeOnce  sync.Once

	// principalProvider maps "the request belongs to plugin X"
	// → "spawn-time principal Y" so the callback knows whose
	// authority to check WITHOUT trusting the plugin's claim of
	// principal_id. nil ⇒ trust the request (used in tests).
	//
	// Production wiring: manager.Load sets this to a closure that
	// returns the Principal it bound to the plugin. The plugin
	// can lie about action/resource (they're its claims about
	// what it wants to do) but cannot impersonate a different
	// principal.
	//
	// Guarded by providerMu — Serve() starts before manager.Load
	// calls WithPrincipalProvider, so reads from handleAuthorize
	// goroutines race the setup write without this lock.
	providerMu        sync.RWMutex
	principalProvider func() *Principal
}

// NewPermissionsCallbackServer creates a server backed by the
// given Decider. Generates a unique socket path under the OS
// temp dir; caller passes that path to the plugin via env.
//
// Listener starts immediately on a goroutine. Plugin can dial as
// soon as the env is set.
//
// Returns the server (with SocketPath, Close) or an error if the
// listener couldn't be created (typically: temp dir not writable).
func NewPermissionsCallbackServer(decider Decider) (*PermissionsCallbackServer, error) {
	if decider == nil {
		return nil, errors.New("policy.NewPermissionsCallbackServer: decider must be non-nil")
	}

	dir := os.TempDir()
	// File name is unique enough — pid + nanosecond. No need for
	// crypto random (we already check file-permission ACL via UDS).
	name := fmt.Sprintf("codefly-perms-%d-%d.sock", os.Getpid(), time.Now().UnixNano())
	path := filepath.Join(dir, name)

	// Best-effort cleanup of any stale file at this exact path
	// (shouldn't exist; defensive).
	_ = os.Remove(path)

	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", path, err)
	}
	// Restrict the socket to the current user only — the callback
	// must not be accessible to other users on the host. UDS
	// inherits filesystem permissions; 0600 means owner-only.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = lis.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}

	srv := &PermissionsCallbackServer{
		decider:    decider,
		socketPath: path,
		listener:   lis,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", srv.handleAuthorize)
	srv.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		// Serve returns http.ErrServerClosed on Close — that's the
		// happy shutdown path. Anything else is a real error worth
		// logging, but we don't have a logger here; the upstream
		// caller observes via the next failed Authorize attempt.
		_ = srv.server.Serve(lis)
	}()

	return srv, nil
}

// SocketPath returns the path the listener is bound to. Caller
// passes this to the plugin via CODEFLY_PERMISSIONS_SOCKET env.
func (s *PermissionsCallbackServer) SocketPath() string {
	return s.socketPath
}

// WithPrincipalProvider sets the trusted-principal-resolver. If
// provided, the callback uses the resolver's Principal instead of
// trusting the request's principal_id field — the plugin can claim
// any principal_id, but the host overrides with the spawn-time
// binding. This is the standard production wiring.
func (s *PermissionsCallbackServer) WithPrincipalProvider(p func() *Principal) *PermissionsCallbackServer {
	s.providerMu.Lock()
	s.principalProvider = p
	s.providerMu.Unlock()
	return s
}

// Close shuts down the server and removes the socket file.
// Idempotent — safe to defer + call again from a parent cleanup.
func (s *PermissionsCallbackServer) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.server.Shutdown(ctx); err != nil {
			firstErr = err
		}
		if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// handleAuthorize is the only endpoint. POST JSON in, JSON out.
// Returns:
//   - 405 Method Not Allowed — if the verb isn't POST
//   - 400 Bad Request — if the JSON is malformed
//   - 200 with Allowed=true|false — successful decision
//
// **Why no 4xx for deny.** A deny is a successful decision, not
// an HTTP error. The model needs to distinguish "policy says no"
// (200 + Allowed=false) from "callback channel broken" (5xx).
// HTTP-level errors mean fail-closed; semantic denies are body
// content.
func (s *PermissionsCallbackServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close() //nolint:errcheck

	var req AuthorizeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
		return
	}

	// Use spawn-time principal if provider is set (production).
	// Falls back to request's claim (tests / standalone use).
	principalID := req.PrincipalID
	orgID := req.OrgID
	s.providerMu.RLock()
	provider := s.principalProvider
	s.providerMu.RUnlock()
	if provider != nil {
		if p := provider(); p != nil {
			principalID = p.ID
			orgID = p.OrgID
		}
	}

	if principalID == "" {
		// No principal at all — fail closed.
		s.writeResponse(w, AuthorizeResponse{
			Allowed: false,
			Reason:  "callback: no principal bound to this plugin",
		})
		return
	}

	// Construct the PDPRequest matching what the Guard would build.
	pdpReq := &PDPRequest{
		Tool: req.Action,
		Args: map[string]any{"resource": req.Resource},
		Identity: map[string]any{
			"principal_id":     principalID,
			"principal_org_id": orgID,
		},
	}
	d := s.decider.Evaluate(r.Context(), pdpReq)
	s.writeResponse(w, AuthorizeResponse{
		Allowed: d.Allow,
		Reason:  d.Reason,
	})
}

func (s *PermissionsCallbackServer) writeResponse(w http.ResponseWriter, resp AuthorizeResponse) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(resp); err != nil {
		// Headers may already be flushed; best we can do is log.
		// The plugin's client surfaces this as a connection error.
		_ = err
	}
}

// =====================================================================
// Plugin-side client (Authorizer impl)
// =====================================================================

// callbackAuthorizer is the plugin-side Authorizer that dials the
// host's PermissionsCallbackServer over UDS. Lazily connects on
// first use; connection is reused across calls.
//
// **Failure modes:**
//   - Socket env not set → fail-closed; every call returns
//     (false, "no permissions callback configured", nil). The
//     plugin author chose to call Authorized; the absence of a
//     callback is a configuration failure, not a transport
//     failure, so err is nil but allowed=false.
//   - Dial fails → fail-closed; (false, "...", err) — the err
//     surfaces the actual network problem.
//   - Request times out → fail-closed; (false, "timeout", err).
type callbackAuthorizer struct {
	socketPath string
	client     *http.Client
	timeout    time.Duration
}

// NewCallbackAuthorizerFromEnv constructs the standard plugin-
// side Authorizer reading CODEFLY_PERMISSIONS_SOCKET from env.
// If the env is unset, returns the no-op disabledAuthorizer that
// fails-closed on every call.
//
// Plugin authors don't call this directly — agents.Serve wires it
// into the request context. Use AuthorizerFromContext from
// handlers.
func NewCallbackAuthorizerFromEnv() Authorizer {
	path := os.Getenv(EnvPermissionsSocket)
	if path == "" {
		return disabledAuthorizer{
			reason: "no permissions callback configured (CODEFLY_PERMISSIONS_SOCKET unset)",
		}
	}
	return NewCallbackAuthorizer(path, 3*time.Second)
}

// NewCallbackAuthorizer constructs an Authorizer that dials the
// given UDS path with the supplied per-request timeout. Useful
// for tests that need to point at a specific socket.
func NewCallbackAuthorizer(socketPath string, timeout time.Duration) Authorizer {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		// UDS doesn't benefit from connection reuse the way TCP
		// does, but enabling keep-alives avoids a fresh dial per
		// call when a plugin loops over many resources.
		MaxIdleConns:    4,
		IdleConnTimeout: 90 * time.Second,
	}
	return &callbackAuthorizer{
		socketPath: socketPath,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		timeout: timeout,
	}
}

func (c *callbackAuthorizer) Authorized(ctx context.Context, action, resource string) (bool, string, error) {
	// principal_id is filled by the host's principalProvider; we
	// pass empty so the host MUST resolve from spawn-time binding.
	// In test setups without a provider, callers can use the
	// dedicated test constructor.
	body, err := json.Marshal(AuthorizeRequest{
		Action:   action,
		Resource: resource,
	})
	if err != nil {
		return false, "", fmt.Errorf("marshal authorize request: %w", err)
	}

	// "unix" is a stub — the http.Client's transport overrides
	// dial to point at the socket regardless of host part.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/authorize", bytes.NewReader(body))
	if err != nil {
		return false, "", fmt.Errorf("build authorize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		// Network / dial / timeout. Fail closed.
		return false, fmt.Sprintf("permissions callback unreachable: %v", err), err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		// 4xx/5xx — body might have a hint. Treat as fail-closed.
		hint, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return false, fmt.Sprintf("permissions callback returned %d: %s", resp.StatusCode, string(hint)),
			fmt.Errorf("status %d", resp.StatusCode)
	}

	var ar AuthorizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return false, fmt.Sprintf("decode response: %v", err), err
	}
	return ar.Allowed, ar.Reason, nil
}

// disabledAuthorizer is what plugins get when no callback is
// configured. Every call fails closed. Distinct (false, "...", nil)
// shape — no err — so plugin code distinguishes "callback not
// available" from "network problem".
type disabledAuthorizer struct {
	reason string
}

func (d disabledAuthorizer) Authorized(_ context.Context, _, _ string) (bool, string, error) {
	return false, d.reason, nil
}

// =====================================================================
// Context plumbing for plugin handlers
// =====================================================================

type authorizerCtxKey struct{}

// WithAuthorizer stamps an Authorizer on ctx. Called by agents.Serve
// (or any plugin wiring) once at startup; handlers retrieve via
// AuthorizerFromContext.
func WithAuthorizer(ctx context.Context, a Authorizer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, authorizerCtxKey{}, a)
}

// =====================================================================
// Callback-backed PDP — for plugin Guard's defense path
// =====================================================================
//
// When a plugin's policyguard.Guard takes the defense path (token
// missing or invalid), it consults its PDP. In production, this
// PDP should defer the authorization decision to the host (which
// has the saas-starter client + the spawn-time principal). The
// callback channel we already use for inline Authorized() works
// for this too; this adapter exposes it as a PDP.
//
// **Why this is symmetric with NewCallbackAuthorizerFromEnv.**
// Both speak to the same UDS server the host stood up; both
// honor the same spawn-time-principal binding (the host
// overrides any client-claimed principal). Authorizer is the
// simple yes/no API for handler-level checks; PDP is the richer
// shape Guard expects. Same wire, different facade.

// NewCallbackPDPFromEnv constructs a PDP backed by the host's
// permission callback (the UDS server set up via
// manager.WithPermissionsCallback). When the env var is unset,
// returns a disabledPDP that fails closed on every call.
//
// **Usage in a plugin:**
//
//	pdp := policy.NewCallbackPDPFromEnv()
//	guard := policyguard.New(myToolbox, pdp, audience)
//	agents.Serve(agents.PluginRegistration{Toolbox: guard})
//
// The Guard's defense path now consults the host's PDP — same
// principal, same role grants, same fail-closed semantics.
func NewCallbackPDPFromEnv() PDP {
	authorizer := NewCallbackAuthorizerFromEnv()
	return &callbackPDP{authorizer: authorizer}
}

// callbackPDP adapts an Authorizer as a PDP. The PDP request
// includes Toolbox + Tool (= action) + Args["resource"]; the
// adapter passes (action, resource) to Authorized.
type callbackPDP struct {
	authorizer Authorizer
}

func (p *callbackPDP) Evaluate(ctx context.Context, req *PDPRequest) PDPDecision {
	resource := ""
	if req.Args != nil {
		if v, ok := req.Args["resource"].(string); ok {
			resource = v
		}
	}
	allowed, reason, err := p.authorizer.Authorized(ctx, req.Tool, resource)
	if err != nil {
		// Backend error — fail closed.
		return PDPDecision{
			Allow:  false,
			Reason: fmt.Sprintf("callback PDP unreachable: %v", err),
		}
	}
	if !allowed {
		return PDPDecision{Allow: false, Reason: reason}
	}
	return PDPDecision{Allow: true}
}

// --- Compile-time assertion ---------------------------------------

var _ PDP = (*callbackPDP)(nil)

// AuthorizerFromContext returns the Authorizer stamped on ctx, or
// a disabledAuthorizer when none is present (so plugin code can
// always call .Authorized without a nil check; the disabled
// variant returns (false, "no authorizer in context", nil)).
//
// **Why never nil.** Defensive: handlers that test against nil
// can forget the check and crash on a deny path; returning a
// type that fails-closed by default keeps the contract simple
// — Authorized always returns a useable answer.
func AuthorizerFromContext(ctx context.Context) Authorizer {
	if ctx == nil {
		return disabledAuthorizer{reason: "no authorizer in context (nil ctx)"}
	}
	a, _ := ctx.Value(authorizerCtxKey{}).(Authorizer)
	if a == nil {
		return disabledAuthorizer{reason: "no authorizer in context"}
	}
	return a
}

// --- Compile-time assertions --------------------------------------

var _ Authorizer = (*callbackAuthorizer)(nil)
var _ Authorizer = disabledAuthorizer{}
