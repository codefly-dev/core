package policy

import "context"

// Decider is the host-facing interface for permission decisions.
//
// Conceptually identical to PDP — the same Evaluate signature. The
// alias exists for *documentation clarity*: when reading host code
// (Mind, gateway, CLI) alongside plugin code, "Decider" pairs
// naturally with "Authorizer" — different audiences, different
// shapes, same engine underneath.
//
//	┌──────────────┐      ┌──────────────┐
//	│ host (Mind,  │      │ plugin       │
//	│  gateway)    │      │              │
//	└──────────────┘      └──────────────┘
//	      │                       │
//	      │ Decider                │ Authorizer
//	      │ (full PDPRequest +     │ (action, resource → bool)
//	      │  PDPDecision shape)    │
//	      ▼                       ▼
//	   ┌─────────────────────────────────┐
//	   │ Same underlying engine          │
//	   │ (CeilingPDP → SaasPDP → … )     │
//	   └─────────────────────────────────┘
//
// Implementations:
//   - SaasPDP (pdp_saas.go) — calls saas-starter's Decide RPC
//   - CeilingPDP (pdp_ceiling.go) — manifest enforcement wrapper
//   - ShadowPDP (pdp_shadow.go) — observability-only wrapper
//   - JSONPDP (pdp.go) — file-backed allow-list for dev/test
//   - FakePDP (testharness/pdp.go) — programmable test double
//
// Production hosts compose the stack via BuildPDP (pdp_mode.go).
type Decider = PDP

// PermissionsBackend is the THINNEST interface that a saas-starter
// gRPC client implementation must satisfy. Pulling this out (rather
// than importing saas-starter's full gen package into core) keeps
// the dependency graph one-way: core has no compile-time knowledge
// of saas-starter; saas-starter (or any other backend) wires itself
// into core via this interface.
//
// **Why a separate interface from Decider/PDP.** Decider speaks
// "PDPRequest with caveats and delegation_proof"; PermissionsBackend
// speaks the simpler shape that maps 1:1 to saas-starter's gRPC.
// SaasPDP is the bridge: it takes a PermissionsBackend and adapts
// it to the Decider interface, layering caching + observability +
// metrics on top.
//
// Implementations:
//   - core/policy/pdp_saas_grpc.go (next session) — concrete client
//     against saas-starter's PermissionService.Decide RPC
//   - testharness/pdp.go (in-memory) — for tests
//   - operator-supplied (e.g. Cedar/OPA bridge) — for custom policy
type PermissionsBackend interface {
	// Decide answers "is this principal allowed to perform this
	// action on this resource?" via the backing policy store.
	//
	// Inputs:
	//
	//   - principalID: opaque identifier the principal carries
	//     (saas-starter's principals.id).
	//   - action: dotted name. Mirrors the codefly tool name when
	//     the call is routed from a tool dispatch, but backends
	//     may also see synthetic actions emitted by Authorizer.
	//   - resource: typed identifier ("repo:foo/bar", "env:staging").
	//   - orgID: organization scope. Required when the backend
	//     enforces per-org isolation.
	//   - scope: optional fine-grained scope (saas-starter's
	//     role_assignments.scope column).
	//
	// Outputs:
	//
	//   - allowed: the verdict.
	//   - reason: human-readable when allowed=false. Surfaces to
	//     the model so it can plan around the limitation.
	//   - decisionPath: trace-style explanation
	//     ("role:editor → perm:github.read_pr"). Optional;
	//     backends that can't trace return "".
	//   - err: only for backend faults (network, deserialization).
	//     Policy denies are NOT errors — they're (false, reason, "").
	//     Errors trigger fail-closed at the SaasPDP layer.
	Decide(ctx context.Context, principalID, action, resource, orgID, scope string) (allowed bool, reason, decisionPath string, err error)
}

// Authorizer is the plugin-facing permission interface.
//
// **Why this is separate from Decider.** Plugin authors should
// never construct a PDPRequest by hand. The plugin doesn't know
// about caveats, delegation proofs, manifest declarations, or
// risk levels — those are host concerns. The plugin asks one
// question: "may the calling principal do action X on resource Y?"
// and gets a yes/no with a reason.
//
// Authorizer wraps a Decider. The wrapping handles:
//
//   - Pulling the Principal from ctx (placed by the principal
//     interceptor — see core/agents/principal_interceptor.go)
//   - Building the PDPRequest with the right Toolbox identity
//   - Caching positive decisions briefly to avoid hammering
//     saas-starter when a plugin loops over many resources
//   - Mapping policy denies into a clear (false, reason) shape
//
// **Plugin authors:** read PLUGIN_AUTHORS.md. The short version
// is `policy.AuthorizerFromContext(ctx).Authorized(ctx, action,
// resource)` returns `(allowed bool, reason string, err error)`.
// Use it for sub-operation gating within a tool that's already
// outer-authorized; never as a substitute for declaring
// permissions in your manifest.
type Authorizer interface {
	// Authorized reports whether the principal on ctx is
	// permitted to perform action on resource.
	//
	// **Three return values, three failure modes:**
	//
	//   - (true, "", nil)        — proceed
	//   - (false, reason, nil)   — policy says no; surface reason
	//   - (false, "", err)       — backend failure (network, etc).
	//                              Caller decides whether to fail
	//                              closed (recommended) or treat
	//                              the operation as best-effort.
	//
	// Distinct (false, reason, nil) vs (false, "", err) semantics
	// matter: a clean policy deny is the model's responsibility to
	// handle (retry differently, ask for help). A backend error is
	// an operational issue (saas-starter unreachable) — the model
	// can't recover by retrying with different args.
	Authorized(ctx context.Context, action, resource string) (allowed bool, reason string, err error)
}
