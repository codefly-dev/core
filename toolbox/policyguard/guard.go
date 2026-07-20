package policyguard

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc/metadata"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	coretoolbox "github.com/codefly-dev/core/toolbox"
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
	// skip the PDP round-trip; missing → fall back to the PDP;
	// present but invalid → deny without credential downgrade.
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

	// wellKnownExpectations are trusted spawn/session bindings. Per-call
	// organization and resource expectations are added from the stamped
	// principal and exact request before verification.
	wellKnownExpectations policy.WellKnownCaveatExpectations

	// trl is the token-revocation list (hardening). A revoked-but-unexpired
	// scoped-auth token id is denied even though its HMAC verifies. Populated
	// from CODEFLY_REVOKED_TOKEN_IDS at startup; nil when unset.
	trl *policy.TokenRevocationList

	// catalog is the plugin's phase-one identity/summary snapshot. The exact
	// selected descriptor is fetched and hashed at call time, matching the
	// host's two-phase approval and detecting contract changes after discovery.
	catalog             *coretoolbox.CatalogSnapshot
	catalogErr          error
	requireBoundDigests bool
}

// New wraps inner. A nil PDP is a configuration error and fails closed with a
// DenyAllPDP. Explicit policy-off mode is handled by agents.Serve by registering
// the raw toolbox without a Guard; silently turning a present guard into
// allow-all would make a missing dependency indistinguishable from a deliberate
// security choice.
//
// Without scoped-auth wiring, every CallTool consults the PDP. To
// enable the two-level scoped-auth fast path, use NewWithScopedAuth
// or set the secret/audience via the With* helpers.
func New(inner toolboxv0.ToolboxServer, pdp policy.PDP, toolboxName string) *Guard {
	if pdp == nil {
		pdp = policy.DenyAllPDP{}
	}
	g := &Guard{
		inner:   inner,
		pdp:     pdp,
		toolbox: toolboxName,
	}
	if snapshot, err := coretoolbox.SnapshotServer(context.Background(), inner); err != nil {
		g.catalogErr = err
	} else {
		g.catalog = snapshot
	}
	g.requireBoundDigests = os.Getenv(coretoolbox.RequireBoundAuthorizationEnvironment) == "1"
	g.wellKnownExpectations = policy.WellKnownCaveatExpectations{
		TenantID:    os.Getenv(policy.EnvToolboxTenantID),
		Environment: os.Getenv(policy.EnvToolboxEnvironment),
		ReleaseID:   os.Getenv(policy.EnvToolboxReleaseID),
		ApprovalID:  os.Getenv(policy.EnvToolboxApprovalID),
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

// WithWellKnownCaveatExpectations installs trusted scope bindings for embedded
// hosts and tests. Process toolboxes normally receive these from session-owned
// environment variables at spawn.
func (g *Guard) WithWellKnownCaveatExpectations(expect policy.WellKnownCaveatExpectations) *Guard {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.wellKnownExpectations = expect
	return g
}

// WithRequiredAuthorizationBindings requires catalog and request digests on
// scoped tokens. Production startup enables this via environment; tests and
// embedded hosts may opt in directly.
func (g *Guard) WithRequiredAuthorizationBindings() *Guard {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.requireBoundDigests = true
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
//  2. **Defense path** (when scoped-auth is unwired OR the token is absent):
//     full PDP evaluation. A present-but-invalid token is denied immediately;
//     it must not downgrade into the credential-less path.
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
	wellKnownExpectations := g.wellKnownExpectations
	requireBoundDigests := g.requireBoundDigests
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
		sa, tokenPresent, verifyErr := g.tryVerifyScopedAuthWith(ctx, req, secret, audience, verifiers, wellKnownExpectations, requireBoundDigests)
		if verifyErr != nil {
			return &toolboxv0.CallToolResponse{Error: "scoped-authz: invalid token"}, nil
		}
		if tokenPresent {
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
		if requireBoundDigests {
			// Bound mode is the production session contract. Falling back to
			// the raw PDP here would bypass the exact catalog/request binding
			// and single-use replay semantics that the host just established.
			return &toolboxv0.CallToolResponse{Error: "scoped-authz: token required"}, nil
		}
		// A genuinely absent token falls through to the PDP path.
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
// Returns the verified token, whether a credential was present, and any
// verification failure. Missing credentials are distinct from malformed or
// unverifiable credentials: only absence may fall back to the PDP path.
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
	wellKnownExpectations policy.WellKnownCaveatExpectations,
	requireBoundDigests bool,
) (*policy.ScopedAuthorization, bool, error) {
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
		return nil, false, nil
	}
	values := md.Get(policy.ScopedAuthMetadataKey)
	if len(values) == 0 {
		wool.Get(ctx).In("policyguard.tryVerifyScopedAuthWith").
			Debug("scoped-auth token absent; falling back to PDP",
				wool.Field("tool", req.GetName()))
		return nil, false, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		err := policy.ErrScopedAuthInvalid
		wool.Get(ctx).In("policyguard.tryVerifyScopedAuthWith").
			Warn("scoped-authz invalid; denying request",
				wool.Field("error", "expected exactly one non-empty token"),
				wool.Field("tool", req.GetName()))
		return nil, true, err
	}

	resource := ""
	if args := req.GetArguments(); args != nil {
		if v, ok := args.AsMap()["resource"].(string); ok {
			resource = v
		}
	}
	expect := policy.VerifyExpectations{
		Action:   req.GetName(),
		Resource: resource,
		Audience: audience,
	}
	if requireBoundDigests {
		if g.catalogErr != nil {
			return nil, true, g.catalogErr
		}
		if g.catalog == nil {
			return nil, true, policy.ErrScopedAuthInvalid
		}
		description, err := g.inner.DescribeTool(ctx, &toolboxv0.DescribeToolRequest{Name: req.GetName()})
		if err != nil {
			return nil, true, err
		}
		approved, err := g.catalog.ApproveTool(req.GetName(), description)
		if err != nil {
			return nil, true, err
		}
		requestDigest, err := coretoolbox.DigestCallToolRequest(req)
		if err != nil {
			return nil, true, err
		}
		expect.CatalogDigest = approved.Digest
		expect.RequestDigest = requestDigest
	}
	// Bind to the Principal stamped on ctx (set by the
	// principalUnaryInterceptor). When no Principal is on ctx,
	// expect.PrincipalID stays empty and the verifier skips that
	// check; the audience+action+resource binding still applies.
	if p := policy.PrincipalFrom(ctx); p != nil {
		expect.PrincipalID = p.ID
		expect.PrincipalKind = p.Kind
		expect.OrganizationID = p.OrgID
		wellKnownExpectations.OrganizationID = p.OrgID
	}
	wellKnownExpectations.ResourceBinding = resource
	if args := req.GetArguments(); args != nil {
		values := args.AsMap()
		if rawQueryID, present := values["query_id"]; present {
			queryID, ok := rawQueryID.(string)
			if !ok || strings.TrimSpace(queryID) == "" {
				return nil, true, fmt.Errorf("query_id must be a non-empty string")
			}
			wellKnownExpectations.QueryID = queryID
		}
		if rawBudget, present := values["result_budget"]; present {
			budget, err := policy.ParseResultBudget(rawBudget)
			if err != nil {
				return nil, true, err
			}
			wellKnownExpectations.ResultBudget = &budget
		}
	}
	verification, err := policy.NewWellKnownCaveatVerification(wellKnownExpectations)
	if err != nil {
		return nil, true, err
	}
	// Organization and resource are signed first-class scoped-auth claims.
	// Register their typed caveat verifiers when present, but do not require
	// redundant caveats from low-level hosts that bind the exact claims directly.
	verification.Required = withoutString(verification.Required, string(policy.CaveatOrganizationID))
	verification.Required = withoutString(verification.Required, string(policy.CaveatResourceBinding))
	verification.Verifiers, err = mergeCaveatVerifiers(verification.Verifiers, verifiers)
	if err != nil {
		return nil, true, err
	}
	expect.CaveatVerifiers = verification.Verifiers
	expect.RequiredCaveats = verification.Required

	sa, err := policy.Verify(values[0], expect, secret)
	if err != nil {
		wool.Get(ctx).In("policyguard.tryVerifyScopedAuthWith").
			Warn("scoped-authz invalid; denying request",
				wool.Field("error", err.Error()),
				wool.Field("tool", req.GetName()))
		return nil, true, err
	}
	return sa, true, nil
}

func withoutString(values []string, omitted string) []string {
	out := values[:0]
	for _, value := range values {
		if value != omitted {
			out = append(out, value)
		}
	}
	return out
}

func mergeCaveatVerifiers(
	wellKnown map[string]policy.CaveatVerifier,
	provider map[string]policy.CaveatVerifier,
) (map[string]policy.CaveatVerifier, error) {
	merged := make(map[string]policy.CaveatVerifier, len(wellKnown)+len(provider))
	for key, verifier := range wellKnown {
		merged[key] = verifier
	}
	for key, verifier := range provider {
		if _, reserved := merged[key]; reserved {
			return nil, fmt.Errorf("provider caveat verifier attempts to replace well-known binding %q", key)
		}
		merged[key] = verifier
	}
	return merged, nil
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
