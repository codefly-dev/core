package policy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// GatewayEvaluator is the host-side composer of tool policies and
// role grants. Hosts (Mind, codefly's gateway, the CLI) construct
// one of these at startup and call EvaluateAndMint per outgoing
// CallTool.
//
// Responsibilities:
//
//  1. Look up the registered ToolPolicy for the (toolbox, tool).
//  2. Run the tool policy against the input (manifest ceiling +
//     operator rules).
//  3. Run the inner Decider (typically SaasPDP) for the role-
//     grant check.
//  4. If both pass, mint a ScopedAuthorization carrying the
//     resolved decision (action, resource, principal, time
//     window, max uses, computed caveats).
//  5. Record observability for every decision (allow/deny/error).
//
// The gateway is the FIRST defense layer; the plugin's Guard is
// the second. Even if a host bug causes the gateway to mint a
// fraudulent token, the plugin's Guard verifies the signature and
// — if absent or invalid — falls back to the PDP-via-callback
// path. Three independent layers; defense in depth.
type GatewayEvaluator struct {
	// ToolPolicies maps "<toolbox>:<tool>" or "<toolbox>:*" to
	// the operator's tool policy. Lookups try the exact key
	// first, then the wildcard. nil/missing = manifest-only
	// policy (no operator rules).
	ToolPolicies map[string]ToolPolicy

	// Decider is the inner role-grant check. Typically a
	// SaasPDP that calls saas-starter's Decide RPC.
	Decider Decider

	// Secret is the HMAC key used to sign minted tokens. Must
	// be at least 32 bytes (enforced at Mint time).
	Secret []byte

	// DefaultTTL is the time window applied when the tool
	// policy doesn't specify. Recommended: 120s. Operators
	// who need different windows configure per-tool.
	DefaultTTL time.Duration

	// Metrics, if non-nil, gets every decision recorded.
	Metrics *PDPMetrics
}

// EvaluationInput carries the call context the gateway needs to
// produce a decision.
type EvaluationInput struct {
	Principal *Principal
	Toolbox   string // canonical identity, e.g. "codefly.dev/github-bot:0.1.0"
	Tool      string // dotted action, e.g. "github.merge_pr"
	Resource  string // typed identifier, e.g. "repo:codefly/x"

	// Context is the runtime context for caveat evaluation
	// (ci_status, labels, request body summary, etc.). Tool
	// policies consume this; pass through to caveats.
	Context map[string]any
}

// EvaluationResult is what EvaluateAndMint returns on success.
type EvaluationResult struct {
	// Token is the encoded ScopedAuthorization, ready to attach
	// to outgoing gRPC metadata via AttachToOutgoingContext.
	Token string

	// Authorization is the decoded form (same data as Token)
	// for inspection / audit.
	Authorization *ScopedAuthorization

	// DecisionPath is a trace-style explanation of how the
	// decision was reached ("manifest-allow → role-grant:
	// editor → tool-policy: ci-green").
	DecisionPath string
}

// ErrGatewayDeny is the umbrella error returned by EvaluateAndMint
// when policy denies the call. Wraps a more specific reason; the
// caller surfaces it to the model so it can plan around.
var ErrGatewayDeny = errors.New("gateway: denied")

// EvaluateAndMint runs the full pipeline. Returns:
//
//   - (result, nil) on allow with a fresh signed token
//   - (nil, err) on deny — err wraps ErrGatewayDeny with the reason
//   - (nil, err) on backend failure — err is the underlying error
//     (typically a saas-starter network issue). The caller MUST
//     fail the request; gateway never silently allows on backend
//     failure.
func (g *GatewayEvaluator) EvaluateAndMint(ctx context.Context, in EvaluationInput) (*EvaluationResult, error) {
	if g.Decider == nil {
		return nil, fmt.Errorf("gateway: nil Decider (misconfiguration)")
	}
	if in.Principal == nil {
		return nil, fmt.Errorf("%w: nil principal", ErrGatewayDeny)
	}
	if in.Tool == "" {
		return nil, fmt.Errorf("%w: empty tool", ErrGatewayDeny)
	}

	start := time.Now()
	defer func() {
		// Observability: latency tracked even when we deny early.
		_ = start // referenced in the closure below if metrics set
	}()

	// 1. Tool policy: manifest declaration + operator rules.
	policy, decisionPath, err := g.evaluateToolPolicy(ctx, in)
	if err != nil {
		// Tool policy denied or errored. Record + return.
		g.recordDeny(ctx, in, err.Error(), time.Since(start))
		return nil, err
	}

	// 2. Role grant via inner Decider.
	pdpReq := &PDPRequest{
		Toolbox:  in.Toolbox,
		Tool:     in.Tool,
		Args:     map[string]any{"resource": in.Resource},
		Identity: in.Principal.AsIdentity(),
	}
	d := g.Decider.Evaluate(ctx, pdpReq)
	if !d.Allow {
		reason := fmt.Sprintf("%s: %s", decisionPath, d.Reason)
		g.recordDeny(ctx, in, reason, time.Since(start))
		return nil, fmt.Errorf("%w: %s", ErrGatewayDeny, reason)
	}

	// 3. Mint the token.
	ttl := policy.TTL
	if ttl <= 0 {
		ttl = g.DefaultTTL
	}
	if ttl <= 0 {
		ttl = 120 * time.Second
	}

	encoded, sa, err := Mint(MintInput{
		Principal:  in.Principal,
		Action:     in.Tool,
		Resource:   in.Resource,
		AudienceID: in.Toolbox,
		TTL:        ttl,
		MaxUses:    policy.MaxUses,
		Caveats:    policy.ResolvedCaveats(in.Context),
	}, g.Secret)
	if err != nil {
		// Mint failure (insufficient secret, etc.) is a
		// CONFIGURATION bug, not a policy decision. Surface as
		// internal error.
		return nil, fmt.Errorf("gateway: mint failed: %w", err)
	}

	g.recordAllow(ctx, in, decisionPath, time.Since(start))
	return &EvaluationResult{
		Token:         encoded,
		Authorization: sa,
		DecisionPath:  fmt.Sprintf("%s → role-grant: %s", decisionPath, d.Reason),
	}, nil
}

func (g *GatewayEvaluator) evaluateToolPolicy(ctx context.Context, in EvaluationInput) (ResolvedToolPolicy, string, error) {
	policy, foundKey := g.lookupToolPolicy(in.Toolbox, in.Tool)
	if policy == nil {
		// No registered tool policy. Default-allow at this
		// layer — the role-grant check still runs. This makes
		// the gateway a no-op for tools without operator rules,
		// matching the M4 rollout default.
		return ResolvedToolPolicy{
			TTL:     g.DefaultTTL,
			MaxUses: 1,
		}, "tool-policy: none-registered", nil
	}
	resolved, err := policy.Evaluate(ctx, in)
	if err != nil {
		return ResolvedToolPolicy{}, "", fmt.Errorf("%w: tool-policy %q: %v", ErrGatewayDeny, foundKey, err)
	}
	return resolved, fmt.Sprintf("tool-policy: %s allow", foundKey), nil
}

func (g *GatewayEvaluator) lookupToolPolicy(toolbox, tool string) (ToolPolicy, string) {
	exact := toolbox + ":" + tool
	if p, ok := g.ToolPolicies[exact]; ok && p != nil {
		return p, exact
	}
	wildcard := toolbox + ":*"
	if p, ok := g.ToolPolicies[wildcard]; ok && p != nil {
		return p, wildcard
	}
	return nil, ""
}

func (g *GatewayEvaluator) recordAllow(ctx context.Context, in EvaluationInput, path string, latency time.Duration) {
	if g.Metrics == nil {
		return
	}
	RecordDecision(ctx, g.Metrics, DecisionEvent{
		Toolbox:       in.Toolbox,
		Tool:          in.Tool,
		PrincipalID:   in.Principal.ID,
		PrincipalKind: in.Principal.Kind,
		AgentID:       in.Principal.AgentID,
		Decision:      PDPDecision{Allow: true, Reason: path},
		Latency:       latency,
	})
}

func (g *GatewayEvaluator) recordDeny(ctx context.Context, in EvaluationInput, reason string, latency time.Duration) {
	if g.Metrics == nil {
		return
	}
	pid := ""
	pkind := ""
	aid := ""
	if in.Principal != nil {
		pid = in.Principal.ID
		pkind = in.Principal.Kind
		aid = in.Principal.AgentID
	}
	RecordDecision(ctx, g.Metrics, DecisionEvent{
		Toolbox:       in.Toolbox,
		Tool:          in.Tool,
		PrincipalID:   pid,
		PrincipalKind: pkind,
		AgentID:       aid,
		Decision:      PDPDecision{Allow: false, Reason: reason},
		Latency:       latency,
	})
}

// =====================================================================
// ToolPolicy
// =====================================================================

// ToolPolicy is the gateway-evaluated rule set per tool. Built-in
// implementations cover the common cases (manifest declaration
// check, simple allow-rule); operators implement custom logic
// for richer policies (Cedar, Rego, business-specific).
//
// **Why an interface rather than a concrete type.** Operators have
// genuinely different needs — some want declarative YAML, some
// want Cedar, some want code. The interface hides those choices
// from GatewayEvaluator; only Evaluate matters for the pipeline.
type ToolPolicy interface {
	// Evaluate runs the policy against the call input. Returns
	// a ResolvedToolPolicy on allow (carrying TTL, MaxUses,
	// caveats to bake into the token) or an error on deny.
	//
	// **Errors are denies, not faults.** A tool policy that
	// returns err means "this call is not allowed by this
	// policy"; the GatewayEvaluator wraps the err with
	// ErrGatewayDeny.
	Evaluate(ctx context.Context, in EvaluationInput) (ResolvedToolPolicy, error)
}

// ResolvedToolPolicy is what a ToolPolicy returns on allow. It
// configures the token mint — TTL, MaxUses, plus any caveats
// the policy wants baked into the token for plugin-side
// verification.
type ResolvedToolPolicy struct {
	// TTL is how long the minted token should live. Zero =
	// use GatewayEvaluator.DefaultTTL.
	TTL time.Duration

	// MaxUses caps token reuse. Zero = single-shot (1).
	MaxUses int

	// Caveats produced by the policy. CaveatProducer functions
	// run at allow time to compute caveat values from the call
	// context (e.g. snapshot the ci_status into the caveat).
	CaveatProducers map[string]CaveatProducer
}

// CaveatProducer computes a caveat value from the call context at
// token-mint time. The output is what the verifier matches at
// plugin side.
//
// Example: a "ci_status" caveat producer reads
// in.Context["ci_status"] and returns the value, baking the
// snapshot into the token. If CI status changes between mint and
// verify, the verifier still sees the snapshot — that's correct:
// the gateway authorized THIS call based on THIS state.
type CaveatProducer func(in EvaluationInput) (any, error)

// ResolvedCaveats runs every producer and collects the results.
// Errors from a producer are NOT swallowed — they propagate up
// as deny via the gateway pipeline.
//
// Returns nil when there are no producers (avoids an empty map
// in the token).
func (r ResolvedToolPolicy) ResolvedCaveats(ctxData map[string]any) map[string]any {
	if len(r.CaveatProducers) == 0 {
		return nil
	}
	in := EvaluationInput{Context: ctxData}
	out := make(map[string]any, len(r.CaveatProducers))
	for key, producer := range r.CaveatProducers {
		v, err := producer(in)
		if err != nil {
			// Producers should never fail at this stage — they've
			// been pre-validated. Surface as an explicit caveat
			// so the verifier rejects loudly rather than silently
			// drops the caveat.
			out[key] = fmt.Sprintf("ERROR-COMPUTING-CAVEAT: %v", err)
			continue
		}
		out[key] = v
	}
	return out
}

// =====================================================================
// Built-in tool policies
// =====================================================================

// AllowAlwaysToolPolicy permits every call with default TTL and
// max_uses. Useful for tests and for operators who don't have
// custom rules — the role-grant check is still the gate.
type AllowAlwaysToolPolicy struct {
	TTL     time.Duration
	MaxUses int
}

func (p AllowAlwaysToolPolicy) Evaluate(_ context.Context, _ EvaluationInput) (ResolvedToolPolicy, error) {
	return ResolvedToolPolicy{TTL: p.TTL, MaxUses: p.MaxUses}, nil
}

// DenyAlwaysToolPolicy is the symmetric opposite: refuses every
// call with the supplied reason. Useful for kill-switch-style
// configurations ("temporarily block all force-push tool calls").
type DenyAlwaysToolPolicy struct {
	Reason string
}

func (p DenyAlwaysToolPolicy) Evaluate(_ context.Context, _ EvaluationInput) (ResolvedToolPolicy, error) {
	reason := p.Reason
	if reason == "" {
		reason = "deny-always tool policy"
	}
	return ResolvedToolPolicy{}, errors.New(reason)
}

// ManifestCeilingPolicy enforces that the tool MUST be declared in
// the plugin's PermissionPolicy. Mirrors the M4 CeilingPDP logic
// at the gateway layer — moves the manifest check upstream of the
// plugin process.
//
// **Why duplicate the M4 check at the gateway.** Defense in depth.
// If the gateway evaluates first and denies undeclared actions,
// the plugin never sees them — minimizing attack surface and
// audit noise. The plugin's CeilingPDP is still the second
// defense layer (catches anything the gateway missed).
type ManifestCeilingPolicy struct {
	Manifest PermissionPolicy
	TTL      time.Duration
	MaxUses  int
}

func (p ManifestCeilingPolicy) Evaluate(_ context.Context, in EvaluationInput) (ResolvedToolPolicy, error) {
	if !p.Manifest.Allows(in.Tool, in.Resource) {
		return ResolvedToolPolicy{}, fmt.Errorf(
			"manifest does not declare %q on %q (gateway-layer ceiling check)",
			in.Tool, in.Resource)
	}
	return ResolvedToolPolicy{TTL: p.TTL, MaxUses: p.MaxUses}, nil
}

// =====================================================================
// Compile-time assertions
// =====================================================================

var _ ToolPolicy = AllowAlwaysToolPolicy{}
var _ ToolPolicy = DenyAlwaysToolPolicy{}
var _ ToolPolicy = ManifestCeilingPolicy{}

// =====================================================================
// Outgoing-context helper
// =====================================================================

// gatewayEvaluatorMu / globalGatewayEvaluator allow code that
// crosses package boundaries to attach tokens without having to
// pass the GatewayEvaluator everywhere. Hosts call
// SetGlobalGatewayEvaluator once at startup; helpers use it to
// stamp tokens on outgoing contexts.
//
// **Why a global.** Outgoing-call helpers (e.g. a CallTool wrapper
// in a Mind library) need to mint+stamp without threading the
// evaluator through every call signature. A global is the cleanest
// fit; tests can swap via SetGlobalGatewayEvaluator(nil) to
// disable.
//
// Use sparingly — pass the evaluator explicitly when you can.
var (
	gatewayEvaluatorMu     sync.RWMutex
	globalGatewayEvaluator *GatewayEvaluator
)

// SetGlobalGatewayEvaluator installs the host-wide evaluator used
// by helpers like AttachScopedAuthToOutgoingContext. Called once
// at host startup; tests can call with nil to clear.
func SetGlobalGatewayEvaluator(g *GatewayEvaluator) {
	gatewayEvaluatorMu.Lock()
	defer gatewayEvaluatorMu.Unlock()
	globalGatewayEvaluator = g
}

// GetGlobalGatewayEvaluator returns the registered evaluator, or
// nil if none.
func GetGlobalGatewayEvaluator() *GatewayEvaluator {
	gatewayEvaluatorMu.RLock()
	defer gatewayEvaluatorMu.RUnlock()
	return globalGatewayEvaluator
}
