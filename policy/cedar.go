package policy

import (
	"context"
	"fmt"
	"time"
)

// =====================================================================
// External-policy-engine bridge (Cedar / OPA / Rego / etc.)
// =====================================================================
//
// The YAML schema (yaml_tool_policy.go) covers most operator
// needs without requiring an external policy engine. But for
// richer policies — entity-relationship reasoning, declarative
// allow/deny rules with complex conditions, formal verification —
// operators may want to plug in Cedar (AWS), OPA Rego, or a
// custom DSL.
//
// **Why core doesn't depend on Cedar / OPA directly.** Both bring
// substantial transitive dependencies (parsers, evaluators,
// runtime). Plugins in the codefly ecosystem don't need them.
// Hosts that DO want them implement ExternalPolicyEvaluator
// (below) and wire it via the ExternalToolPolicy adapter.
//
// **The contract.** ExternalPolicyEvaluator takes the same input
// the gateway has — Principal, action, resource, runtime context
// — and returns a Decision with the standard ResolvedToolPolicy
// fields. The adapter handles the integration with
// GatewayEvaluator's lookup model.
//
// Operators implement this once per engine. Codefly never sees
// the Cedar/Rego source; the engine type is private to the
// operator's package.

// ExternalPolicyEvaluator is the bridge interface. Implementations
// wrap a Cedar bundle, OPA bundle, or any other rule engine.
//
// **Implementation notes:**
//
//   - Evaluate is on the gateway hot path. Cache aggressively
//     inside your implementation (Cedar's PolicySet is
//     already-compiled; OPA's prepared queries are cheap to
//     reuse).
//
//   - Return errors only for genuine evaluation faults (engine
//     bug, malformed policy data). A clean policy deny is NOT an
//     error — return ExternalDecision{Allow: false, Reason: ...}.
//     Errors are reported as ErrGatewayDeny by the adapter.
//
//   - The Caveats map in ExternalDecision is what gets baked
//     into the ScopedAuthorization. Engines that compute
//     conditional allows (e.g. "allow only when ci_status=green")
//     should snapshot the matched values here so the plugin's
//     verifiers re-check them.
type ExternalPolicyEvaluator interface {
	Evaluate(ctx context.Context, in EvaluationInput) (ExternalDecision, error)
}

// ExternalDecision is what an external engine returns. Maps
// 1:1 to the gateway's ResolvedToolPolicy plus a deny path.
type ExternalDecision struct {
	// Allow is the verdict.
	Allow bool

	// Reason is required when Allow is false. Surfaces to the
	// model via the gateway's deny path.
	Reason string

	// TTL — token lifetime when Allow=true. Zero uses
	// GatewayEvaluator.DefaultTTL.
	TTL time.Duration

	// MaxUses — single-shot (0 → 1) by default.
	MaxUses int

	// Caveats — name → value pairs to bake into the token.
	// Plugin verifiers must be registered for each name.
	Caveats map[string]any
}

// ExternalToolPolicy wraps an ExternalPolicyEvaluator as a
// ToolPolicy GatewayEvaluator can consume.
//
// Usage:
//
//	cedarEngine := myteam.NewCedarEngine(policyBundle)  // your code
//	gatewayPolicies := map[string]policy.ToolPolicy{
//	    "codefly.dev/github-bot:0.1.0:*": &policy.ExternalToolPolicy{
//	        Evaluator: cedarEngine,
//	    },
//	}
type ExternalToolPolicy struct {
	// Evaluator is the wrapped engine. Required.
	Evaluator ExternalPolicyEvaluator
}

// Evaluate implements ToolPolicy.
func (e *ExternalToolPolicy) Evaluate(ctx context.Context, in EvaluationInput) (ResolvedToolPolicy, error) {
	if e.Evaluator == nil {
		return ResolvedToolPolicy{}, fmt.Errorf("ExternalToolPolicy: nil Evaluator")
	}
	d, err := e.Evaluator.Evaluate(ctx, in)
	if err != nil {
		return ResolvedToolPolicy{}, fmt.Errorf("external policy: %w", err)
	}
	if !d.Allow {
		reason := d.Reason
		if reason == "" {
			reason = "external policy denied"
		}
		return ResolvedToolPolicy{}, fmt.Errorf("%s", reason)
	}
	// Convert the engine's caveat map into producers that just
	// emit the static value. Engines that want dynamic per-call
	// caveat computation should implement a richer interface;
	// this default suits the snapshot-and-bake pattern.
	var producers map[string]CaveatProducer
	if len(d.Caveats) > 0 {
		producers = make(map[string]CaveatProducer, len(d.Caveats))
		for key, value := range d.Caveats {
			value := value // capture
			producers[key] = func(_ EvaluationInput) (any, error) {
				return value, nil
			}
		}
	}
	return ResolvedToolPolicy{
		TTL:             d.TTL,
		MaxUses:         d.MaxUses,
		CaveatProducers: producers,
	}, nil
}

// --- Compile-time assertion ---------------------------------------

var _ ToolPolicy = (*ExternalToolPolicy)(nil)
