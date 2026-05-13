// Package policy is the codefly permission and authority layer.
//
// =============================================================
// THREE LAYERS, THREE CONCERNS, ENFORCED INDEPENDENTLY
// =============================================================
//
//   - CAPACITY (sandbox)       — what bytes/syscalls a binary CAN
//     touch at the kernel level. Lives
//     in core/runners/sandbox; applied
//     at manager.Load via the sandbox
//     wrap. Independent of authority.
//
//   - AUTHORITY (this package) — what business actions a Principal
//     IS ALLOWED to perform. Lives
//     in this package + saas-starter.
//     Enforced by the PDP at every
//     tool call.
//
//   - ACCOUNTABILITY (audit)   — who did what under whose
//     authority. Cross-cuts both
//     layers. saas-starter audit_events
//     and delegation_chain in tokens.
//
// All three travel via the same Principal-bearing Biscuit-or-JWT
// token. Capacity is enforced by the OS regardless of authority.
// Authority is checked by the PDP. Audit logs the full chain.
// Three independent failures still get you defense in depth.
//
// =============================================================
// THE PIECES IN THIS PACKAGE
// =============================================================
//
// **Identity:**
//
//   - Principal (principal.go)
//     Unified type for humans, services, and agents. Carries ID,
//     Kind, OrgID, AgentID, optional DelegationChain, the verified
//     credential. Validated at construction; immutable.
//
//   - WithPrincipal / PrincipalFrom (principal.go)
//     Context helpers: stamp a Principal on a ctx, retrieve it
//     downstream. The gRPC interceptor (in core/agents) is the
//     standard stamper; handlers read.
//
//   - EncodePrincipalToken / DecodePrincipalToken (principal_token.go)
//     The wire format. Today: base64url(JSON) v1-unsigned. M6+:
//     Biscuit. Plugin authors never call these directly — the
//     manager + interceptor handle the round-trip.
//
// **Authority manifest:**
//
//   - PermissionPolicy (permissions.go)
//     The plugin manifest's declaration: which actions on which
//     resources the plugin CAN perform. The PDP intersects this
//     with role grants — the manifest is the CEILING.
//
//   - SandboxPolicy (permissions.go)
//     The plugin manifest's CAPACITY declaration: read paths,
//     write paths, network policy. Applied via sandbox.Wrap at
//     manager.Load. Independent of PermissionPolicy.
//
// **Decision interfaces — three audiences, three shapes:**
//
//   - Decider (decider.go) — host-facing. Identical to PDP;
//     hosts (Mind, gateway, CLI) construct and pass these. Full
//     PDPRequest / PDPDecision shape with caveats and delegation
//     proofs. This is the interface that gets wired through
//     manager.Load via WithPermissionsCallback.
//
//   - Authorizer (decider.go) — plugin-facing. Simple `Authorized
//     (ctx, action, resource) → (bool, reason, err)` shape. Plugin
//     authors call this for fine-grained sub-operation gating
//     INSIDE a tool (the outer call is already authorized by the
//     Guard). Implementation is a UDS callback to the host.
//
//   - PermissionsBackend (decider.go) — saas-starter-facing. The
//     thinnest interface a saas-starter gRPC client must satisfy
//     to plug into SaasPDP. Pulls the dependency arrow one-way:
//     core has no compile-time tie to saas-starter; saas-starter
//     wires itself in via this interface.
//
// **PDP — the decision engine:**
//
//   - PDP interface (pdp.go)
//     Evaluate(ctx, *PDPRequest) → PDPDecision. The single point
//     of authorization. policyguard.Guard wraps a Toolbox to
//     consult the PDP on every CallTool.
//
//   - AllowAllPDP / DenyAllPDP / JSONPDP (pdp.go)
//     Built-ins for development, tests, and simple production
//     allow-lists.
//
//   - SaasPDP (pdp_saas.go)
//     Production Decider. Adapts a PermissionsBackend (saas-
//     starter or any other policy store) into the PDP interface.
//     Adds: positive-decision LRU cache (configurable TTL,
//     never caches denies), fail-closed-on-backend-error,
//     observability via PDPMetrics + RecordDecision.
//
//   - CeilingPDP (pdp_ceiling.go)
//     Wraps an inner PDP, intersects with the plugin's manifest
//     PermissionPolicy. First gate — fail fast on undeclared
//     actions before round-tripping to saas-starter.
//
//   - ShadowPDP (pdp_shadow.go)
//     Wraps an inner PDP, ALWAYS allows but records the inner
//     decision via observability. Used during M5 rollout to
//     surface "what would have been denied" before flipping to
//     enforce.
//
//   - BuildPDP / ResolvePDPMode (pdp_mode.go)
//     The standard composition of the above, driven by
//     CODEFLY_PDP_MODE (off / shadow / enforce). Hosts call
//     these at startup.
//
// **Gateway-layer pre-evaluation (gateway.go + scoped_auth.go):**
//
// The host (Mind, codefly's gateway, CLI) evaluates a tool policy
// BEFORE forwarding the call to the plugin. On allow, mints a
// short-lived signed token (ScopedAuthorization) carrying the
// resolved decision. The token rides on outgoing gRPC metadata
// (`x-codefly-scoped-authz`) and is verified by the plugin's
// Guard before invoking inner toolbox handlers.
//
//   - GatewayEvaluator (gateway.go) — host-side composer of
//     ToolPolicy + Decider, mints tokens on allow.
//   - ToolPolicy (gateway.go) — pluggable per-tool rule type.
//     Built-in: AllowAlwaysToolPolicy, DenyAlwaysToolPolicy,
//     ManifestCeilingPolicy. Operators implement custom for
//     Cedar/Rego/business-specific rules.
//   - ScopedAuthorization (scoped_auth.go) — the token type:
//     time-bounded, use-bounded, audience-bound, signed via
//     HMAC-SHA256 with a per-spawn secret.
//   - Mint / Verify (scoped_auth.go) — HMAC-signed JSON envelope.
//   - ReplayTracker (scoped_auth.go) — plugin-side per-spawn LRU
//     enforcing MaxUses across calls.
//
// **Two-level defense:** Guard tries the fast path (verify token)
// first; on missing/invalid token, falls back to the full PDP
// path. Three independent layers: gateway pre-evaluation, plugin
// outer Guard, inline Authorizer for sub-operations. Any single
// layer's failure leaves the others enforcing.
//
// See TWO_LEVEL_AUTHZ.md for the design rationale and security
// analysis.
//
// **Token formats — v1-hmac and v2-ed25519:**
//
//   - `Mint(input, secret)` / `Verify(token, expect, secret)` —
//     v1-hmac (HMAC-SHA256). Shared secret between gateway and
//     plugin. Best for single-host setups.
//
//   - `MintEd25519(input, privateKey)` /
//     `VerifyEd25519(token, expect, publicKey)` — v2-ed25519
//     (public-key signing). Gateway holds the private key;
//     plugins hold the public key. No secret distribution.
//
//   - `TokenVerifier` (`scoped_auth_v2.go`) — dual-format
//     dispatch by `fmt:` tag. Supports key rotation (multiple
//     public keys; first match wins).
//
// **Hardening (hardening.go):**
//
//   - `TokenRevocationList` — invalidate compromised tokens
//     before expiry. Concurrent-safe; supports bulk Replace
//     for file-backed reloads.
//   - Break-glass: `CODEFLY_BREAK_GLASS_JUSTIFICATION` env var
//     bypasses PDP for incident response with mandatory
//     WARN-level audit on every call.
//   - Recursion depth caps:
//     `CODEFLY_MAX_DELEGATION_DEPTH` (default 3) bounds
//     delegation-chain length to defend against pathological
//     multi-hop escalation patterns.
//
// **M7 escalation (escalation.go):**
//
//   - `EscalationGrantor` interface — saas-starter implements,
//     core defines. Routes escalation requests through Slack/
//     email/UI approval pipeline.
//   - `RequestEscalation(ctx, req)` — SDK helper plugins call
//     when authority is denied; returns ctx with elevated token
//     attached.
//   - `AuthorizedOrEscalate(ctx, authorizer, action, resource,
//     justification)` — ergonomic wrapper.
//
// **M8 pattern grants** — `via_pattern` audit caveat marks
// tokens minted via auto-approval pattern matches at the
// saas-starter side.
//
// **Plugin → host callback channel (callback.go):**
//
// Plugin authors invoke `policy.AuthorizerFromContext(ctx).
// Authorized(ctx, action, resource)` for inline permission checks
// inside a tool handler. The Authorizer is a UDS-backed HTTP
// client that calls the host's PermissionsCallbackServer. Wiring:
//
//   - Host: manager.Load(WithPermissionsCallback(decider)) creates
//     the server (UDS, 0600 file perms), sets the path in the
//     plugin's CODEFLY_PERMISSIONS_SOCKET env, binds the spawn-
//     time Principal as the trusted subject. Close shuts the
//     server and removes the socket file.
//
//   - Plugin: agents.Serve's principal interceptor stamps the
//     Authorizer on every request ctx (process-singleton, lazy-
//     constructed from env). Without a callback socket, it's
//     the disabledAuthorizer that fails closed with a clear
//     reason on every call.
//
// **Why a callback channel rather than running the PDP in-process
// in the plugin.** Plugins must NOT depend on saas-starter's gen
// client (one-way dependency arrow). Permission state is mutable
// (revocations must be visible immediately); centralizing the
// PDP cache + metrics in the host is the only place that property
// is reasoned about cleanly.
//
// **Security property: principal binding cannot be impersonated.**
// The PermissionsCallbackServer uses a principalProvider closure
// that returns the spawn-time Principal. The plugin's request
// body's principal_id is IGNORED — even a compromised plugin
// cannot escalate by claiming a different principal. End-to-end
// test: TestE2E_Authorizer_PluginCannotImpersonate.
//
// **Observability:**
//
//   - PDPMetrics (observability.go)
//     Atomic counters for decisions (allow/deny/require_approval/
//     fail_closed/cache_hits) plus mean latency. Read via
//     Snapshot(). Operators wire this into Prometheus / OTEL.
//
//   - RecordDecision (observability.go)
//     Single entry point: bumps the counters, logs the decision
//     via wool with structured fields. Every PDP wrapper calls
//     it; one place to instrument.
//
// =============================================================
// HOW DECISIONS FLOW
// =============================================================
//
// On every plugin tool call:
//
//  1. host (manager.Load) spawns plugin with WithPrincipal(p)
//     - mints CODEFLY_PRINCIPAL_TOKEN (encoded p)
//     - mints CODEFLY_AGENT_TOKEN (process binding, separate)
//     - sandbox.Wrap applies if WithSandbox set
//
//  2. plugin process starts; agents.Serve registers gRPC server
//     - chains: auth → principal → rpcStats interceptors
//     - if PluginRegistration.PDP != nil: wraps Toolbox with
//     policyguard.Guard
//
//  3. host calls plugin.CallTool(ctx, request)
//     - principalUnaryInterceptor extracts token from env or
//     metadata, decodes, validates, stamps via WithPrincipal
//     - Guard receives the call, builds a PDPRequest
//     (Toolbox + Tool + Args + Identity from principal)
//     - PDP stack evaluates:
//     a. CeilingPDP checks manifest.Allows(action, resource)
//     - undeclared → deny; inner never consulted
//     b. inner PDP (e.g. SaasPDP) checks role grants
//     c. ShadowPDP (if mode=shadow) records but always allows
//     - allow → handler runs with Principal on ctx
//     - deny → CallToolResponse{Error: reason}, handler skipped
//
//  4. handler reads PrincipalFrom(ctx) for audit / branching
//     - NEVER for authorization decisions (PDP did that)
//
// =============================================================
// PLUGIN AUTHOR SURFACE — three roles, no security code
// =============================================================
//
// A plugin author's interaction with the permission system:
//
//   - DECLARE: edit toolbox.codefly.yaml's `permissions:` block.
//     This is reviewed by the user at install time; this is the
//     ceiling.
//
//   - READ: optionally call policy.PrincipalFrom(ctx) inside a
//     tool handler — for audit fields, display names, or to
//     decide what to RETURN (e.g. filter data the principal
//     "owns"). Never to gate "may this principal perform this
//     action" — that's the PDP.
//
//   - REQUEST (M7+): if the plugin's role grant doesn't cover
//     a needed action, call host.RequestEscalation to ask a
//     grantor for temporary delegated authority. The host
//     handles the user notification and approval flow.
//
// Plugin authors do NOT:
//   - implement a PDP
//   - check permissions in handler code
//   - construct or verify principal tokens
//   - manage role grants or audit logs
//
// See PLUGIN_AUTHORS.md (in this directory) for the practical
// guide with code examples.
//
// =============================================================
// BASH-AST + CANONICAL REGISTRY (orthogonal layer)
// =============================================================
//
// CanonicalRegistry (canonical.go) is a separate enforcement
// layer for plugins that exec shell commands. It maps a binary
// name (e.g. "git") to its canonical toolbox owner so a plugin's
// `bash -c "git push"` is refused with "use the git toolbox
// instead". The bash AST parser splits on &&/||/;/| and evaluates
// each command, defeating the canonical chained-binary bypass.
//
// This is the OTHER half of "defense in depth": the PDP gates
// declared tool calls; the CanonicalRegistry gates ad-hoc shell
// invocations.
//
// =============================================================
// EXTENDING THE STACK
// =============================================================
//
// Add a new PDP wrapper: implement PDP.Evaluate, add a
// constructor that takes an inner PDP, layer it in BuildPDP.
//
// Add a new caveat: extend the Biscuit verification path (M6+).
// Until then, hand-validate fields you care about in the
// inner PDP's Evaluate.
//
// Add a new principal kind: it's already extensible — the
// principals.kind CHECK constraint in saas-starter governs
// what's accepted. Update both ends.
//
// =============================================================
// WHAT'S IN saas-starter, NOT HERE
// =============================================================
//
// The role-assignment data, audit_events, delegation_grants
// (M7+), and the actual principal CRUD live in saas-starter's
// principals/role tables. core/policy is the codefly-side wire +
// enforcement; saas-starter is the source of truth.
//
// The bridge is the PermissionDecider interface (pdp_saas.go,
// when implemented) — keeps core decoupled from saas-starter's
// generated client. Hosts wire a concrete client at startup.
package policy
