# Wiring Mind (or any host) into the codefly permission system

This document is for engineers building a **host** that spawns
codefly plugins — Mind, the codefly gateway, the CLI, or any
other orchestrator. It explains the interfaces you implement and
the wires you connect.

For plugin authors, see PLUGIN_AUTHORS.md.

## What a "host" is

A host is any process that opens `session.ToolboxSession` instances to use
plugins. The host owns:

- **Principal resolution** — figuring out who's making each request
  (user-driven via session JWT, agent-driven via service principal)
- **Decider construction** — wiring saas-starter's permission RPCs
  into the codefly `policy.Decider` interface
- **Session lifecycle** — one process and authority envelope per principal,
  tenant, and job/session
- **Outer tool authorization** — supplying the host `policy.Decider`; the
  supported session API owns guarded launch, catalog/request binding, and
  single-use authorization minting
- **Audit sink** — persisting the redacted lifecycle events emitted by the
  session

## Supported host API

External hosts should use `core/toolbox/session`, not compose raw
`manager.LoadOption` values. `ToolboxSession` validates production admission,
launches through a private per-spawn UDS directory, discovers identity and
summaries, describes the selected tool on demand, evaluates host policy, mints
an authorization bound to the exact descriptor and request digests, invokes,
audits, and cleans up.

The CLI-independent proof harness is importable from
`core/toolbox/conformance/host`:

```go
proof, err := conformancehost.RunIdentityProof(ctx,
    conformancehost.IdentityProofOptions{
        Session: session.Options{
            Manifest:  manifest,
            Principal: principal,
            Decider:   decider,
            Scope: session.Scope{
                TenantID: tenantID,
                Environment: environment,
                ReleaseID: releaseID,
            },
        },
        RequestID: requestID,
        ObjectiveID: objectiveID,
        TaskID: taskID,
    })
```

The harness and session never expose the scoped token or spawn secret. Audit
events carry correlation IDs and digests, not raw arguments/results or tenant
authority.

## Authorization interfaces beneath the session

`ToolboxSession` composes these interfaces. Host implementers normally provide
only a `Decider` (often backed by `PermissionsBackend`) and an `AuditSink`;
the remaining wiring is documented here for core maintainers.

### 1. `policy.PermissionsBackend` — adapt your auth backend

This is the THINNEST interface that bridges core/policy to your
authorization store (saas-starter, OPA, Cedar, custom, etc.).

```go
type PermissionsBackend interface {
    Decide(ctx context.Context,
        principalID, action, resource, orgID, scope string,
    ) (allowed bool, reason, decisionPath string, err error)
}
```

For saas-starter, the implementation calls
`PermissionService.Decide` (the M2 RPC) and unpacks the response:

```go
type SaasStarterBackend struct {
    Client api.PermissionServiceClient
}

func (b *SaasStarterBackend) Decide(ctx context.Context,
    principalID, action, resource, orgID, scope string,
) (bool, string, string, error) {
    resp, err := b.Client.Decide(ctx, &api.DecideRequest{
        PrincipalId: principalID,
        Action:      action,
        Resource:    resource,
        OrgId:       orgID,
        // scope intentionally not in saas-starter's Decide v1;
        // pass via context if needed.
    })
    if err != nil {
        return false, "", "", fmt.Errorf("saas-starter decide: %w", err)
    }
    allowed := resp.Decision == api.Decision_DECISION_ALLOW
    return allowed, resp.Reason, resp.DecisionPath, nil
}
```

This type lives in your host's codebase (Mind, gateway, CLI),
not in core. core has zero compile-time knowledge of saas-starter.

### 2. `policy.Decider` — wrap the backend with caching + observability

`SaasPDP` is the production wrapper. It takes a `PermissionsBackend`
and adapts it to the `Decider` interface, adding:

- LRU cache for positive decisions (configurable TTL)
- Never caches denies (immediate revocation visibility)
- Fail-closed on backend error (the security default)
- PDPMetrics for observability
- `RecordDecision` per call for structured logging

```go
import "github.com/codefly-dev/core/policy"

backend := &SaasStarterBackend{Client: yourGrpcClient}
metrics := &policy.PDPMetrics{}

decider := policy.NewSaasPDP(backend).
    WithCache(15 * time.Second).
    WithMetrics(metrics)
```

Operators read `metrics.Snapshot()` periodically and ship to
Prometheus / OTEL / wherever.

### 3. `policy.PluginRegistration.PDP` — wrap plugin's outer tool calls

The PDP gates EVERY `CallTool` to the plugin via `policyguard.Guard`.
Layered for production:

```go
// 1. Inner: the SaasPDP backed by your saas-starter client.
inner := policy.NewSaasPDP(backend).
    WithCache(15 * time.Second).
    WithMetrics(metrics)

// 2. Read mode from env, build the standard stack.
mode, err := policy.ResolvePDPMode()
if err != nil {
    log.Fatal(err)  // CODEFLY_PDP_MODE typo: fail loud at startup
}
requireManifest := policy.ResolveRequireManifest()

// 3. Per plugin: build the full PDP stack (Shadow → Ceiling → Inner).
//    The manifest comes from the plugin's resources.Toolbox.
pdp := policy.BuildPDP(mode, inner, plugin.Permissions, requireManifest, metrics)
```

This `pdp` is what you pass into `PluginRegistration.PDP` — but
note: `PluginRegistration` is what the PLUGIN constructs in its own
`agents.Serve(...)` call. The HOST doesn't directly populate it.

The way this works in practice: **your host doesn't pass PDP into
PluginRegistration**. Your host passes `WithPermissionsCallback
(decider)` (see #4 below). The plugin's outer Guard logic runs in
the plugin process; right now plugins construct their own PDP
inside `agents.Serve` (typically `AllowAllPDP`).

For end-to-end outer-call PDP gating, the plugin's `agents.Serve`
needs to build the PDP from env + manifest. We can either:
- Have the host pass a "cooked" PDP via callback as well (similar
  to the inline callback) — adds a second callback channel
- Have the plugin build its own PDP with `policy.BuildPDP` reading
  CODEFLY_PDP_MODE — simpler, but the plugin would need a way to
  reach saas-starter directly (which we don't want)

The current architecture: **outer tool authorization happens on the
host side via the same callback channel as inline `Authorized()`.**
The plugin's outer call routes through the host's permission
callback by design. The plugin doesn't need to construct a PDP at
all if it relies on the callback for everything.

### 4. `manager.WithPermissionsCallback(decider)` — session internals

`ToolboxSession` installs this option together with the principal, private UDS,
per-spawn secret, bound-digest requirement, and strict production admission.
Core-level tests may compose it directly:

```go
import "github.com/codefly-dev/core/agents/manager"

agentConn, err := manager.Load(ctx, agentRef,
    manager.WithSandbox(sb),
    manager.WithPrincipal(p),
    manager.WithPermissionsCallback(decider),
)
```

`manager.Load`:
1. Creates a `policy.PermissionsCallbackServer` backed by `decider`
2. Listens on a per-spawn UDS in the OS temp dir (file perms 0600)
3. Sets `CODEFLY_PERMISSIONS_SOCKET=<path>` in the plugin's env
4. Binds the spawn-time `Principal` as the trusted subject
5. Hooks `agentConn.Close()` to shut the server + remove the socket

The plugin's `policy.AuthorizerFromContext(ctx).Authorized(...)`
calls dial this UDS automatically (lazy connect, reused via
keep-alive).

## Worked example: Mind acting on a user's behalf

Suppose Mind receives a request from user `antoine@codefly.dev`
to "review my open PRs". Mind:

1. Reads the user's session JWT, validates against saas-starter,
   extracts `principal_id=u-antoine`, `org_id=org-codefly`
2. Constructs a `policy.Principal{ID: "u-antoine", Kind: KindHuman,
   OrgID: "org-codefly", DisplayName: "antoine@codefly.dev"}`
3. Opens one Toolbox session with that principal and trusted tenant scope:

```go
decider := policy.NewSaasPDP(saasBackend).WithCache(15 * time.Second)
toolboxSession, err := session.Open(ctx, session.Options{
    Manifest: githubManifest,
    Principal: antoineAsPrincipal,
    Decider: decider,
    Scope: session.Scope{
        TenantID: trustedTenantID,
        Environment: "production",
    },
    Audit: auditSink,
})
if err != nil {
    return fmt.Errorf("open github toolbox: %w", err)
}
defer toolboxSession.Close()

result, err := toolboxSession.Call(ctx, session.CallRequest{
    Name: "github.list_prs",
    Arguments: arguments,
    Resource: "repo:codefly-dev/codefly.dev",
    RequestID: requestID,
    ObjectiveID: objectiveID,
    TaskID: taskID,
})
```

What happens behind the scenes:

- The session lists summaries and describes only `github.list_prs`
- Host policy approves the exact descriptor/request/resource/scope
- The plugin's outer `CallTool` arrives at its `agents.Serve` server
- `principalUnaryInterceptor` extracts the Principal from
  `CODEFLY_PRINCIPAL_TOKEN` env and stamps it on ctx
- Same interceptor stamps the Authorizer (callback client) on ctx
- `policyguard.Guard` (if configured) calls the host's PDP — the
  call goes through the same UDS callback — verdict comes back
- If allowed: handler runs with antoine's principal on ctx
- Inside the handler, plugin can call `Authorized(ctx, "github.read_secrets", ...)`
  for fine-grained per-resource gating — same UDS, same
  decider, same cache

When `toolboxSession.Close()` runs:
- gRPC connection closes
- UDS callback server shuts down
- The process exits and its private socket directory is removed

## Mode switching: shadow → enforce

Production rollout (M5 in the master plan):

1. Deploy with `CODEFLY_PDP_MODE=shadow`. Decisions are logged
   via `RecordDecision`, metrics counters increment, but every
   call returns Allow. Watch your dashboard for "would-have-been
   denied" events:

   ```
   pdp_decisions_total{decision="deny"}
   ```

2. Investigate denies. Are they correct policy decisions, or
   policy gaps (a legitimate role grant that's missing)? Fix
   the gaps.

3. After ≥7 days of clean shadow runs, flip the env to
   `CODEFLY_PDP_MODE=enforce`. Decisions now bind. Have the
   rollback ready: `CODEFLY_PDP_MODE=shadow` on the same env
   reverts in seconds (no code redeploy needed).

## Multi-tenant rule: one authority envelope per session

A Toolbox process is bound to exactly one principal and trusted session scope.
Open a separate `ToolboxSession` when the principal, organization, tenant, or
environment changes. The `x-codefly-principal` metadata key is reserved and any
attempt to replace spawn authority per call is rejected as `Unauthenticated`.
This keeps outer authorization, inline `Authorized()` callbacks, replay state,
trace context, and audit attribution on the same identity.

## Audit story

Every PDP decision flows through `RecordDecision`, which:

- Increments the right counter on `PDPMetrics`
- Logs a structured wool entry with principal_id, action,
  resource, decision, decision_path, latency_ms
- (Future M9) Attaches a span event for distributed tracing

Your host adds the audit-log write — saas-starter's
`audit_events` table is the canonical sink. Wire a custom PDP
wrapper that writes audit on top of `RecordDecision`:

```go
type AuditingPDP struct {
    Inner   policy.Decider
    Auditor func(ctx context.Context, ev policy.DecisionEvent)
}

func (a AuditingPDP) Evaluate(ctx context.Context, req *policy.PDPRequest) policy.PDPDecision {
    d := a.Inner.Evaluate(ctx, req)
    a.Auditor(ctx, policy.DecisionEvent{
        Toolbox:  req.Toolbox,
        Tool:     req.Tool,
        Decision: d,
        // ... fill from ctx
    })
    return d
}
```

## Two-level model: gateway pre-evaluation + scoped tokens

This section explains the machinery owned by `ToolboxSession`; it is not a
second supported host integration path. Manually copying only part of the
example will fail strict verification because production tokens must bind the
principal/organization, audience, selected descriptor digest, exact request
digest, resource, scope caveats, expiry, and one-use budget.

For defense-in-depth + the performance win, wire the gateway-layer
evaluator. Two new pieces:

### 1. Construct a `GatewayEvaluator`

**Recommended: load tool policies from operator YAML.**

```yaml
# /etc/codefly/tool-policies.yaml
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
      allowlist:
        context_key: ci_status
        allowed: [green, success]

  - id: codefly.dev/github-bot:0.1.0:github.force_push
    deny: true
    reason: "force-push requires manual approval (M7+)"
```

```go
import "github.com/codefly-dev/core/policy"

secret := policy.NewSpawnSecret() // 32 random bytes

// Parse operator YAML into ToolPolicy implementations.
yamlBytes, _ := os.ReadFile("/etc/codefly/tool-policies.yaml")
toolPolicies, err := policy.ParseYAMLToolPolicies(yamlBytes)
if err != nil {
    log.Fatal(err) // misconfigured policy file: fail startup loud
}

evaluator := &policy.GatewayEvaluator{
    Decider:      yourSaasPDP,        // role-grant check
    Secret:       secret,              // must match the plugin's
    DefaultTTL:   120 * time.Second,
    Metrics:      yourPDPMetrics,
    ToolPolicies: toolPolicies,
}
```

**Programmatic alternative** (when you don't want YAML):

```go
evaluator := &policy.GatewayEvaluator{
    Decider: yourSaasPDP,
    Secret:  secret,
    ToolPolicies: map[string]policy.ToolPolicy{
        "codefly.dev/github-bot:0.1.0:github.merge_pr": yourCustomMergePolicy,
        "codefly.dev/github-bot:0.1.0:*": policy.ManifestCeilingPolicy{
            Manifest: pluginManifest,
        },
    },
}
```

**External engine alternative** (Cedar / OPA / custom):

```go
type cedarEngine struct {
    bundle *cedar.PolicySet
}

func (c *cedarEngine) Evaluate(ctx context.Context, in policy.EvaluationInput) (policy.ExternalDecision, error) {
    // ... evaluate Cedar bundle against in ...
    return policy.ExternalDecision{
        Allow:   decision == cedar.Allow,
        Reason:  cedar.Reason(),
        Caveats: cedar.Snapshot(),
        TTL:     60 * time.Second,
    }, nil
}

evaluator := &policy.GatewayEvaluator{
    Decider: yourSaasPDP,
    Secret:  secret,
    ToolPolicies: map[string]policy.ToolPolicy{
        "codefly.dev/github-bot:0.1.0:*": &policy.ExternalToolPolicy{
            Evaluator: &cedarEngine{bundle: yourBundle},
        },
    },
}
```

Core has no dependency on Cedar/OPA. You bring the engine.

### 2. Pass the same secret to the spawned plugin (session internals)

```go
agentConn, err := manager.Load(ctx, agent,
    manager.WithSandbox(sb),
    manager.WithPrincipal(p),
    manager.WithPermissionsCallback(decider),
    manager.WithScopedAuthSecret(secret), // SAME secret as evaluator
)
```

`manager.Load` base64url-encodes the secret and sets it as
`CODEFLY_SCOPED_AUTHZ_SECRET` in the plugin env. The plugin's
Guard picks it up automatically and uses it to verify incoming
tokens.

### 3. Mint + attach token per outgoing call (session internals)

```go
result, err := evaluator.EvaluateAndMint(ctx, policy.EvaluationInput{
    Principal: principal,
    Toolbox:   "codefly.dev/github-bot:0.1.0",
    Tool:      "github.merge_pr",
    Resource:  "repo:codefly-dev/codefly.dev",
    CatalogDigest: approvedTool.Digest,
    RequestDigest: requestDigest,
    Caveats: wellKnownCaveats,
    Context:   contextForCaveats,
})
if err != nil {
    return errFromPolicyDecision(err)
}

// Attach the token to outgoing metadata.
md := metadata.Pairs(policy.ScopedAuthMetadataKey, result.Token)
ctx = metadata.NewOutgoingContext(ctx, md)

resp, err := pluginClient.CallTool(ctx, callToolRequest)
```

The plugin's Guard verifies the token, skips the PDP-via-callback
for outer authorization (fast path), and the inner toolbox handler
runs with the verified `ScopedAuthorization` stamped on `ctx`.

### What this gives you operationally

- **Latency**: plugin verifies HMAC locally (<1ms), no UDS call.
- **Audit clarity**: every minted token has a unique ID; log at
  mint AND at consume; correlate.
- **Three defense layers**: gateway + plugin Guard + inline
  Authorizer. Any one layer's failure (bug, misconfiguration,
  compromise) leaves the others enforcing.
- **Caveat enforcement**: bake conditions (ci_status, labels,
  time-of-day) into tokens; verifier rejects if conditions
  changed by call time.

### Local low-level mode

Low-level local tests may omit scoped authorization and exercise the callback
PDP path directly. This is not production admission: a production
`ToolboxSession` always requires the principal, callback, enforcing sandbox,
per-spawn secret, catalog/request binding, and guarded token path.

See `TWO_LEVEL_AUTHZ.md` in this directory for the full design
rationale, security analysis, and threat model.

## M7 escalation — synchronous approval flow

When the gateway pre-evaluation OR the plugin's PDP returns a
deny that's recoverable via approval (e.g. high-risk action that
needs a grantor sign-off), wire the escalation path:

```go
import "github.com/codefly-dev/core/policy"

// At host startup, wire your grantor implementation. The
// SaasStarterEscalationGrantor lives in your host binary and
// adapts saas-starter's RequestDelegation / WaitForDelegation /
// DecideDelegation RPCs to core's EscalationGrantor interface.
// See MIND_INTEGRATION_M7.md for the implementation template.
grantor := &mindhost.SaasStarterEscalationGrantor{
    Client: yourPermissionServiceClient,
    Secret: spawnSecret, // SAME as manager.WithScopedAuthSecret
}
policy.SetGlobalEscalationGrantor(grantor)
```

Plugin authors call `policy.RequestEscalation(ctx, req)` or the
ergonomic `policy.AuthorizedOrEscalate(ctx, authorizer, action,
resource, justification)`. The SDK:

1. Validates the request (action, principal, justification non-
   empty).
2. Calls grantor.Request — blocks until the grantor (a human in
   your saas-starter approval UI) decides OR the request's
   timeout elapses.
3. On approve: receives a freshly minted scoped-auth token from
   saas-starter; stamps the verified ScopedAuthorization on the
   returned ctx; attaches the encoded token as outgoing gRPC
   metadata for the retry.
4. On deny: returns `policy.ErrEscalationDenied` wrapping the
   grantor's reason.
5. On timeout: returns `policy.ErrEscalationTimedOut`.
6. On infrastructure error: returns the underlying error
   (distinguishable from the policy outcomes).

The plugin retries the original action with the elevated ctx —
the receiving plugin's Guard takes the fast path and dispatches
without further PDP consultation.

See `MIND_INTEGRATION_M7.md` for the full saas-starter
implementation guide (table schema, RPC sketches, UI surface).

## What you DON'T need to do

- ❌ Implement the wire format (token encoding, UDS protocol)
- ❌ Manage plugin processes manually (manager handles it)
- ❌ Coordinate inline `Authorized()` semantics with plugin code
  (the interceptor handles ctx stamping)
- ❌ Marshal principal data through gRPC metadata yourself
- ❌ Reason about cache invalidation (TTL handles it; M10 adds
  Postgres NOTIFY for instant invalidation)

You implement: `PermissionsBackend`. Everything else composes
from existing core types.

## Testing your host wiring

Use the conformance fixture plus the CLI-independent host harness for the
supported integration path. Backend adapter unit tests may use the policy test
harness, but V3/V4 host qualification must launch the real fixture process.

Example backend unit setup:

```go
backend := policy.NewFakeBackend(false /* default deny */).
    Allow("u-antoine", "github.read_pr", "repo:codefly/x", "org-codefly")

decider := policy.NewSaasPDP(backend)

// Spawn a plugin against this decider, test that
// CallTool with the right principal succeeds and the wrong
// principal fails.
```

End-to-end examples live in `core/toolbox/session/session_test.go`; they cover
production sandbox admission, allow/deny, expiry, wrong scope, timeout,
cancellation, process loss, concurrency, redaction, and cleanup.

## Where the code lives

| Concern | File |
|---|---|
| `Decider` / `Authorizer` / `PermissionsBackend` interfaces | `core/policy/decider.go` |
| `SaasPDP` (production Decider) | `core/policy/pdp_saas.go` |
| `PermissionsCallbackServer` (host UDS server) | `core/policy/callback.go` |
| `callbackAuthorizer` (plugin UDS client) | `core/policy/callback.go` |
| `WithPermissionsCallback` LoadOption | `core/agents/manager/loader.go` |
| Supported external-host lifecycle | `core/toolbox/session/session.go` |
| CLI-independent conformance harness | `core/toolbox/conformance/host/harness.go` |
| Plugin-side principal + authorizer ctx stamping | `core/agents/principal_interceptor.go` |
| `BuildPDP` (Shadow/Ceiling/Inner composition) | `core/policy/pdp_mode.go` |
| Reference plugin (Authorized example) | `core/toolbox/launch/cmd/network-victim-toolbox/main.go` |
| E2E test of the full wire | `core/toolbox/launch/permission_e2e_test.go` |

## Open follow-ups

- **SaasStarterBackend implementation in your host.** The interface
  is defined in core; the concrete adapter against saas-starter's
  generated client lives outside core (typically in your CLI or
  Mind binary). One file, ~30 lines.
- **M7 escalation flow** — `host.RequestEscalation` API, on top of
  saas-starter's `delegation_grants` table. Not in core yet.
- **M9 audit UI** — saas-starter's audit_events query interface.
  Independent of core; needs Mind/saas-starter UI work.
