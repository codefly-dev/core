package policy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"
)

// =====================================================================
// Synchronous escalation — Phase 3 / M7
// =====================================================================
//
// **What this is.** When a plugin's call hits a policy that
// returns RequireApproval (rather than Allow or Deny), the agent
// SDK turns that into an escalation request: "this principal needs
// a grantor's permission to do action X on resource Y; here's why
// they need it." The grantor reviews, approves or denies; on
// approve, the gateway mints a fresh ScopedAuthorization for that
// specific call and the agent retries.
//
// **Why "scoped" matters here.** The escalated token authorizes
// ONLY that specific (action, resource, principal, time-window).
// The grantor's approval doesn't elevate the principal's role
// permanently — they get authority for THIS one call, expiring
// in seconds. Compromised tokens have minimal blast radius.
//
// **Where the pieces live:**
//   - EscalationRequest / EscalationDecision / EscalationResult:
//     the wire types between agent SDK and grantor
//   - EscalationGrantor: the interface mind/saas-starter implements
//     (concrete RPC client lives outside core)
//   - RequestEscalation: the SDK helper plugin code calls
//   - AuthorizedOrEscalate: the convenience wrapper that retries
//     an Authorized check after escalation
//
// **What runs where:**
//
//   plugin handler                   host (Mind/saas-starter)
//   ─────────────                    ────────────────────────
//   call PDP / Authorized            ────►  ApprovalRequired? mint id, persist
//                                           notify grantor (Slack/email/UI)
//   <───── RequireApproval(id)
//   call host.RequestEscalation      ────►  block on approval state-change
//                                           grantor decides
//                                    ◄────  approve → mint scoped token
//                                           deny    → return deny + reason
//   retry with elevated ctx          ────►  ScopedAuthorization verified
//                                           handler runs

// EscalationRequest is the input to RequestEscalation. The agent
// SDK builds one from the failed authz attempt + the plugin's
// caller-supplied justification.
//
// **Why a separate type from PDPRequest.** PDPRequest is for fast
// authorization decisions (microseconds). EscalationRequest can
// block for minutes waiting for human approval; carries
// notification-relevant fields (justification, requested grantor,
// urgency) that don't belong in a PDP request.
type EscalationRequest struct {
	// Action and Resource are what's being requested. Must match
	// the failed authorization attempt — the elevated token
	// will authorize EXACTLY this (action, resource).
	Action   string
	Resource string

	// Principal is who's requesting. Bound to the spawn-time
	// principal so the plugin can't request escalation as
	// someone else.
	Principal *Principal

	// Justification is the human-readable reason this principal
	// needs the action right now. Surfaced to the grantor in
	// the approval UI. Required — empty justifications are
	// rejected; without one, the grantor has no basis to decide.
	//
	// Examples:
	//   "PR has 2 approvals, CI green, auto-merge label"
	//   "Customer support ticket #123 needs db access"
	//   "Out-of-hours hotfix for INC-456"
	Justification string

	// Grantor optionally targets a specific approver or role
	// ("user:antoine", "team:engineering", "role:admin"). Empty
	// means "the saas-starter default approver chain for this
	// (action, resource)" — typically anyone with the matching
	// role assignment.
	Grantor string

	// Timeout caps how long RequestEscalation blocks. Defaults
	// to 5 minutes if zero. A timeout produces a timeout-deny
	// response (caller distinguishes from a grantor-deny).
	Timeout time.Duration

	// Context carries metadata for the approval UI (PR number,
	// commit sha, etc.). Same shape as EvaluationInput.Context
	// so callers can pass through without translation.
	Context map[string]any

	// PriorRequestID, when set, references a previous
	// RequireApproval decision that triggered this escalation.
	// The grantor can correlate the request with the failed
	// authorization attempt in audit logs.
	PriorRequestID string
}

// Validate checks the request is structurally complete. Called
// by RequestEscalation before going to the grantor; saves a
// round-trip on misconfigured calls.
func (r *EscalationRequest) Validate() error {
	if r == nil {
		return errors.New("escalation: nil request")
	}
	if r.Principal == nil {
		return errors.New("escalation: nil principal")
	}
	if err := r.Principal.Validate(); err != nil {
		return fmt.Errorf("escalation: %w", err)
	}
	if r.Action == "" {
		return errors.New("escalation: action required")
	}
	if r.Justification == "" {
		// Hard requirement: grantors need a reason. Empty
		// justifications produce empty approval prompts that
		// just rubber-stamp.
		return errors.New("escalation: justification required (the grantor needs to know why)")
	}
	return nil
}

// EscalationDecision is the grantor's verdict.
type EscalationDecision int

const (
	EscalationDecisionUnspecified EscalationDecision = iota
	// EscalationApproved: grantor approved; the gateway minted
	// a fresh ScopedAuthorization (in EscalationResult.Token).
	EscalationApproved
	// EscalationDenied: grantor explicitly refused. Reason in
	// EscalationResult.Reason.
	EscalationDenied
	// EscalationTimedOut: nobody decided within the request's
	// Timeout. Distinct from EscalationDenied so the SDK can
	// surface "no answer yet" vs "explicit no".
	EscalationTimedOut
)

func (d EscalationDecision) String() string {
	switch d {
	case EscalationApproved:
		return "approved"
	case EscalationDenied:
		return "denied"
	case EscalationTimedOut:
		return "timed_out"
	}
	return "unspecified"
}

// EscalationResult is the grantor's response. On Approved, Token
// carries a ScopedAuthorization the agent can attach to its retry
// via the standard metadata header.
type EscalationResult struct {
	// Decision is the verdict.
	Decision EscalationDecision

	// Token is the encoded ScopedAuthorization minted on
	// approve. Empty on deny / timeout.
	Token string

	// Authorization is the decoded form (same data as Token) for
	// inspection / audit logging.
	Authorization *ScopedAuthorization

	// Reason is human-readable. Required on deny; optional on
	// approve (some approvers add a note explaining why); empty
	// on timeout.
	Reason string

	// Decider is the principal that made the decision. Empty on
	// timeout. Always populated on approve/deny so audit traces
	// which grantor signed off.
	Decider string

	// GrantID is the saas-starter delegation_grants.id row that
	// recorded this decision. Lets auditors trace from a tool
	// call back to the exact grant that allowed it.
	GrantID string
}

// EscalationGrantor is the interface mind/saas-starter
// implements. Concrete implementations route the request through
// their notification + approval pipeline (Slack, in-app inbox,
// email, etc.) and return the grantor's verdict synchronously.
//
// **Why this is a one-method interface.** The agent SDK's
// flow is: build a request, call Request, get a result. Anything
// richer (the request_id mid-flight, polling, etc.) is the
// implementation's concern.
//
// **Why core defines the interface but no implementation.** Core
// has zero dependency on saas-starter (one-way arrow). Operators
// implementing this interface call their own RequestDelegation /
// WaitForDelegation RPCs and translate to/from these types.
type EscalationGrantor interface {
	// Request submits the escalation, blocks until decided OR
	// the request's Timeout elapses, returns the result.
	//
	// Errors are for INFRASTRUCTURE failures (network,
	// auth-backend down). A grantor-decided deny is NOT an
	// error — it's EscalationResult{Decision: EscalationDenied,
	// Reason: ...}. The SDK treats infrastructure errors as
	// fail-closed (the action doesn't proceed); a deny is
	// distinguished from a timeout so the model knows whether
	// to retry differently or give up.
	Request(ctx context.Context, req EscalationRequest) (*EscalationResult, error)
}

// =====================================================================
// Grantor registry — process-singleton like the gateway evaluator
// =====================================================================
//
// Plugin code that wants to call RequestEscalation (e.g. when
// retrying after RequireApproval) needs an EscalationGrantor.
// Mirrors SetGlobalGatewayEvaluator: hosts wire one at startup;
// SDK helpers fall back to "no grantor configured" with a clear
// error message.

var (
	grantorMu       sync.RWMutex
	globalGrantor   EscalationGrantor
)

// SetGlobalEscalationGrantor installs the host-wide grantor
// used by RequestEscalation when no grantor is supplied
// explicitly. Hosts call this once at startup; tests pass nil
// to clear.
func SetGlobalEscalationGrantor(g EscalationGrantor) {
	grantorMu.Lock()
	defer grantorMu.Unlock()
	globalGrantor = g
}

// GetGlobalEscalationGrantor returns the registered grantor or
// nil if none.
func GetGlobalEscalationGrantor() EscalationGrantor {
	grantorMu.RLock()
	defer grantorMu.RUnlock()
	return globalGrantor
}

// =====================================================================
// SDK helpers
// =====================================================================

// ErrEscalationDenied is returned when the grantor explicitly
// refuses. Callers can errors.Is to distinguish from infrastructure
// errors and from timeouts.
var ErrEscalationDenied = errors.New("escalation: denied")

// ErrEscalationTimedOut is returned when the request times out
// without a decision. Distinct from ErrEscalationDenied so the
// SDK can retry or surface "no answer yet" appropriately.
var ErrEscalationTimedOut = errors.New("escalation: timed out")

// ErrNoGrantor is returned when RequestEscalation runs with no
// grantor configured. Distinct from infrastructure failure so
// operators see "you forgot to wire the grantor" loud.
var ErrNoGrantor = errors.New("escalation: no grantor configured (call SetGlobalEscalationGrantor at startup)")

// RequestEscalation is the SDK entry point for plugins that need
// to escalate authority. It:
//
//   1. Validates the request (action, principal, justification).
//   2. Calls the grantor (global or supplied).
//   3. On approve: returns ctx with the scoped-auth metadata
//      attached, ready for the plugin to retry the failed call.
//   4. On deny: returns ErrEscalationDenied (wrapping the reason).
//   5. On timeout: returns ErrEscalationTimedOut.
//   6. On infrastructure error: returns the underlying error.
//
// **Plugin-side usage:**
//
//	// Initial call hit RequireApproval; escalate.
//	ctx, err := policy.RequestEscalation(ctx, policy.EscalationRequest{
//	    Principal:     policy.PrincipalFrom(callCtx),
//	    Action:        "github.merge_pr",
//	    Resource:      "pr:codefly-dev/codefly.dev/456",
//	    Justification: "PR has 2 approvals, CI green, auto-merge label",
//	    Timeout:       5 * time.Minute,
//	})
//	if err != nil { return err }
//	// Retry with elevated ctx — the metadata header carries the
//	// scoped-auth token; downstream Guard verifies + dispatches.
//	return github.MergePR(ctx, prID)
//
// The ctx returned has the scoped-auth metadata attached as
// outgoing-context metadata (so it travels on the next gRPC
// call automatically). For non-gRPC calls, the caller can read
// the token via ScopedAuthFrom(ctx) — also stamped — and attach
// it however the downstream channel expects.
func RequestEscalation(ctx context.Context, req EscalationRequest) (context.Context, error) {
	if err := req.Validate(); err != nil {
		return ctx, err
	}
	grantor := GetGlobalEscalationGrantor()
	if grantor == nil {
		return ctx, ErrNoGrantor
	}
	if req.Timeout <= 0 {
		req.Timeout = 5 * time.Minute
	}

	// Apply the timeout to the grantor call. Even if the grantor
	// implementation forgets to honor Timeout, the parent ctx
	// cancel will unblock.
	callCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	result, err := grantor.Request(callCtx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ctx, ErrEscalationTimedOut
		}
		return ctx, err
	}

	switch result.Decision {
	case EscalationApproved:
		// Stamp the verified ScopedAuthorization on ctx +
		// attach the encoded token as outgoing metadata for
		// the next gRPC hop.
		ctx = WithScopedAuth(ctx, result.Authorization)
		ctx = withOutgoingScopedAuthHeader(ctx, result.Token)
		return ctx, nil
	case EscalationDenied:
		reason := result.Reason
		if reason == "" {
			reason = "no reason provided"
		}
		return ctx, fmt.Errorf("%w: %s", ErrEscalationDenied, reason)
	case EscalationTimedOut:
		return ctx, ErrEscalationTimedOut
	}
	return ctx, fmt.Errorf("escalation: unexpected decision %v", result.Decision)
}

// AuthorizedOrEscalate is the high-ergonomic wrapper for plugin
// code: try authorization, escalate on RequireApproval, return
// the elevated ctx.
//
// **Three outcomes:**
//   - allowed (no escalation needed): returns the original ctx, nil
//   - escalation approved: returns ctx with the elevated token, nil
//   - denied / timed-out / no-grantor: returns ctx, error
//
// The plugin uses the returned ctx for the actual call; the SDK
// hides whether the call's authority came from the principal's
// own grants OR a grantor's escalation.
//
// Justification is REQUIRED — without it, escalation rejects at
// validation. Plugin authors who don't have a justification
// should use Authorized directly and surface the deny to the
// model.
func AuthorizedOrEscalate(
	ctx context.Context,
	authorizer Authorizer,
	action, resource, justification string,
) (context.Context, error) {
	if authorizer == nil {
		authorizer = AuthorizerFromContext(ctx)
	}
	allowed, reason, err := authorizer.Authorized(ctx, action, resource)
	if err != nil {
		return ctx, fmt.Errorf("authorized check failed: %w", err)
	}
	if allowed {
		return ctx, nil
	}
	// Not allowed. Try escalation.
	p := PrincipalFrom(ctx)
	if p == nil {
		return ctx, fmt.Errorf("authorized=false (%s); no principal on ctx, cannot escalate", reason)
	}
	return RequestEscalation(ctx, EscalationRequest{
		Principal:     p,
		Action:        action,
		Resource:      resource,
		Justification: justification,
	})
}

// withOutgoingScopedAuthHeader attaches the encoded scoped-auth
// token as outgoing gRPC metadata. The next gRPC call from this
// ctx carries the header; the receiving plugin's Guard verifies it.
//
// **Why append-or-replace.** If ctx already carries outgoing
// metadata (e.g. the principal header from an earlier interceptor),
// AppendToOutgoingContext extends it rather than overwriting.
// If a previous escalation already set this header, the new value
// REPLACES — repeated escalations within one logical call should
// surface the most recent token, not a stack of them.
func withOutgoingScopedAuthHeader(ctx context.Context, token string) context.Context {
	// Strip any existing scoped-auth header by reconstructing
	// outgoing metadata without it, then add the new value.
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return metadata.AppendToOutgoingContext(ctx, ScopedAuthMetadataKey, token)
	}
	clean := metadata.MD{}
	for k, vs := range md {
		if k == ScopedAuthMetadataKey {
			continue
		}
		clean[k] = vs
	}
	clean.Set(ScopedAuthMetadataKey, token)
	return metadata.NewOutgoingContext(ctx, clean)
}
