package policy

import (
	"context"
	"fmt"
)

// CeilingPDP enforces the manifest declarations as the maximum
// authority a plugin can ever exercise — even if a saas-starter
// role grants more, this layer denies actions outside the manifest.
//
// **Why this wrapper exists separately from the inner PDP.**
// The role-grant check (saas-starter) and the manifest-ceiling
// check answer different questions:
//
//   - Role-grant: "is this principal CURRENTLY allowed to do X?"
//     (mutable; revokable by an admin in real time)
//   - Manifest-ceiling: "does this plugin's manifest CLAIM the
//     authority to do X?" (set at install; immutable until
//     re-install with a new manifest)
//
// Putting them in the same wrapper makes the order explicit:
//  1. Manifest ceiling — fast, local, fail-loud on undeclared
//  2. Inner PDP (typically saas-starter) — slower, remote, deals
//     with role grants and approval flows
//
// First-deny-wins. If the manifest doesn't declare the action,
// the inner PDP is never consulted — saves a network round-trip
// for actions the plugin couldn't perform anyway.
//
// **Why this is "defense in depth."** Without the ceiling, a
// compromised plugin could have an admin-mistake role grant
// expanded into authority the user never reviewed. With the
// ceiling, the install-time review IS the contract — the plugin
// can never silently exceed it, no matter what role grants
// happen later.
type CeilingPDP struct {
	// Inner is the role-grant PDP (typically SaasPDP). Required.
	Inner PDP

	// Manifest is the plugin's declared authority. The ceiling
	// check uses Manifest.Allows(action, resource).
	Manifest PermissionPolicy

	// RequireManifest controls behavior when Manifest is empty
	// (zero PermissionPolicy):
	//
	//   - false (default during M4 rollout): empty manifest =
	//     "no ceiling enforced", every action passes through to
	//     Inner. This preserves backwards-compat with plugins
	//     that haven't declared permissions yet.
	//
	//   - true (after M4 enforcement flip): empty manifest =
	//     "no actions allowed", every action denied. This is the
	//     production target: every plugin MUST declare its
	//     permissions before the host accepts it.
	//
	// Operators flip this via CODEFLY_PDP_REQUIRE_MANIFEST=true
	// once every plugin has been audited.
	RequireManifest bool
}

// NewCeilingPDP wraps inner with manifest enforcement. Panics on
// nil inner — same as ShadowPDP, the constructor refuses
// misconfiguration that would silently make the wrapper a no-op.
func NewCeilingPDP(inner PDP, manifest PermissionPolicy, requireManifest bool) CeilingPDP {
	if inner == nil {
		panic("policy.NewCeilingPDP: inner PDP must be non-nil")
	}
	return CeilingPDP{
		Inner:           inner,
		Manifest:        manifest,
		RequireManifest: requireManifest,
	}
}

// Evaluate runs the ceiling check, then defers to Inner. The
// manifest decision is the FIRST gate — it short-circuits before
// the inner PDP is touched. This both:
//
//  1. Avoids a saas-starter round-trip for actions the plugin
//     couldn't perform anyway (faster + cheaper)
//  2. Makes the ceiling check unmistakable in audit logs (the
//     decision_path includes "manifest-ceiling" when this layer
//     refused; "role-grant" or "no-grant" come from Inner)
func (c CeilingPDP) Evaluate(ctx context.Context, req *PDPRequest) PDPDecision {
	// What action+resource is being checked? PDPRequest.Tool is
	// the dotted action ("git.status", "github.merge_pr"); the
	// resource is in Args under the conventional "resource" key
	// when the caller supplies one. Tools that operate without a
	// specific resource pass empty.
	action := req.Tool
	resource := ""
	if req.Args != nil {
		if v, ok := req.Args["resource"].(string); ok {
			resource = v
		}
	}

	// Empty manifest semantics depend on RequireManifest.
	if c.Manifest.IsEmpty() {
		if c.RequireManifest {
			return PDPDecision{
				Allow:  false,
				Reason: fmt.Sprintf("manifest-ceiling: plugin declares no permissions; %q on %q denied (CODEFLY_PDP_REQUIRE_MANIFEST=true)", action, resource),
			}
		}
		// Backwards-compat: pass through to Inner.
		return c.Inner.Evaluate(ctx, req)
	}

	// Manifest non-empty: check the ceiling.
	if !c.Manifest.Allows(action, resource) {
		return PDPDecision{
			Allow: false,
			Reason: fmt.Sprintf("manifest-ceiling: plugin's permissions block does not declare %q on %q "+
				"(install-time review surface — re-install with updated manifest if this is intentional)",
				action, resource),
		}
	}

	// Manifest allows — check the role grant via Inner.
	d := c.Inner.Evaluate(ctx, req)
	if d.Allow {
		return d
	}
	// Inner denied. Propagate the reason verbatim — the agent SDK
	// surfaces this to the model so it understands the role-grant
	// gap (vs the manifest-ceiling gap above).
	return d
}

// --- Compile-time interface assertion ------------------------------

var _ PDP = CeilingPDP{}
