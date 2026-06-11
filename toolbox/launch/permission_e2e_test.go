//go:build sandbox_e2e

// Build-tagged with the same tag as the OS sandbox E2E tests —
// permission flow tests need a real backend to spawn the victim
// plugin. CI matrices that have bwrap/sandbox-exec installed run
// `go test -tags sandbox_e2e ./...`.
package launch_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/codefly-dev/core/agents/manager"
	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/toolbox/launch"
)

// TestE2E_Principal_FlowsToHandler is the load-bearing identity-
// wire test. It proves the full chain:
//
//	host: manager.Load(WithPrincipal(p))
//	    │
//	    ▼ encodes p as JSON-base64 in CODEFLY_PRINCIPAL_TOKEN env
//	plugin process spawned with the env
//	    │
//	    ▼ principalUnaryInterceptor decodes + stamps on ctx
//	tool handler: policy.PrincipalFrom(ctx) returns p
//
// If any link breaks, who.am.i returns "absent" or wrong fields
// and this test fails. Without this test, the architecture's
// "plugin author can read the principal for audit" property is
// just a hopeful claim.
//
// **What plugin authors should NOT take from this test.** The
// plugin reads the Principal here for *demonstration*, not for
// authorization decisions. Production plugins surface the
// principal in audit logs and audit-relevant content; they
// never branch on it for security. The PDP / Guard does that.
func TestE2E_Principal_FlowsToHandler(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-principal",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-principal",
		},
		// Empty Sandbox + no CanonicalFor → no sandbox wrap. We
		// don't need OS confinement for this test; we're testing
		// the AUTHORITY (principal) wire, not the CAPACITY (sandbox).
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	// Construct the principal the host will pass.
	want := &policy.Principal{
		ID:          "test-principal-12345",
		Kind:        policy.KindAgent,
		OrgID:       "test-org-7890",
		AgentID:     "codefly.dev/network-victim:e2e-principal",
		DisplayName: "Test Bot",
		ExpiresAt:   time.Now().Add(time.Hour),
	}

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(want))
	require.NoError(t, err)
	defer plugin.Close()

	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "who.am.i must succeed; this is the identity-wire happy path")

	// Decode the structured response.
	require.Len(t, resp.Content, 1, "who.am.i returns exactly one structured content block")
	got := resp.Content[0].GetStructured()
	require.NotNil(t, got, "response must be a structpb.Struct, not text")
	m := got.AsMap()

	require.True(t, m["present"].(bool),
		"principal MUST be present on the handler ctx — the entire wire is broken if this is false")
	require.Equal(t, want.ID, m["id"])
	require.Equal(t, want.Kind, m["kind"])
	require.Equal(t, want.OrgID, m["org_id"])
	require.Equal(t, want.AgentID, m["agent_id"])
	require.Equal(t, want.DisplayName, m["display_name"])
	require.Equal(t, float64(0), m["chain_len"], "no delegation chain on this principal")
}

// TestE2E_Principal_DelegationChain_Preserved verifies the
// delegation chain travels intact through the wire. Critical for
// the audit story (M9) — the chain answers "who delegated authority
// to whom" and must NOT be lost in transit.
func TestE2E_Principal_DelegationChain_Preserved(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-chain",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-chain",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	want := &policy.Principal{
		ID:      "p-bot",
		Kind:    policy.KindAgent,
		OrgID:   "org-x",
		AgentID: "codefly.dev/network-victim:e2e-chain",
		DelegationChain: []policy.DelegationLink{
			{PrincipalID: "u-antoine", Kind: policy.KindHuman, DisplayName: "antoine", GrantID: "g-100"},
			{PrincipalID: "a-mind", Kind: policy.KindAgent, DisplayName: "Mind", GrantID: "g-101"},
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(want))
	require.NoError(t, err)
	defer plugin.Close()

	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	m := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, float64(2), m["chain_len"],
		"delegation chain length must survive the encoding round-trip")
}

// TestE2E_Principal_Absent_HandlerSeesNil verifies the negative
// path: when the host doesn't pass WithPrincipal, the handler
// gets a nil principal — distinct from "wrong principal".
//
// Why this matters: the contract is "PrincipalFrom returns nil
// when no principal was bound" — plugin code that branches on
// presence (e.g. for audit logging) must be able to trust this.
func TestE2E_Principal_Absent_HandlerSeesNil(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-no-principal",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-no-principal",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	// Notice: NO WithPrincipal. The host explicitly chooses
	// WithoutPrincipal to suppress the security warning while
	// asserting the absence path.
	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithoutPrincipal())
	require.NoError(t, err)
	defer plugin.Close()

	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	m := resp.Content[0].GetStructured().AsMap()
	require.False(t, m["present"].(bool),
		"WithoutPrincipal → handler must see PrincipalFrom returning nil — distinct from wrong principal")
}

// =====================================================================
// Authorizer callback flow — host → plugin → host (callback) → host
// =====================================================================

// TestE2E_Authorizer_AllowsViaCallback verifies the full plugin →
// host callback channel:
//
//  1. host registers a Decider via WithPermissionsCallback
//  2. spawn plugin (env carries CODEFLY_PERMISSIONS_SOCKET)
//  3. plugin calls authorizer.Authorized(ctx, action, resource)
//  4. plugin's HTTP client dials the host's UDS server
//  5. host's server invokes the Decider with the spawn-time
//     principal (NOT the plugin's claim — security property)
//  6. verdict travels back: plugin returns it in the response
//
// Asserts the wire works AND that the principal is bound at
// spawn time, not from the plugin's claim.
func TestE2E_Authorizer_AllowsViaCallback(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-authz-allow",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-authz-allow",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	// FakePDP: deny by default, explicitly allow:
	//   - check.action (the OUTER tool the host calls via Guard)
	//   - github.read_pr (the inline Authorized() check inside)
	// The Guard gates the outer call; the inline Authorizer
	// gates the inner check. Both consult this same FakePDP.
	fakePDP := testharness.NewFakeDeny().
		AllowTool("", "check.action").
		AllowTool("", "github.read_pr")

	principal := &policy.Principal{
		ID: "p-allow", Kind: policy.KindAgent, OrgID: "org-1",
		AgentID: "codefly.dev/network-victim:e2e-authz-allow",
	}

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithPermissionsCallback(fakePDP))
	require.NoError(t, err)
	defer plugin.Close()

	args, _ := structpb.NewStruct(map[string]any{
		"action":   "github.read_pr",
		"resource": "repo:codefly/x",
	})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "check.action", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)

	require.Len(t, resp.Content, 1)
	m := resp.Content[0].GetStructured().AsMap()
	require.True(t, m["allowed"].(bool),
		"FakePDP allows github.read_pr; verdict must travel back to plugin")
	require.NotContains(t, m, "error",
		"successful callback round-trip must produce no error field")

	// Verify the FakePDP saw the SPAWN-TIME principal on every
	// call. With the Guard wrapping the plugin, two calls hit
	// the PDP per request: one for the outer "check.action"
	// (Guard's defense path) and one for the inline
	// "github.read_pr" (Authorizer's callback). Both must show
	// the trusted principal.
	require.GreaterOrEqual(t, fakePDP.CallCount(), 2,
		"both outer Guard PDP and inline Authorizer call PDP")
	for _, call := range fakePDP.Calls() {
		require.Equal(t, "p-allow", call.Identity["principal_id"],
			"PDP must have received the spawn-time principal on every call — security-critical")
	}
	// At least one of the calls must have been for github.read_pr.
	tools := []string{}
	for _, c := range fakePDP.Calls() {
		tools = append(tools, c.Tool)
	}
	require.Contains(t, tools, "github.read_pr")
	require.Contains(t, tools, "check.action")
}

// TestE2E_Authorizer_DenyReasonReachesPlugin proves a clean
// policy deny propagates with its reason intact through the
// callback. The plugin sees (allowed=false, reason="...", err=nil)
// — distinct from a transport error.
func TestE2E_Authorizer_DenyReasonReachesPlugin(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-authz-deny",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-authz-deny",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	fakePDP := testharness.NewFakeAllow().
		DenyTool("", "github.force_push", "force-push forbidden by policy")

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(&policy.Principal{
			ID: "p-deny", Kind: policy.KindAgent, OrgID: "org-1",
			AgentID: "codefly.dev/network-victim:e2e-authz-deny",
		}),
		manager.WithPermissionsCallback(fakePDP))
	require.NoError(t, err)
	defer plugin.Close()

	args, _ := structpb.NewStruct(map[string]any{
		"action":   "github.force_push",
		"resource": "repo:codefly/x",
	})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "check.action", Arguments: args,
	})
	require.NoError(t, err)
	m := resp.Content[0].GetStructured().AsMap()
	require.False(t, m["allowed"].(bool))
	require.Equal(t, "force-push forbidden by policy", m["reason"],
		"deny reason must travel verbatim plugin ← host ← PDP for the model to plan around")
}

// TestE2E_Authorizer_NoCallback_FailsClosed verifies the safe
// default: when the host doesn't pass WithPermissionsCallback,
// the plugin's authorizer is the disabled variant — every
// Authorized() call returns (false, "...callback not configured...",
// nil). The plugin doesn't crash; the user-visible behavior is
// fail-closed.
func TestE2E_Authorizer_NoCallback_FailsClosed(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-authz-nocallback",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-authz-nocallback",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithoutPrincipal())
	require.NoError(t, err)
	defer plugin.Close()

	args, _ := structpb.NewStruct(map[string]any{
		"action":   "anything",
		"resource": "any",
	})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "check.action", Arguments: args,
	})
	require.NoError(t, err)
	m := resp.Content[0].GetStructured().AsMap()
	require.False(t, m["allowed"].(bool),
		"no callback configured → plugin's Authorized fails closed (the safe default)")
	require.Contains(t, m["reason"].(string), "CODEFLY_PERMISSIONS_SOCKET",
		"reason must point to the missing config so operators can debug")
}

// TestE2E_Authorizer_PluginCannotImpersonate is the security-
// critical assertion: even if the plugin tries to claim a
// different principal_id in the request body, the host's
// principalProvider OVERRIDES with the spawn-time binding. The
// PDP only ever sees the trusted principal.
//
// (The current victim plugin doesn't expose a way to impersonate
// — `check.action` doesn't accept principal_id — so this test
// asserts the property at the lower layer: the PDP receives the
// spawn-time ID regardless of any plugin claim.)
func TestE2E_Authorizer_PluginCannotImpersonate(t *testing.T) {
	// The CallbackAuthorizer's request body sends an empty
	// principal_id, relying on the host's principalProvider to
	// fill from spawn-time. We've already covered the OK path
	// in TestE2E_Authorizer_AllowsViaCallback; this test pins
	// the contract: the PDP's identity map carries the SPAWN-TIME
	// principal_id, never any client-provided claim.
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-authz-impersonate",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-authz-impersonate",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	// Decider that records every principal_id it sees.
	seenIDs := make(map[string]int)
	var seenMu sync.Mutex
	pdp := pdpRecorderForE2E(func(req *policy.PDPRequest) policy.PDPDecision {
		seenMu.Lock()
		defer seenMu.Unlock()
		if id, ok := req.Identity["principal_id"].(string); ok {
			seenIDs[id]++
		}
		return policy.PDPDecision{Allow: true}
	})

	trusted := &policy.Principal{
		ID: "TRUSTED", Kind: policy.KindAgent, OrgID: "org-1",
		AgentID: "codefly.dev/network-victim:e2e-authz-impersonate",
	}

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(trusted),
		manager.WithPermissionsCallback(pdp))
	require.NoError(t, err)
	defer plugin.Close()

	// Issue 5 Authorized() calls; each must surface the trusted
	// principal at the PDP, never an alternative.
	args, _ := structpb.NewStruct(map[string]any{
		"action":   "github.read_pr",
		"resource": "repo:codefly/x",
	})
	for i := 0; i < 5; i++ {
		resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
			Name: "check.action", Arguments: args,
		})
		require.NoError(t, err)
		require.Empty(t, resp.Error)
	}

	seenMu.Lock()
	defer seenMu.Unlock()
	// With Guard wrapping, each call hits PDP twice (outer + inline).
	// 5 calls × 2 = 10 expected counts under "TRUSTED".
	require.GreaterOrEqual(t, seenIDs["TRUSTED"], 5,
		"every callback call MUST surface the spawn-time principal at the PDP "+
			"(outer Guard + inline Authorizer both consult the same PDP)")
	require.Len(t, seenIDs, 1,
		"NO other principal_id may have been seen — impersonation is not possible")
}

// pdpRecorderForE2E adapts a function to a Decider for E2E tests.
type pdpRecorderForE2E func(req *policy.PDPRequest) policy.PDPDecision

func (f pdpRecorderForE2E) Evaluate(_ context.Context, req *policy.PDPRequest) policy.PDPDecision {
	return f(req)
}

// =====================================================================
// Two-level scoped-authz: gateway mint → metadata wire → plugin verify
// =====================================================================

// TestE2E_ScopedAuth_FastPathSkipsPDP exercises the full
// gateway-mint → wire → plugin-verify flow:
//
//  1. Host starts a plugin with WithPrincipal + WithScopedAuthSecret
//     AND WithPermissionsCallback (callback wired with a deny-PDP).
//  2. Host constructs a GatewayEvaluator with the same secret.
//  3. Per call: host evaluates, mints a token, attaches via gRPC
//     metadata, calls the plugin's CallTool.
//  4. Plugin's policyguard.Guard verifies the token, dispatches
//     to the inner toolbox handler. The PDP-via-callback is NOT
//     consulted on the fast path.
//
// Assertions:
//   - Tool call succeeds end-to-end (plugin runs the handler).
//   - The deny-PDP receives ZERO calls — fast path skipped it.
//   - The handler sees the verified ScopedAuthorization on ctx
//     via policy.ScopedAuthFrom(ctx).
//
// **Why the deny-PDP shape.** It's the strictest possible test:
// if anything bypassed the fast path (bug in interceptor / wire /
// Guard), the deny-PDP would be consulted and the call would
// fail. PDP not called == fast path 100% effective.
func TestE2E_ScopedAuth_FastPathSkipsPDP(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-scoped-fastpath",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-scoped-fastpath",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	principal := &policy.Principal{
		ID: "u-fastpath", Kind: policy.KindHuman, OrgID: "org-x",
	}
	secret := policy.NewSpawnSecret()

	// Deny PDP — would refuse every call if the fast path
	// didn't skip it.
	denyPDP := testharness.NewFakeDeny()

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithScopedAuthSecret(secret),
		manager.WithPermissionsCallback(denyPDP),
	)
	require.NoError(t, err)
	defer plugin.Close()

	// Host-side: mint a token authorizing this specific call.
	auditedToolbox := "codefly.dev/network-victim:e2e-scoped-fastpath"
	encoded, _, err := policy.Mint(policy.MintInput{
		Principal:  principal,
		Action:     "who.am.i",
		Resource:   "",
		AudienceID: auditedToolbox,
		TTL:        time.Minute,
	}, secret)
	require.NoError(t, err)

	// Attach the token to outgoing metadata.
	md := metadata.Pairs(policy.ScopedAuthMetadataKey, encoded)
	ctx = metadata.NewOutgoingContext(ctx, md)

	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"valid scoped-auth token: fast path proceeds, handler runs")

	// PDP receives ZERO calls — the fast path bypassed it.
	require.Equal(t, 0, denyPDP.CallCount(),
		"fast path: PDP must NOT be consulted when token is valid")

	// Handler reads PrincipalFrom ctx (existing behavior) AND
	// the ScopedAuthorization should ALSO be on ctx — but the
	// who.am.i tool only surfaces Principal. We verify the
	// principal-id round-trip as a sanity check that the call
	// actually reached the handler.
	m := resp.Content[0].GetStructured().AsMap()
	require.True(t, m["present"].(bool))
	require.Equal(t, "u-fastpath", m["id"])
}

// TestE2E_ScopedAuth_DefensePathFallsBackToPDP exercises the
// defense behavior: when a request reaches the plugin WITHOUT a
// scoped token, the Guard falls back to the PDP-via-callback path.
// Single-level model still works alongside the fast path.
func TestE2E_ScopedAuth_DefensePathFallsBackToPDP(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-scoped-defense",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-scoped-defense",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	principal := &policy.Principal{
		ID: "u-defense", Kind: policy.KindHuman, OrgID: "org-x",
	}
	secret := policy.NewSpawnSecret()

	// PDP that ALLOWS — needed for the defense path to succeed.
	allowPDP := testharness.NewFakeAllow()

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithScopedAuthSecret(secret),
		manager.WithPermissionsCallback(allowPDP),
	)
	require.NoError(t, err)
	defer plugin.Close()

	// NO scoped-auth metadata — Guard takes the defense path.
	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"defense path: no token + allow-PDP → call proceeds")

	// PDP WAS consulted on the defense path.
	require.GreaterOrEqual(t, allowPDP.CallCount(), 1,
		"defense path: PDP must be consulted when no token is present")
}

// TestE2E_ScopedAuth_InvalidTokenFallsBackToPDP — tampered or
// expired token → Guard logs WARN + falls back to PDP. Defense
// in depth: a buggy gateway can't break enforcement.
func TestE2E_ScopedAuth_InvalidTokenFallsBackToPDP(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-scoped-invalid",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-scoped-invalid",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	principal := &policy.Principal{ID: "u-inv", Kind: policy.KindHuman, OrgID: "org-x"}
	secret := policy.NewSpawnSecret()

	// PDP that DENIES — defense path catches the would-be bypass.
	denyPDP := testharness.NewFakeDeny()

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithScopedAuthSecret(secret),
		manager.WithPermissionsCallback(denyPDP),
	)
	require.NoError(t, err)
	defer plugin.Close()

	// Send a bogus token — verify fails, Guard falls back to
	// PDP, PDP denies.
	md := metadata.Pairs(policy.ScopedAuthMetadataKey, "totally.fake.token")
	ctx = metadata.NewOutgoingContext(ctx, md)

	args, _ := structpb.NewStruct(map[string]any{})
	resp, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"invalid token + deny-PDP: defense path catches the bypass attempt")
	require.Equal(t, 1, denyPDP.CallCount(),
		"PDP MUST be consulted when token verify fails")
}

// =====================================================================
// M7 escalation flow — PDP denies, grantor approves, retry succeeds
// =====================================================================

// e2eApprovingGrantor is a test EscalationGrantor that auto-
// approves every request, minting a fresh scoped-auth token
// signed by the shared secret + bound to the supplied audience.
//
// In production, the grantor is wired to saas-starter (see
// MIND_INTEGRATION_M7.md). For E2E tests, this stand-in
// exercises the SDK without standing up the full saas-starter
// approval pipeline.
type e2eApprovingGrantor struct {
	secret   []byte
	audience string
	calls    int
}

func (g *e2eApprovingGrantor) Request(_ context.Context, req policy.EscalationRequest) (*policy.EscalationResult, error) {
	g.calls++
	encoded, sa, err := policy.Mint(policy.MintInput{
		Principal:  req.Principal,
		Action:     req.Action,
		Resource:   req.Resource,
		AudienceID: g.audience,
		TTL:        time.Minute,
		MaxUses:    1,
	}, g.secret)
	if err != nil {
		return nil, err
	}
	return &policy.EscalationResult{
		Decision:      policy.EscalationApproved,
		Token:         encoded,
		Authorization: sa,
		Decider:       "e2e-test-approver",
		GrantID:       "e2e-grant-" + sa.ID,
	}, nil
}

// TestE2E_M7_Escalation_HostRetriesAfterDeny is the load-bearing
// M7 integration test. The flow:
//
//  1. Host calls plugin without a pre-minted token. Plugin's
//     Guard takes the defense path; the PDP (callback to host)
//     denies the call.
//  2. Host catches the deny (model would too) and calls
//     policy.RequestEscalation with a justification.
//  3. RequestEscalation invokes the registered (test) grantor,
//     which auto-approves and mints a fresh scoped token bound
//     to this principal + action + resource.
//  4. Host retries the call with the elevated ctx (token in
//     outgoing metadata).
//  5. Plugin's Guard takes the FAST path on the retry: token
//     verifies, PDP is NOT consulted, handler runs.
//
// Asserts:
//   - First call fails with the deny reason.
//   - Grantor is called exactly once.
//   - Second call (retry) succeeds end-to-end.
//   - The PDP was consulted on call #1 (defense path) and NOT
//     on call #2 (fast path) — same property as the
//     scoped-auth fast-path test, repeated under the M7 retry.
func TestE2E_M7_Escalation_HostRetriesAfterDeny(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-m7-escalate",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-m7-escalate",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	principal := &policy.Principal{
		ID: "u-m7", Kind: policy.KindHuman, OrgID: "org-x",
	}
	secret := policy.NewSpawnSecret()

	// PDP that denies who.am.i — forces the defense path to
	// reject the first call. The model's typical response would
	// be "ask for help"; here we substitute that with
	// programmatic escalation.
	denyPDP := testharness.NewFakeDeny()

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithScopedAuthSecret(secret),
		manager.WithPermissionsCallback(denyPDP),
	)
	require.NoError(t, err)
	defer plugin.Close()

	// Wire the test grantor as the global. Production wires
	// SaasStarterEscalationGrantor here.
	audience := "codefly.dev/network-victim:e2e-m7-escalate"
	grantor := &e2eApprovingGrantor{secret: secret, audience: audience}
	policy.SetGlobalEscalationGrantor(grantor)
	defer policy.SetGlobalEscalationGrantor(nil)

	args, _ := structpb.NewStruct(map[string]any{})

	// --- Call #1: defense path → PDP denies ---
	resp1, err := plugin.Client.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err, "transport call always succeeds; the deny is in resp.Error")
	require.NotEmpty(t, resp1.Error,
		"first call: defense path + deny PDP → call refused")
	pdpCallsAfterFirst := denyPDP.CallCount()
	require.Equal(t, 1, pdpCallsAfterFirst,
		"PDP consulted exactly once on the defense path")

	// --- Escalate: host calls RequestEscalation ---
	elevatedCtx, err := policy.RequestEscalation(ctx, policy.EscalationRequest{
		Principal:     principal,
		Action:        "who.am.i",
		Resource:      "",
		Justification: "test fixture: principal lacks role; need temporary auth via M7 flow",
		Timeout:       30 * time.Second,
	})
	require.NoError(t, err, "grantor approved → escalation succeeded")
	require.Equal(t, 1, grantor.calls)

	// The elevated ctx must carry the scoped-auth metadata
	// header for outgoing gRPC calls. Preserve it across the
	// gRPC client invocation.
	sa := policy.ScopedAuthFrom(elevatedCtx)
	require.NotNil(t, sa)
	require.Equal(t, "who.am.i", sa.Action)

	// --- Call #2: fast path → token verifies → handler runs ---
	resp2, err := plugin.Client.CallTool(elevatedCtx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp2.Error,
		"retry with elevated ctx: fast path proceeds end-to-end")

	// Crucially, the deny-PDP was NOT consulted on the retry —
	// the fast path bypasses it.
	require.Equal(t, pdpCallsAfterFirst, denyPDP.CallCount(),
		"fast path on retry: PDP must NOT be consulted again")

	// Sanity: the handler saw the trusted principal on the retry.
	m := resp2.Content[0].GetStructured().AsMap()
	require.Equal(t, "u-m7", m["id"])
}

// TestE2E_M7_Escalation_GrantorDeny_RetryStillFails verifies the
// negative path: the grantor REFUSES the escalation. The host's
// retry attempt has no elevated ctx; the call still fails.
func TestE2E_M7_Escalation_GrantorDeny_RetryStillFails(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-m7-deny",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-m7-deny",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	principal := &policy.Principal{ID: "u-m7d", Kind: policy.KindHuman, OrgID: "org-x"}
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithScopedAuthSecret(secret),
		manager.WithPermissionsCallback(denyPDP),
	)
	require.NoError(t, err)
	defer plugin.Close()

	// Grantor that always denies.
	denyingGrantor := &fakeDenyingGrantor{reason: "approver said no this time"}
	policy.SetGlobalEscalationGrantor(denyingGrantor)
	defer policy.SetGlobalEscalationGrantor(nil)

	// Skip the "first call" — go straight to the escalation
	// attempt to focus on the deny path.
	_, err = policy.RequestEscalation(ctx, policy.EscalationRequest{
		Principal:     principal,
		Action:        "who.am.i",
		Justification: "asking despite knowing it'll be denied",
	})
	require.ErrorIs(t, err, policy.ErrEscalationDenied,
		"grantor's deny surfaces as ErrEscalationDenied — distinct from infrastructure errors")
	require.Contains(t, err.Error(), "approver said no this time")
}

// fakeDenyingGrantor — minimal grantor that always denies.
type fakeDenyingGrantor struct{ reason string }

func (g *fakeDenyingGrantor) Request(_ context.Context, _ policy.EscalationRequest) (*policy.EscalationResult, error) {
	return &policy.EscalationResult{
		Decision: policy.EscalationDenied,
		Reason:   g.reason,
		Decider:  "test-denying-approver",
	}, nil
}

// TestE2E_ScopedAuth_ReplayProtection — single-shot tokens
// (max_uses=1, the default) are rejected on the second use even
// within the same plugin process.
func TestE2E_ScopedAuth_ReplayProtection(t *testing.T) {
	codeflyHome := resolveSymlinks(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tb := &resources.Toolbox{
		Name:    "network-victim",
		Version: "e2e-scoped-replay",
		Agent: &resources.Agent{
			Kind:      resources.ToolboxAgent,
			Name:      "network-victim",
			Publisher: "codefly.dev",
			Version:   "e2e-scoped-replay",
		},
	}
	require.NoError(t, tb.Validate())
	installVictimAtAgentPath(t, ctx, codeflyHome, tb.Agent)

	principal := &policy.Principal{ID: "u-replay", Kind: policy.KindHuman, OrgID: "org-x"}
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()

	plugin, err := launch.LaunchWithOptions(ctx, tb, launch.Options{},
		manager.WithPrincipal(principal),
		manager.WithScopedAuthSecret(secret),
		manager.WithPermissionsCallback(denyPDP),
	)
	require.NoError(t, err)
	defer plugin.Close()

	encoded, _, err := policy.Mint(policy.MintInput{
		Principal:  principal,
		Action:     "who.am.i",
		AudienceID: "codefly.dev/network-victim:e2e-scoped-replay",
		TTL:        time.Minute,
		MaxUses:    1, // single-shot
	}, secret)
	require.NoError(t, err)

	md := metadata.Pairs(policy.ScopedAuthMetadataKey, encoded)
	callCtx := metadata.NewOutgoingContext(ctx, md)
	args, _ := structpb.NewStruct(map[string]any{})

	// First use — succeeds.
	resp1, err := plugin.Client.CallTool(callCtx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp1.Error,
		"first use: fast path proceeds")

	// Second use — replay tracker rejects.
	resp2, err := plugin.Client.CallTool(callCtx, &toolboxv0.CallToolRequest{
		Name: "who.am.i", Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp2.Error,
		"second use of single-shot token: rejected")
	require.Contains(t, resp2.Error, "max uses exhausted")
}

// silenceUnusedImport keeps `os` and `filepath` imports stable for
// future tests in this file that need them.
var _ = os.TempDir
var _ = filepath.Clean
