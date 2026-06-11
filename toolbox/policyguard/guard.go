package policyguard

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc/metadata"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/wool"
)

// Guard wraps a ToolboxServer with PDP enforcement. Identity /
// ListTools / list-style RPCs pass through unmolested — those are
// "what does this toolbox claim it can do" and don't mutate state.
// CallTool, ReadResource, and GetPrompt pass through the PDP
// because they're side-effecting (or at least information-disclosing).
type Guard struct {
	toolboxv0.UnimplementedToolboxServer

	inner   toolboxv0.ToolboxServer
	pdp     policy.PDP
	toolbox string // identity surfaced in PDPRequest.Toolbox

	// identity is the constant attribution for calls passing through
	// this guard. Hosts that want per-call attribution should run
	// per-caller guards (one per agent identity) rather than try to
	// thread context attribution through the gRPC layer.
	identity map[string]any

	// configMu guards the rotatable scoped-auth fields below.
	// CallTool snapshots them under RLock and passes the snapshot
	// into tryVerifyScopedAuthWith; the With* mutators take Lock.
	// Without this lock, a host that rotates the secret at runtime
	// (the documented use case) races with concurrent in-flight
	// requests.
	configMu sync.RWMutex

	// scopedAuthSecret is the HMAC key used to verify gateway-
	// minted ScopedAuthorization tokens. When set (non-zero
	// length), CallTool first checks for a token in the request
	// metadata: valid token → trust the gateway's pre-evaluation,
	// skip the PDP round-trip; missing/invalid → fall back to
	// the PDP path (defense in depth).
	//
	// Lifecycle: set by NewWithScopedAuth or from the
	// CODEFLY_SCOPED_AUTHZ_SECRET env at process startup. May
	// be rotated at runtime via WithScopedAuthSecret.
	scopedAuthSecret []byte

	// replayTracker enforces ScopedAuthorization.MaxUses across
	// repeated calls with the same token id. Per-Guard so
	// distinct toolboxes don't share state.
	replayTracker *policy.ReplayTracker

	// audienceID is the plugin's canonical identity used for
	// the token's audience-binding check. Empty audience skips
	// the check (test mode).
	audienceID string

	// caveatVerifiers is consulted for each caveat in the token
	// at verify time. Unknown caveats deny by default — see
	// scoped_auth.go.
	caveatVerifiers map[string]policy.CaveatVerifier

	// trl is the token-revocation list (hardening). A revoked-but-unexpired
	// scoped-auth token id is denied even though its HMAC verifies. Populated
	// from CODEFLY_REVOKED_TOKEN_IDS at startup; nil when unset.
	trl *policy.TokenRevocationList
}

// New wraps inner. If pdp is nil, an AllowAllPDP is substituted —
// the wrapper is then a no-op. This makes Guard always-safe-to-
// install: code paths that haven't migrated their config to a real
// PDP get the same behavior they had before.
//
// Without scoped-auth wiring, every CallTool consults the PDP. To
// enable the two-level scoped-auth fast path, use NewWithScopedAuth
// or set the secret/audience via the With* helpers.
func New(inner toolboxv0.ToolboxServer, pdp policy.PDP, toolboxName string) *Guard {
	if pdp == nil {
		pdp = policy.AllowAllPDP{}
	}
	g := &Guard{
		inner:   inner,
		pdp:     pdp,
		toolbox: toolboxName,
	}
	// Token revocation list (hardening). Operators publish revoked scoped-auth
	// token ids via CODEFLY_REVOKED_TOKEN_IDS (comma-separated); CallTool denies
	// any verified token whose id appears here.
	if raw := strings.TrimSpace(os.Getenv("CODEFLY_REVOKED_TOKEN_IDS")); raw != "" {
		var ids []string
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			g.trl = policy.NewTokenRevocationList()
			g.trl.Replace(ids)
		}
	}
	// If the spawn provided a scoped-auth secret via env, pick
	// it up automatically. Hosts that DON'T want this can clear
	// via WithScopedAuthSecret(nil) or use NewWithScopedAuth
	// explicitly with their own secret.
	if envSecret := scopedAuthSecretFromEnv(); len(envSecret) > 0 {
		g.scopedAuthSecret = envSecret
		g.replayTracker = policy.NewReplayTracker()
		// Bind the audience to THIS plugin's name. The gateway mints tokens
		// with AudienceID = the toolbox name (gateway.go), so without this the
		// auto-enabled fast path left audienceID empty and the audience check
		// was skipped — a token minted for plugin A would verify in plugin B
		// whenever a host shares one HMAC secret across plugins.
		g.audienceID = toolboxName
	}
	return g
}

// NewWithScopedAuth is the explicit constructor for the two-level
// model: pdp is the inner enforcer (defense layer 2), secret is
// the HMAC key for verifying gateway tokens (defense layer 1).
//
// audienceID should be the plugin's canonical identity
// ("publisher/name:version"); tokens minted for a different
// audience are rejected.
func NewWithScopedAuth(inner toolboxv0.ToolboxServer, pdp policy.PDP, toolboxName string, secret []byte, audienceID string) *Guard {
	g := New(inner, pdp, toolboxName)
	g.scopedAuthSecret = secret
	g.audienceID = audienceID
	if g.replayTracker == nil {
		g.replayTracker = policy.NewReplayTracker()
	}
	return g
}

// WithScopedAuthSecret replaces the verifier secret (e.g. for
// rotation). Returns the receiver for chaining.
func (g *Guard) WithScopedAuthSecret(secret []byte) *Guard {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.scopedAuthSecret = secret
	if len(secret) > 0 && g.replayTracker == nil {
		g.replayTracker = policy.NewReplayTracker()
	}
	return g
}

// WithAudience binds the verifier to a specific audience identity.
// Tokens whose audience doesn't match are rejected.
func (g *Guard) WithAudience(audienceID string) *Guard {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.audienceID = audienceID
	return g
}

// WithCaveatVerifiers registers caveat verifiers consulted at
// token-verify time. Unknown caveat keys reject by default;
// register only the keys the operator's tool policies produce.
func (g *Guard) WithCaveatVerifiers(verifiers map[string]policy.CaveatVerifier) *Guard {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.caveatVerifiers = verifiers
	return g
}

// scopedAuthSecretFromEnv reads the per-spawn HMAC secret from
// the env var manager.Load sets. Empty when no secret was
// configured.
func scopedAuthSecretFromEnv() []byte {
	v := os.Getenv("CODEFLY_SCOPED_AUTHZ_SECRET")
	if v == "" {
		return nil
	}
	// The env carries the secret as base64url. Decoding here
	// gives us the raw bytes for HMAC.
	return decodeSecret(v)
}

// decodeSecret accepts the secret as either base64url-encoded
// (production: 32 random bytes encode to 43 chars) or as raw
// bytes (tests can pass literal strings). Falls back gracefully.
func decodeSecret(s string) []byte {
	if decoded, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(decoded) >= 32 {
		return decoded
	}
	// Last-resort: treat the string itself as the secret. Lets
	// tests pass arbitrary literal secrets without base64
	// ceremony.
	return []byte(s)
}

// WithIdentity attaches a constant attribution map to every PDP
// request this Guard makes. Useful when the host knows it's wrapping
// a toolbox for a specific agent or session.
func (g *Guard) WithIdentity(identity map[string]any) *Guard {
	g.identity = identity
	return g
}

// --- Pass-through (no PDP) ---------------------------------------

func (g *Guard) Identity(ctx context.Context, req *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return g.inner.Identity(ctx, req)
}

func (g *Guard) ListTools(ctx context.Context, req *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return g.inner.ListTools(ctx, req)
}

// ListToolSummaries is the lightweight catalog half of the two-phase
// API. Same trust class as ListTools — pass through unmolested.
func (g *Guard) ListToolSummaries(ctx context.Context, req *toolboxv0.ListToolSummariesRequest) (*toolboxv0.ListToolSummariesResponse, error) {
	return g.inner.ListToolSummaries(ctx, req)
}

// DescribeTool is the per-tool spec half of the two-phase API. Same
// trust class as ListTools — pass through unmolested. The PDP gates
// the actual side-effecting RPC (CallTool) below; describing a tool
// is not itself a privileged operation.
func (g *Guard) DescribeTool(ctx context.Context, req *toolboxv0.DescribeToolRequest) (*toolboxv0.DescribeToolResponse, error) {
	return g.inner.DescribeTool(ctx, req)
}

func (g *Guard) ListResources(ctx context.Context, req *toolboxv0.ListResourcesRequest) (*toolboxv0.ListResourcesResponse, error) {
	return g.inner.ListResources(ctx, req)
}

func (g *Guard) ListPrompts(ctx context.Context, req *toolboxv0.ListPromptsRequest) (*toolboxv0.ListPromptsResponse, error) {
	return g.inner.ListPrompts(ctx, req)
}

// --- Policy-gated --------------------------------------------------

// CallTool is the load-bearing authorization gate. Two paths:
//
//  1. **Fast path** (when scoped-auth is wired AND a token rides
//     on the request): verify the token's signature, expiry,
//     audience, action, resource, and caveats. Valid → trust the
//     gateway's pre-evaluation, stamp the verified
//     ScopedAuthorization on ctx for handler visibility, dispatch
//     to inner. The PDP is NOT consulted on this path.
//
//  2. **Defense path** (when scoped-auth is unwired OR token is
//     missing/invalid): full PDP evaluation. Same behavior as the
//     one-level model. Token-invalid logs a warning so operators
//     see "scoped-auth verify failed" events without breaking the
//     call (the PDP catches abuse).
//
// Refused calls return a CallToolResponse with the deny reason in
// Error — the SAME envelope a tool that refused itself would
// produce. The model sees an actionable refusal, not a transport
// error or a panic-style disconnect.
func (g *Guard) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	// Snapshot rotatable config once per call so a concurrent
	// With* mutator can't tear our view (secret swapped while
	// the tracker is still the old one, etc.).
	g.configMu.RLock()
	secret := g.scopedAuthSecret
	tracker := g.replayTracker
	audience := g.audienceID
	verifiers := g.caveatVerifiers
	g.configMu.RUnlock()

	// Delegation-depth cap (hardening): reject a Principal whose delegation
	// chain exceeds CODEFLY_MAX_DELEGATION_DEPTH, regardless of which path
	// authorizes below. Pure deny — no-op when no principal / chain is present.
	if p := policy.PrincipalFrom(ctx); p != nil {
		if err := policy.CheckDelegationDepth(p); err != nil {
			return &toolboxv0.CallToolResponse{Error: "denied: " + err.Error()}, nil
		}
	}

	// Resource of THIS call, used both for the resource-binding enforcement
	// below and (when present) break-glass auditing.
	callResource := ""
	if args := req.GetArguments(); args != nil {
		if v, ok := args.AsMap()["resource"].(string); ok {
			callResource = v
		}
	}

	// Try the fast path first when scoped-auth is configured.
	if len(secret) > 0 {
		if sa, ok := g.tryVerifyScopedAuthWith(ctx, req, secret, audience, verifiers); ok {
			// Resource binding: a resource-scoped token must only authorize a
			// call that carries the SAME resource. The Verify primitive skips
			// this when the call presents no resource arg, so enforce it here —
			// otherwise a token minted for "repo:foo" would authorize a call
			// that surfaces no resource, voiding its least-authority binding.
			if sa.Resource != "" && sa.Resource != callResource {
				return &toolboxv0.CallToolResponse{Error: "scoped-authz: token resource binding not satisfied by call"}, nil
			}
			// Token revocation (hardening): a revoked-but-unexpired token id is
			// denied even though its HMAC verified.
			if g.trl != nil && g.trl.IsRevoked(sa.ID) {
				return &toolboxv0.CallToolResponse{Error: "scoped-authz: token revoked"}, nil
			}
			// Replay tracking — enforces MaxUses across calls.
			if err := tracker.Consume(sa); err != nil {
				return &toolboxv0.CallToolResponse{
					Error: "scoped-authz: " + err.Error(),
				}, nil
			}
			ctx = policy.WithScopedAuth(ctx, sa)
			return g.inner.CallTool(ctx, req)
		}
		// Fall through to PDP path. The verify failure (if any)
		// has already been logged by tryVerifyScopedAuthWith.
	}

	// Defense path: PDP-via-callback (single-level model).
	pdpReq := &policy.PDPRequest{
		Toolbox:  g.toolbox,
		Tool:     req.GetName(),
		Identity: g.identity,
	}
	if req.GetArguments() != nil {
		pdpReq.Args = req.GetArguments().AsMap()
	}
	d := g.pdp.Evaluate(ctx, pdpReq)
	if !d.Allow {
		// Break-glass (hardening): emergency operator override of a PDP deny.
		// Active only when CODEFLY_BREAK_GLASS_JUSTIFICATION is set (a deliberate
		// operator action); every use is audit-logged at WARN.
		if policy.IsBreakGlassActive() {
			policy.LogBreakGlassUsage(ctx, req.GetName(), callResource)
			return g.inner.CallTool(ctx, req)
		}
		return &toolboxv0.CallToolResponse{Error: d.Reason}, nil
	}
	return g.inner.CallTool(ctx, req)
}

// tryVerifyScopedAuthWith attempts the fast-path token verification
// using the supplied snapshot of rotatable config. Callers (CallTool)
// snapshot once under configMu.RLock and pass the captured values
// here so a concurrent With* mutator can't tear the view between
// CallTool's "secret is set" check and Verify's actual HMAC.
//
// Returns the verified token + true on success; (nil, false) when:
//   - no token in metadata (silent: defense path takes over)
//   - token present but invalid (logs WARN; defense path takes over)
//
// Token absence is NOT a deny by itself — the defense path (PDP)
// still runs. This is the load-bearing two-level property: each
// layer can fail independently without opening a hole.
func (g *Guard) tryVerifyScopedAuthWith(
	ctx context.Context,
	req *toolboxv0.CallToolRequest,
	secret []byte,
	audience string,
	verifiers map[string]policy.CaveatVerifier,
) (*policy.ScopedAuthorization, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		// No request metadata at all — silently fall through to
		// the PDP. The verify-failure WARN below only fires for
		// present-but-invalid tokens; record the absence at Debug
		// so operators can audit "scoped-auth configured but
		// nobody's using it" without noise.
		wool.Get(ctx).In("policyguard.tryVerifyScopedAuthWith").
			Debug("no metadata on request; falling back to PDP",
				wool.Field("tool", req.GetName()))
		return nil, false
	}
	values := md.Get(policy.ScopedAuthMetadataKey)
	if len(values) == 0 || values[0] == "" {
		wool.Get(ctx).In("policyguard.tryVerifyScopedAuthWith").
			Debug("scoped-auth token absent; falling back to PDP",
				wool.Field("tool", req.GetName()))
		return nil, false
	}

	resource := ""
	if args := req.GetArguments(); args != nil {
		if v, ok := args.AsMap()["resource"].(string); ok {
			resource = v
		}
	}

	expect := policy.VerifyExpectations{
		Action:          req.GetName(),
		Resource:        resource,
		Audience:        audience,
		CaveatVerifiers: verifiers,
	}
	// Bind to the Principal stamped on ctx (set by the
	// principalUnaryInterceptor). When no Principal is on ctx,
	// expect.PrincipalID stays empty and the verifier skips that
	// check; the audience+action+resource binding still applies.
	if p := policy.PrincipalFrom(ctx); p != nil {
		expect.PrincipalID = p.ID
	}

	sa, err := policy.Verify(values[0], expect, secret)
	if err != nil {
		// Token present but failed verify — log a warning and
		// fall through to the defense path. We don't OUTRIGHT
		// DENY here: a misconfigured gateway shouldn't break the
		// system, and the PDP is the second layer that catches
		// any actual abuse.
		wool.Get(ctx).In("policyguard.tryVerifyScopedAuthWith").
			Warn("scoped-authz invalid; falling back to PDP",
				wool.Field("error", err.Error()),
				wool.Field("tool", req.GetName()))
		return nil, false
	}
	return sa, true
}

// ReadResource is policy-gated for the same reason CallTool is —
// the resource URI may name a sensitive file the operator wants to
// keep out of agent context. PDP key for ReadResource: Tool is the
// URI string. Rules can match on the URI prefix via the
// suffix-match shorthand (e.g. Tool="config.yaml" matches any URI
// ending in /config.yaml).
func (g *Guard) ReadResource(ctx context.Context, req *toolboxv0.ReadResourceRequest) (*toolboxv0.ReadResourceResponse, error) {
	pdpReq := &policy.PDPRequest{
		Toolbox:  g.toolbox,
		Tool:     req.GetUri(),
		Identity: g.identity,
	}
	d := g.pdp.Evaluate(ctx, pdpReq)
	if !d.Allow {
		return &toolboxv0.ReadResourceResponse{
			Content: []*toolboxv0.Content{{Body: &toolboxv0.Content_Text{Text: "policy: " + d.Reason}}},
		}, nil
	}
	return g.inner.ReadResource(ctx, req)
}

// GetPrompt is also gated — prompts may template in private context
// (paths, secrets) that the operator wants to deny per caller.
func (g *Guard) GetPrompt(ctx context.Context, req *toolboxv0.GetPromptRequest) (*toolboxv0.GetPromptResponse, error) {
	pdpReq := &policy.PDPRequest{
		Toolbox:  g.toolbox,
		Tool:     req.GetName(),
		Identity: g.identity,
	}
	if req.GetArguments() != nil {
		pdpReq.Args = req.GetArguments().AsMap()
	}
	d := g.pdp.Evaluate(ctx, pdpReq)
	if !d.Allow {
		return &toolboxv0.GetPromptResponse{Description: "denied: " + d.Reason}, nil
	}
	return g.inner.GetPrompt(ctx, req)
}
