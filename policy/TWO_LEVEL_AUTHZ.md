# Two-level defense-in-depth: scoped authorization tokens

**Status:** proposal (2026-05-10).
**Author audience:** codefly + saas-starter engineers.
**Decision required:** sign-off before implementation phase 1.

---

## TL;DR

Today's permission system has two enforcement points (Guard at
plugin → PDP via callback for sub-operations). This proposal adds
a **third, gateway-layer pre-evaluation** that mints a short-lived
**scoped authorization token** carrying the resolved decision. The
token rides on every plugin request and is verified inside the
plugin's Guard before it ever calls back to the PDP.

Three layers, three independent failures still get you safe-by-
default:

```
1. Gateway layer (NEW): tool policy evaluation + scoped token mint
2. Outer Guard at plugin: token verify (fast) OR PDP via callback (fallback)
3. Inline Authorizer: per-sub-operation checks via callback
```

The token travels in **gRPC metadata** (same channel as the
principal — no proto changes; transparent to plugin authors).

---

## Motivation

### What we have

| Layer | Where | When | Cost |
|---|---|---|---|
| **Outer Guard** | Plugin process (`policyguard.Guard`) | Every `CallTool` | Round-trip to host PDP via UDS |
| **Inline Authorizer** | Plugin handler code | When the author calls `Authorized()` | Round-trip to host PDP via UDS |

Both layers consult the same PDP through the same UDS callback.
Two issues:

1. **Latency**: every tool call pays a UDS round-trip, even for
   simple actions where the principal's role grant is well-known
   and stable.
2. **No pre-flight evaluation**: the gateway (Mind, codefly's CLI,
   the orchestration layer) sees every tool call go through it,
   but doesn't itself evaluate any policy. It just forwards.
3. **All-or-nothing trust**: if the plugin's Guard is bypassed
   (a bug, a misconfiguration), there's no fallback that the
   gateway already vetted.

### What we want

The gateway should evaluate the **tool's policy** (which can be
richer than just RBAC — rate limits, conditional logic, time
windows) and forward a **proof of evaluation** to the plugin. The
plugin verifies the proof and proceeds without re-evaluating —
unless the proof is missing/invalid, in which case it falls back
to the existing callback.

This is the standard **API Gateway → backend** pattern (AWS
APIGateway → Lambda, Kong → microservice, Envoy → upstream)
applied to codefly's host → plugin relationship.

### What this gives us

- **Defense in depth.** Three independent enforcement points;
  any one failing doesn't open a hole.
- **Performance.** Hot path: gateway evaluates once, mints a token
  reusable for short windows; plugin verifies signature locally
  (microseconds). No UDS round-trip on the hot path.
- **Per-call scoping.** Each token authorizes ONE specific
  (action, resource, principal, time window). Even a leaked token
  has minimal blast radius — it expires in seconds and only
  authorizes one action.
- **Pluggable tool policies.** Tool authors (or operators) can
  attach Cedar/Rego/custom rules to specific tools beyond what
  RBAC expresses — rate limits, business-hour windows, label-
  matching gates, etc. — and the policy lives at the gateway,
  not in plugin code.
- **Audit clarity.** Gateway logs every minted token (with the
  policy decision path); plugin logs every consumed token. Both
  correlate via the token's unique ID.

---

## The model

### Tool policy

A `ToolPolicy` is a function from `(Principal, Action, Resource,
Context) → Decision` evaluated at the gateway. Lives outside any
plugin process, configured by the operator.

Sources of tool policies (any combination, evaluated in order):

1. **Manifest-derived defaults.** Every plugin's manifest declares
   a `permissions` block; the gateway uses these as the baseline
   policy (action MUST be in the manifest). Already in place via
   M4's `CeilingPDP` — moved up to the gateway.

2. **Operator-supplied tool policies.** YAML or Cedar/Rego rules
   per tool. Example:

   ```yaml
   tool: github.merge_pr
   rules:
     - allow: true
       when:
         - "ci_status == 'green'"
         - "now().hour >= 9 AND now().hour < 18"
         - "rate(per_minute=5)"
     - require_approval: true
       when:
         - "labels contains 'force-merge'"
   ```

3. **Saas-starter role grants.** The existing RBAC check (M2's
   `Decide` RPC). Always runs as the final step — the gateway
   checks role grants before minting.

The gateway pipeline:

```
1. Load tool policy for (toolbox, tool)
2. Evaluate manifest declaration  (does the plugin DECLARE this action?)
3. Evaluate operator rules        (does the operator's tool policy ALLOW?)
4. Evaluate role grants           (does saas-starter grant the principal?)
5. If all pass: mint a ScopedAuthorization with computed caveats
6. Forward to plugin with the token in metadata
```

### Scoped authorization token

A short-lived bearer token that proves "this principal has been
authorized for this action on this resource for the next N
seconds, max M uses, with these caveats". Plugin verifies the
signature and matches the caveats against the actual call.

**Format**: HMAC-SHA256 over a canonical JSON envelope. Why not JWT
or Biscuit:

| Format | Why not for v1 |
|---|---|
| JWT | RS256 needs key distribution; HS256 is fine but lacks the M6 path |
| Biscuit | Excellent eventually (M6); overkill for v1 — Datalog complexity unwarranted for a simple time-bounded capability |
| Macaroon | Same reasoning as Biscuit; we'd just use 2-3 caveats here |

We use **HMAC-SHA256 over canonical JSON** for v1. Path to Biscuit
preserved by branching on the `format` field of the envelope —
v2-biscuit is additive when we need first-/third-party caveats.

```json
{
  "id": "ulid-01HXAB...",                      // unique per mint, for audit
  "fmt": "v1-hmac",                             // version tag
  "principal_id": "u-antoine",
  "principal_kind": "human",
  "principal_org_id": "org-codefly",
  "action": "github.merge_pr",
  "resource": "repo:codefly-dev/codefly.dev",
  "issued_at": 1715300000,                     // unix seconds
  "expires_at": 1715300120,                    // 120s window typical
  "max_uses": 1,                                // most actions are single-shot
  "caveats": {                                  // optional per-action constraints
    "ci_status": "green",
    "labels": ["auto-merge"]
  },
  "audience": "codefly.dev/github-bot:0.1.0"   // bound to a specific plugin
}
```

The HMAC is computed over the canonical (sorted-key, no-whitespace)
JSON encoding and appended as `<json>.<hmac>` for transport (same
shape as JWT; trivial to debug with `cut -d. -f1 | base64 -d`).

### How the token is passed

**Recommended: gRPC metadata header `x-codefly-scoped-authz`.**

This is the same channel we already use for `x-codefly-token`
(per-spawn auth) and `x-codefly-principal` (principal claim). Two
existing precedents; one new header. Properties:

- ✅ **No proto changes.** Every existing RPC works unchanged.
- ✅ **Plugin authors never see it.** The interceptor strips it
  from metadata, makes it available via context. Plugin handlers
  read `policy.PrincipalFrom(ctx)` and `policy.ScopedAuthFrom(ctx)`
  if they want; otherwise it's invisible plumbing.
- ✅ **Survives any gRPC client → server hop.** If we add a Mind
  → gateway → plugin chain, metadata propagates through standard
  interceptors.
- ✅ **Easy to debug.** `grpcurl --metadata` flag works.
- ✅ **Future-proof.** When we move to Biscuit (M6), only the
  encode/verify changes; the wire stays metadata.

**Alternative considered: proto-level common envelope.**

```proto
message RequestEnvelope {
  ScopedAuthorization scoped_auth = 1;
  google.protobuf.Any payload = 2;
}
service Toolbox {
  rpc CallTool(RequestEnvelope) returns (RequestEnvelope);  // !
}
```

Rejected for v1:

- ❌ Every RPC signature changes — every plugin's handlers do too.
- ❌ Proto-level wrapping forces explicit unwrap-then-dispatch in
  every server. Verbose and error-prone.
- ❌ Doesn't compose well with grpc-gateway / Connect transcoders;
  REST clients would have to manually wrap/unwrap.
- ❌ Generates inconsistent SDKs across languages.
- ❌ Migration is invasive (every existing plugin breaks).

Metadata gives us the same property (auth on every call) with
zero proto churn and no plugin-author surface change.

**Hybrid escape hatch.** For tools where the resource is too
large for metadata (>4KB header limits) or where the policy needs
the request body to evaluate, the gateway can:

1. Read the body once,
2. Evaluate policy on it,
3. Mint a token with a `body_hash` caveat,
4. Forward request + token,
5. Plugin verifies the body hash matches what it received.

This keeps metadata-only on the hot path and only special-cases
high-payload tools. Document as a future extension; not in v1.

---

## Lifecycle: Mint → Forward → Verify

### 1. Gateway mints

```go
// In Mind/gateway code, before forwarding the CallTool to a plugin:
sa, err := evaluator.EvaluateAndMint(ctx, EvaluationInput{
    Principal:  antoineAsPrincipal,
    Toolbox:    "codefly.dev/github-bot:0.1.0",
    Tool:       "github.merge_pr",
    Resource:   "repo:codefly-dev/codefly.dev",
    Context:    map[string]any{"ci_status": "green", "labels": []string{"auto-merge"}},
})
if err != nil {
    // Tool policy denied or backend unreachable; return to caller
    return nil, err
}

// sa is a *ScopedAuthorization carrying the policy verdict.
md := metadata.Pairs("x-codefly-scoped-authz", sa.Encode())
ctx = metadata.NewOutgoingContext(ctx, md)

resp, err := pluginClient.CallTool(ctx, req)
```

The evaluator:

1. Loads the `ToolPolicy` for the tool (manifest + operator rules).
2. Evaluates rules against the input context.
3. Calls saas-starter `Decide` for the role-grant check (the
   existing M2 RPC).
4. If all pass, mints a `ScopedAuthorization` and signs with the
   per-spawn HMAC secret.
5. Returns the encoded token.

### 2. Plugin verifies

In `policyguard.Guard.CallTool`:

```go
// Pseudocode:
func (g *Guard) CallTool(ctx, req) (resp, err) {
    if token := scopedAuthFromMetadata(ctx); token != "" {
        sa, verr := g.verifier.Verify(token,
            ExpectedAction(req.Name),
            ExpectedAudience(g.toolboxIdentity),
            ExpectedClock(time.Now()),
        )
        if verr == nil && sa != nil {
            // Stamp the scoped auth on ctx for downstream visibility.
            ctx = policy.WithScopedAuth(ctx, sa)
            // Trust the gateway's decision; skip the PDP callback.
            return g.inner.CallTool(ctx, req)
        }
        // Token present but invalid — log + fall through to PDP.
        // Don't outright deny: the gateway might have a bug; the PDP
        // is the SECOND defense layer that should catch it.
        wool.Get(ctx).Warn("scoped-authz invalid; falling back to PDP",
            wool.Field("error", verr.Error()))
    }
    // No token, or invalid token — fall back to the existing PDP path.
    return g.evaluateViaPDP(ctx, req)
}
```

Verification is local and fast (<1ms): HMAC compare + JSON parse +
caveat checks. The plugin **doesn't trust** the token without
verification; even though the host generated it, an attacker that
got into the request path (e.g., a man-in-the-middle on a non-UDS
transport) could try to forge.

### 3. Caveat enforcement

Token caveats are checked at verify time:

| Caveat | Check |
|---|---|
| `expires_at` | `now < expires_at` (with small clock skew tolerance) |
| `max_uses` | Plugin tracks per-token-id usage; reject when exhausted |
| `audience` | Matches the verifying plugin's identity |
| `action` | Matches `req.Name` exactly |
| `resource` | Matches the call's resource (request-specific) |
| `principal_id` | Matches the principal stamped on ctx |
| Custom (`ci_status`, `labels`, `ip_range`, ...) | Plugin or framework checks via registered caveat verifiers |

If any caveat fails, the token is rejected and the plugin falls
back to the PDP path.

### Token tracking for max_uses

`max_uses=1` is the safe default. Tracking is per-token-id in an
in-memory LRU on the plugin side. A token's ID is unique; replay
detection is "have I seen this id before, and is it within
expiry?".

For higher max_uses, the same LRU records a counter per id.
Plugin restart resets the LRU — that's acceptable: the token's
expiry bounds the worst case, and a restart-induced re-use would
still need a valid-signature, in-window token.

---

## Composition with existing layers

The two-level proposal layers ON TOP of what we have, never
replacing:

```
                   ┌─────────────────────────────────────┐
                   │ Gateway evaluator (NEW)             │
                   │ - Tool policy (manifest + rules)    │
                   │ - Role grants via saas-starter      │
                   │ - Mint ScopedAuthorization          │
                   └─────────────────────────────────────┘
                                   │
                                   │ x-codefly-scoped-authz (metadata)
                                   ▼
                   ┌─────────────────────────────────────┐
                   │ Outer Guard (plugin)                │
                   │                                     │
                   │ Token? Verify → trust → handler.    │
                   │ Else: fall back to PDP via callback.│
                   └─────────────────────────────────────┘
                                   │
                                   ▼
                          plugin handler runs
                                   │
                                   │ optional, for sub-operations
                                   ▼
                   ┌─────────────────────────────────────┐
                   │ Inline Authorizer                   │
                   │ policy.AuthorizerFromContext(ctx).  │
                   │   Authorized(action, resource)      │
                   │ — UDS callback to host PDP           │
                   └─────────────────────────────────────┘
```

When does each layer run?

| Scenario | Gateway evaluator | Outer Guard | Inline Authorizer |
|---|---|---|---|
| User invokes via Mind, hot path | ✅ mints token | ✅ verifies token (fast) | If plugin asks |
| User invokes via Mind, gateway eval errors | (returns error) | (call never sent) | — |
| Direct host call without gateway | — | ✅ falls back to PDP | If plugin asks |
| Forged token | (didn't mint) | ✅ verify fails → PDP | If plugin asks |
| Token expired mid-call | (didn't mint) | ✅ verify rejects → PDP | If plugin asks |
| Compromised plugin | (already vetted) | ✅ token bounds blast radius | (also bounded) |

The defense-in-depth property: any single layer's failure leaves
the others enforcing. A buggy gateway can't bypass; a buggy plugin
can't escalate.

---

## Security analysis

### What this defends against

- **Plugin compromise.** A plugin that bypasses its own Guard
  still can't act outside the token's caveats — the token was
  scoped at the gateway. Without a token, the plugin's Guard
  falls back to the PDP, which still gates.
- **Replay.** Tokens have unique IDs + max_uses + expiry. Replays
  past expiry fail; replays within expiry past max_uses fail.
- **Token leak.** Tokens are short-lived (typically 60-300s) and
  scoped to one (action, resource). A leaked token gives an
  attacker minimal authority for a brief window — vs a leaked
  long-lived credential which is a full compromise.
- **Cross-tool reuse.** Tokens have an `audience` field bound to
  one plugin's canonical identity. Tokens minted for plugin A
  fail verification at plugin B.
- **Gateway compromise.** A compromised gateway can mint
  fraudulent tokens, but the audit log (gateway-side) records
  every mint with its policy decision path. Anomaly detection on
  the audit stream catches "gateway minted 1000 tokens for
  unusual actions".

### What this does NOT defend against

- **Compromised host signing key.** The HMAC secret is shared
  between gateway and plugin; if both are owned, tokens can be
  forged. Mitigation: per-spawn secret rotation; treat the secret
  as session-bound, regenerate on each plugin spawn.
- **Time skew.** Plugin and gateway must agree on time within a
  small skew window (e.g., 30s). NTP keeps this tight in practice.
  Tokens beyond skew tolerance are rejected.
- **Saas-starter compromise.** If the auth backend is owned, all
  bets are off — that's the trust root.

### Threat model summary

| Adversary | Defense |
|---|---|
| Buggy plugin | Gateway-pre-vetted token; max_uses bounds; per-call scope |
| Buggy gateway | Plugin's PDP fallback enforces if token absent/invalid |
| Network attacker reading metadata | Token is short-lived + scoped; UDS transport limits visibility |
| Network attacker injecting tokens | HMAC signature; per-spawn secret; audience binding |
| Replay | Unique token id + max_uses + expiry |
| Long-running compromise | Tokens expire in seconds; secrets rotate per spawn |

---

## Implementation phases

### Phase 1 — Foundation (this PR / next session)

- `policy.ScopedAuthorization` type + `Encode` / `Verify` (HMAC-SHA256)
- `policy.ToolPolicy` interface + manifest-derived default
- `policy.GatewayEvaluator` — composes ToolPolicy + Decider, mints
- `manager.WithScopedAuthSecret` LoadOption (or auto-generate)
- `policyguard.Guard` learns to verify scoped tokens (with fallback)
- `x-codefly-scoped-authz` metadata header
- Unit tests + 2-3 E2E tests

### Phase 2 — Tool-policy DSL ✅ DONE

- YAML schema for operator-supplied tool policies (yaml_tool_policy.go)
- Built-in caveats: time_window, rate_limit, allowlist (caveats.go)
- Cedar/OPA bridge interface (cedar.go — ExternalToolPolicy +
  ExternalPolicyEvaluator)

**YAML schema example:**

```yaml
tools:
  - id: codefly.dev/github-bot:0.1.0:github.merge_pr
    allow: true
    ttl: 60s
    max_uses: 1
    caveats:
      time_window:
        start_hour: 9
        end_hour: 17
        timezone: America/New_York
        days_of_week: [mon, tue, wed, thu, fri]
      rate_limit:
        per_minute: 5
        scope: principal       # or principal_org / global
      allowlist:
        context_key: ci_status
        allowed: [green, success]

  - id: codefly.dev/github-bot:0.1.0:github.force_push
    deny: true
    reason: "force-push requires manual approval (M7+)"

  - id: codefly.dev/github-bot:0.1.0:*
    allow: true
    ttl: 120s
```

Operators ship this YAML alongside their gateway config.
`policy.ParseYAMLToolPolicies(yamlBytes)` returns
`map[string]ToolPolicy` ready to drop into
`GatewayEvaluator.ToolPolicies`.

**Built-in caveats:**

- **time_window**: only allow during specified hours / days /
  timezone. Snapshots the spec into the token; plugin
  re-checks current time against the snapshot (catches gateway
  clock drift).
- **rate_limit**: sliding-window token bucket per
  (principal, tool) — or wider scope. Mint-time only; no
  caveat travels in the token. Stateful at the gateway.
- **allowlist**: generic list-membership check. Operator
  configures `context_key` + `allowed` set. Both mint-time
  precheck AND verify-time snapshot check. `match_mode: equals`
  (default) or `any_of_list` (context value is a list, any
  element matches).

**External policy engines:** `policy.ExternalPolicyEvaluator` is
a one-method interface (`Evaluate(ctx, EvaluationInput) →
ExternalDecision`). Wrap a Cedar bundle, OPA Rego query, or any
custom engine; pass to `policy.ExternalToolPolicy{Evaluator: x}`
to get a `ToolPolicy` for `GatewayEvaluator`.

Core has zero dependency on Cedar/OPA — operators bring their own
engine.


### Phase 3 — Approval flow integration (M7) ✅ DONE (core side)

Core ships the SDK + interfaces; saas-starter implements the
RPCs and approval UI per `MIND_INTEGRATION_M7.md`.

- `policy.EscalationRequest` / `EscalationResult` types
- `policy.EscalationGrantor` interface — saas-starter implements
- `policy.RequestEscalation(ctx, req)` SDK helper — plugins call this
  to escalate; returns ctx with scoped-auth token attached
- `policy.AuthorizedOrEscalate(ctx, authorizer, action, resource, justification)` —
  ergonomic wrapper that tries authz first and escalates on deny
- `policy.SetGlobalEscalationGrantor(g)` — host wires the grantor
  at startup; SDK helpers find it via the global registry
- `via_approval` caveat baked into approved-escalation tokens
  for audit (saas-starter side; convention documented)
- E2E tests: full flow with fake grantor approving + denying
- Saas-starter migration `38_create_delegation_grants.up.sql`
  with NOTIFY trigger for instant streaming wake-up

**Saas-starter pending** (see MIND_INTEGRATION_M7.md):
- `RequestDelegation` / `WaitForDelegation` (server-stream) /
  `DecideDelegation` RPCs
- `SaasStarterEscalationGrantor` adapter in the host binary
- Approval UI surface in saas-starter frontend
- Notification fan-out (Slack / email / in-app) on pending insert

### Phase 4 — Public-key signing (M6) ✅ DONE (ed25519)

- `v2-ed25519` format alongside `v1-hmac` (`scoped_auth_v2.go`)
- `MintEd25519(input, privKey)` / `VerifyEd25519(token, expect, pubKey)`
- `TokenVerifier` type for dual-format dispatch + key rotation
  (multiple public keys; first match wins)
- Public-key signing removes the shared-secret distribution
  problem: the gateway holds the PRIVATE key; plugins hold the
  PUBLIC key (distributable freely)
- Migration path: hosts can mint either format during transition;
  plugins with TokenVerifier configured for both verify either

**Why ed25519 instead of full Biscuit.** Biscuit's load-bearing
benefit was public-key signing; we get that with ed25519 (32-byte
public, 64-byte private, ~50µs verify, standard library). Full
Biscuit's Datalog caveats and offline attenuation are deferred —
add `v3-biscuit` via the same fmt-tag dispatch when there's a
specific need.

### Phase 5 — Hardening (M10) ✅ DONE (core)

`core/policy/hardening.go`:

- **Token Revocation List (TRL)**: `TokenRevocationList` type
  with concurrent-safe Add/Remove/Replace/IsRevoked. Plugin's
  Guard checks TRL alongside replay tracking. Operator
  populates programmatically (Postgres NOTIFY hook) or via
  file-backed reload.
- **Break-glass override**: `CODEFLY_BREAK_GLASS_JUSTIFICATION`
  env var. When set with a non-empty justification, the Guard
  bypasses PDP for that ONE process and emits WARN audit logs
  on every CallTool. Used for incident response when normal
  authz is itself the problem.
- **Recursion depth caps**: Default 3 hops in
  Principal.DelegationChain. Override via
  `CODEFLY_MAX_DELEGATION_DEPTH`. Tokens whose chain exceeds
  the cap are rejected at verify time. Defends against
  pathological multi-agent escalation patterns.

### Phase 5b — M8 audit caveats (`via_approval`, `via_pattern`)

Registered as built-in caveats in core. Saas-starter mints these
into tokens to mark M7 (escalation-approved) or M8 (pattern-
matched) provenance. Plugin-side verifiers accept any value (the
signature already proved authenticity); audit logs capture the
caveats for the delegation-chain trail.

NOT declarable in YAML — minted directly by the saas-starter
backend. Attempting to declare in YAML rejects loudly so
operators don't think they're configuring something.

---

## Open decisions before phase 1

1. **Token id format.** ULID gives time-ordered, audit-friendly
   IDs. Alternative: random UUID v4. **Recommendation: ULID.**

2. **Default expiry window.** Short = safe but more gateway
   round-trips. Long = perf win but bigger blast radius on leak.
   **Recommendation: 120 seconds default; configurable per tool
   policy.**

3. **Default max_uses.** 1 is safest for most actions. Some tools
   genuinely need batch (e.g., bulk read). **Recommendation: 1
   default; tools opt up via tool policy.**

4. **Clock skew tolerance.** Seconds vs minutes. **Recommendation:
   30 seconds.** NTP keeps clocks tighter than this in practice;
   minutes is too loose.

5. **Per-spawn vs per-host signing secret.** Per-spawn means a
   leaked secret only ever forges tokens for one plugin spawn
   (which dies on close). Per-host is simpler but riskier.
   **Recommendation: per-spawn.** Generated in `manager.Load`,
   bound to AgentConn lifetime. We already have this pattern with
   `CODEFLY_AGENT_TOKEN`.

6. **Fallback policy when token verify fails.** Two options:
   (a) deny outright, (b) fall through to PDP. **Recommendation:
   (b) fall through with a WARN log.** A misconfigured gateway
   shouldn't break the system; the PDP is the second defense
   layer that should catch any actual abuse.

7. **Metadata header name.** Recommendation: `x-codefly-scoped-authz`
   (consistent with `x-codefly-token`, `x-codefly-principal`).

---

## Backwards compatibility

- Plugins without scoped-authz support: their Guard never sees the
  metadata; falls back to existing PDP path. **Zero migration
  required.**
- Hosts without gateway evaluator: simply don't set the metadata.
  Plugin's Guard falls back to PDP path. **Zero migration
  required.**
- Existing tests: pass unchanged (they don't set the metadata).
- New tests: layered explicitly.

This is purely additive. We can ship phase 1 without touching any
existing plugin or host code.

---

## What plugin authors need to do

**Nothing.** The metadata is plumbing — invisible to plugin code.
A plugin author who never opens this file will:

- Get the gateway-evaluated token transparently when invoked from
  Mind/the gateway
- Get the PDP-via-callback fallback transparently when invoked
  directly
- Optionally read `policy.ScopedAuthFrom(ctx)` if they want to
  see the gateway's decision path or token id (for audit logging)
  — same way they read `policy.PrincipalFrom(ctx)`

**Plugin manifest declarations are unchanged.** `permissions:`
block still declares the ceiling. The gateway's tool policy is
separate (operator concern).

---

## What host engineers need to do

After phase 1 ships:

```go
// Existing wiring:
agentConn, err := manager.Load(ctx, agent,
    manager.WithSandbox(sb),
    manager.WithPrincipal(p),
    manager.WithPermissionsCallback(decider),
    manager.WithScopedAuthSecret(secret), // NEW; auto-generated if omitted
)

// At gateway (Mind, codefly's gateway):
evaluator := policy.NewGatewayEvaluator(toolPolicies, decider, secret, metrics)

// Per call:
sa, err := evaluator.EvaluateAndMint(ctx, policy.EvaluationInput{
    Principal: principal,
    Toolbox:   "codefly.dev/github-bot:0.1.0",
    Tool:      "github.merge_pr",
    Resource:  "repo:codefly-dev/codefly.dev",
    Context:   contextFromRequest,
})
if err != nil {
    return errFromPolicyDecision(err)
}
ctx = sa.AttachToOutgoingContext(ctx)
client.CallTool(ctx, req)
```

Three new lines per gateway call. Plus the operator wires up their
`ToolPolicy` registry (one-time setup).

---

## Summary

This proposal extends the codefly permission system with a third
defense layer at the gateway, carried via a short-lived signed
token in gRPC metadata. It's:

- **Additive.** No existing code changes shape; gracefully falls
  back when not deployed.
- **Performance-positive.** Fast path (token verify) avoids the
  PDP round-trip; fallback (PDP) preserves correctness.
- **Defense-in-depth.** Three independent layers; any one's
  failure doesn't open holes.
- **Plugin-author-invisible.** Metadata is plumbing; plugin code
  unchanged.
- **Auditable.** Every minted token has a unique id; gateway log
  + plugin log correlate.
- **Cleanly composable** with M6 (Biscuit), M7 (escalation), M9
  (audit UI).

Ship phase 1; let it bake; layer M6's Biscuit format in next.
