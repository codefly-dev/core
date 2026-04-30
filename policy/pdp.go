package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PDPDecision is what the PDP returns. Allow/Deny are exhaustive —
// "no decision" maps to Deny per zero-trust. Reason carries a human-
// readable explanation that surfaces back to the agent so the model
// understands WHY it was refused (and can plan around it).
type PDPDecision struct {
	// Allow is the load-bearing field. When false, the toolbox
	// dispatch layer short-circuits with Reason as the user-visible
	// refusal.
	Allow bool

	// Reason is required when Allow is false; ignored when true.
	// Surface verbatim — the model uses it to decide whether to
	// retry with different arguments, escalate to the user, or
	// abandon the path.
	Reason string
}

// PDPRequest is everything a policy decision point needs to make a
// call. Mirrors the toolbox CallTool surface so a Rego policy can
// reason about exactly what's about to happen — toolbox name, tool
// name, structured arguments, and the calling identity.
//
// Identity is intentionally a free-form map so callers can route
// whatever attribution context the host has (an agent ID, a user
// ID, the parent session). The PDP is responsible for interpreting
// the keys it cares about; unknown keys are ignored.
type PDPRequest struct {
	Toolbox  string         // identity from manifest, e.g. "git"
	Tool     string         // dotted tool name, e.g. "git.status"
	Args     map[string]any // structured arguments (post-decoded)
	Identity map[string]any // caller attribution (agent id, user id, ...)
}

// PDP is the policy decision point a toolbox dispatch layer consults
// before invoking a tool. Implementations:
//
//   - AllowAll: always allows; the codefly default while operators
//     migrate. Useful for development.
//   - DenyAll:  always denies; useful in tests + as a sanity check
//     that the wrap is wired (a flipped flag should refuse every
//     call, surfacing the wrap's effect).
//   - JSONPDP:  reads a JSON allow-list from disk. Working default
//     for production until a real Rego policy is in place.
//   - (operator-supplied) RegoPDP: github.com/open-policy-agent/opa/rego.Rego.
//     Registered via a small adapter the operator writes; codefly
//     does NOT depend on OPA directly (heavy transitive surface).
//
// The interface is intentionally tiny so any of the above is a
// drop-in replacement.
type PDP interface {
	Evaluate(ctx context.Context, req *PDPRequest) PDPDecision
}

// --- AllowAll ---------------------------------------------------

// AllowAllPDP allows every call. Identity surface for "no policy
// configured."
type AllowAllPDP struct{}

func (AllowAllPDP) Evaluate(_ context.Context, _ *PDPRequest) PDPDecision {
	return PDPDecision{Allow: true}
}

// --- DenyAll ----------------------------------------------------

// DenyAllPDP denies every call. Useful in tests + as a sanity check.
type DenyAllPDP struct{}

func (DenyAllPDP) Evaluate(_ context.Context, _ *PDPRequest) PDPDecision {
	return PDPDecision{
		Allow:  false,
		Reason: "policy: deny-all PDP active (no calls permitted)",
	}
}

// --- JSON allow-list --------------------------------------------

// JSONPolicy is a minimal-but-real allow-list. Each rule names a
// toolbox + tool (or "*" for any) and a verdict. First match wins;
// the default if no rule matches is Default (typically "deny" for
// safety, "allow" for development).
//
// Why this exists alongside a Rego target: Rego is great for
// expressive policies (group membership, attribute-based access),
// but it's heavy and operators often start with "git is allowed,
// docker is not." The JSON shape covers that without dragging in
// the OPA Go library.
//
// Migrate to Rego when you find yourself writing predicates the
// JSON shape can't express (boolean logic across attributes,
// time-of-day rules, etc.). Same PDP interface; different evaluator.
type JSONPolicy struct {
	Default string       `json:"default"` // "allow" or "deny"
	Rules   []PolicyRule `json:"rules"`
}

// PolicyRule matches a tool call. Empty fields are wildcards
// (treat as "any"). Allow controls the decision when the rule
// matches.
type PolicyRule struct {
	Toolbox string `json:"toolbox,omitempty"` // "" = any
	Tool    string `json:"tool,omitempty"`    // "" = any (matches with or without dotted prefix)
	Allow   bool   `json:"allow"`
	Reason  string `json:"reason,omitempty"` // surfaced when Allow=false; ignored when true
}

// JSONPDP evaluates a JSONPolicy. Construct with NewJSONPDPFromFile
// or directly via NewJSONPDP for in-memory use (tests).
type JSONPDP struct {
	policy JSONPolicy
}

// NewJSONPDP returns a PDP backed by the given parsed policy.
func NewJSONPDP(p JSONPolicy) *JSONPDP {
	return &JSONPDP{policy: p}
}

// NewJSONPDPFromFile reads a JSON file and constructs a JSONPDP.
// The file shape mirrors JSONPolicy directly; example:
//
//	{
//	  "default": "deny",
//	  "rules": [
//	    {"toolbox": "git", "allow": true},
//	    {"toolbox": "docker", "tool": "docker.list_containers", "allow": true},
//	    {"toolbox": "web", "allow": false, "reason": "no outbound HTTP from this workspace"}
//	  ]
//	}
func NewJSONPDPFromFile(path string) (*JSONPDP, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // caller-trusted policy file
	if err != nil {
		return nil, fmt.Errorf("read policy %s: %w", path, err)
	}
	var p JSONPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse policy %s: %w", path, err)
	}
	if p.Default != "allow" && p.Default != "deny" {
		return nil, fmt.Errorf("policy %s: default must be \"allow\" or \"deny\" (got %q)", path, p.Default)
	}
	return NewJSONPDP(p), nil
}

// Evaluate runs the rules in order. First match wins; the rule's
// Allow field is the decision. If no rule matches, the policy's
// Default applies.
func (j *JSONPDP) Evaluate(_ context.Context, req *PDPRequest) PDPDecision {
	for _, rule := range j.policy.Rules {
		if !ruleMatches(rule, req) {
			continue
		}
		if rule.Allow {
			return PDPDecision{Allow: true}
		}
		reason := rule.Reason
		if reason == "" {
			reason = fmt.Sprintf("policy: %q on %q denied by JSON rule",
				req.Tool, req.Toolbox)
		}
		return PDPDecision{Allow: false, Reason: reason}
	}
	if j.policy.Default == "allow" {
		return PDPDecision{Allow: true}
	}
	return PDPDecision{
		Allow: false,
		Reason: fmt.Sprintf("policy: %q on %q has no matching rule; default-deny applies",
			req.Tool, req.Toolbox),
	}
}

// ruleMatches: empty fields wildcard; Tool may be the dotted form
// ("git.status") or a bare verb ("status") — both match. The dotted
// form matches first, but if a rule says Tool="status" we still
// match a request for "git.status" (the verb suffix matches). This
// makes simple "allow status everywhere" rules concise.
func ruleMatches(rule PolicyRule, req *PDPRequest) bool {
	if rule.Toolbox != "" && rule.Toolbox != req.Toolbox {
		return false
	}
	if rule.Tool == "" {
		return true
	}
	if rule.Tool == req.Tool {
		return true
	}
	// Suffix match — "status" matches "git.status".
	if strings.HasSuffix(req.Tool, "."+rule.Tool) {
		return true
	}
	return false
}
