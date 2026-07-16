# Permissions for plugin authors

If you're writing a codefly plugin (a Toolbox), this is the only
permissions document you need.

## TL;DR

You have **three** interactions with the permission system:

1. **DECLARE** what your plugin needs in `toolbox.codefly.yaml`
2. **READ** the calling principal in your handler (optional, for
   audit/display only)
3. **REQUEST** more authority on demand if you need it (M7+)

You DO NOT:

- write security checks in your plugin code
- verify or decode principal tokens
- consult role assignments or audit logs
- enforce permission decisions

The host does all of that. Your plugin trusts that *if a tool call
reached your handler, the principal had authority to make it.*

For a native Toolbox, use `registry.Base` to declare the tool surface once and
`agents.ServeToolbox(server)` to start it. `ServeToolbox` automatically installs
the callback-backed guard, scoped-token verifier, and canonical audience when
the host launches the process; plugin authors do not compose PDP wiring.

---

## 1. DECLARE — `toolbox.codefly.yaml`

Every action your plugin can perform must be declared in the manifest.
This is the **ceiling** — the maximum authority the plugin will ever
have. Even if a role grants the principal more, the PDP intersects
with this declaration. Even if your plugin is compromised, it can't
silently exceed what's declared.

### Minimum example

```yaml
name: github-bot
version: 0.1.0
agent:
  kind: codefly:toolbox
  publisher: codefly.dev
  name: github-bot
  version: 0.1.0
sandbox:
  read_paths: ["${WORKSPACE}"]
  write_paths: ["${WORKSPACE}"]
  network: open                      # github API needs net
permissions:
  required:
    - action: github.read_pr
      resource: "repo:${ORG}/*"
      reason: "Inspect PRs to decide auto-merge eligibility"
    - action: github.merge_pr
      resource: "repo:${ORG}/*"
      reason: "Auto-merge approved PRs with green CI"
  optional:
    - action: github.deploy_staging
      resource: "env:staging"
      reason: "Trigger staging deploy after merge"
  risk_levels:
    github.merge_pr: medium          # require approval if no time-window grant
    github.force_push: critical      # always require approval
    github.read_pr: low              # role grant alone is enough
```

### Field reference

**`required`**: actions the plugin needs to function. If the
installing user doesn't grant any of these, the install fails.

**`optional`**: actions that enhance functionality but aren't
mandatory. Your plugin code MUST handle their absence gracefully —
a denied optional action must skip the dependent feature, not
crash. Test your code with the optional permissions BOTH granted
and denied.

**`reason`**: human-readable explanation surfaced to the user at
install time. *Required entries must have a non-empty reason* —
without it, the install-time review is just a string of action
names. Be specific: "Inspect PRs to decide auto-merge eligibility"
beats "GitHub access".

**`action`**: dotted name (`github.read_pr`). The verb your tool
performs. May contain `*` wildcard (`github.*` matches anything in
the github namespace).

> **Wildcard rule:** only the bare `*` and the `prefix*` suffix
> form are supported. Patterns like `*foo`, `foo*bar`, or `**`
> are rejected by `PermissionPolicy.Validate()` at install time —
> they used to match nothing silently. Use multiple explicit
> declarations instead.

**`resource`**: typed pattern (`repo:codefly-dev/*`,
`env:staging`, `file:/tmp/*`). Empty means "any resource of any
type" — usually too broad; declare specific types when you can.
Same suffix-`*`-only wildcard rule as `action`. Placeholders:
`${ORG}`, `${WORKSPACE}` are expanded at install time, not at
PDP-call time.

**`risk_levels`**: action → tier (`low`/`medium`/`high`/`critical`).
At PDP time, high-risk actions can require approval (M7+) even
when role-granted. You know the impact of your actions best — be
honest about risk. `force_push` is critical; `read_pr` is low.

### What happens at install time

1. User runs `codefly install github-bot`
2. CLI reads `toolbox.codefly.yaml`, validates the permissions
   block (`PermissionPolicy.Validate()`)
3. CLI surfaces the declarations to the user with the reasons:
   ```
   github-bot v0.1.0 requests these permissions:
     [REQUIRED] github.read_pr on repo:${ORG}/*
                "Inspect PRs to decide auto-merge eligibility"
     [REQUIRED] github.merge_pr on repo:${ORG}/*  ← MEDIUM RISK
                "Auto-merge approved PRs with green CI"
     [OPTIONAL] github.deploy_staging on env:staging
                "Trigger staging deploy after merge"
   Approve? (y/N)
   ```
4. On approve: a Principal of `kind=agent` is created in
   saas-starter with role grants matching the manifest
5. The plugin is now installed; subsequent calls use this Principal

### What happens at call time

When a tool the plugin exposes is called:

1. The PDP first checks your **manifest declaration**: is the
   action declared? If not, deny — your plugin can't ask for
   authority it didn't reserve at install. This is the **ceiling**.
2. The PDP then checks the **role grant** in saas-starter: is the
   principal currently allowed to do this action? Roles can be
   revoked; the manifest grant doesn't survive revocation.
3. If both pass, the call reaches your handler with the principal
   stamped on `ctx`.

Both gates must pass. The manifest is the upper bound; the role
grant is the current state.

---

## 2. READ — `policy.PrincipalFrom(ctx)` and `policy.ScopedAuthFrom(ctx)`

Inside a tool handler, you can read both the calling principal AND
the gateway-issued ScopedAuthorization (when the two-level model is
deployed). Both are read-only metadata — your plugin reads them for
audit / display / data filtering, never for authorization decisions.

```go
import "github.com/codefly-dev/core/policy"

func (s *server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
    p := policy.PrincipalFrom(ctx)  // may be nil

    // Use p only to choose the already-authorized data partition.
    if p != nil && p.Kind == policy.KindHuman {
        return findUserOwnedResources(ctx, p.ID)
    }

    // ... rest of your tool implementation
}
```

### Reading the gateway's authorization (`policy.ScopedAuthFrom`)

When the host runs the two-level model (Mind, codefly gateway), the
gateway pre-evaluates a tool policy and mints a ScopedAuthorization
token that authorizes THIS specific (action, resource, principal,
time-window). The Guard verifies the token before your handler runs.
Inside the handler:

```go
sa := policy.ScopedAuthFrom(ctx)  // may be nil (single-level mode)
if sa != nil {
	// sa.ID is a safe audit correlation reference. Do not log the token,
	// principal, resource, tenant caveats, or raw request/result.
	recordAuthorizationReference(sa.ID)
}
```

When `sa` is non-nil, your call is on the **fast path** — the gateway
pre-evaluated, the Guard verified the token, the PDP was NOT
consulted. The `sa.ID` is your audit correlation key — log it; the
gateway logs the same id at mint time.

When `sa` is nil, your call took the **defense path** — the Guard
fell back to the PDP-via-callback for outer authorization (gateway
not deployed or no scoped credential was supplied). A credential
that is present but invalid is rejected before the handler runs.
Behavior is identical from your handler's perspective; the `nil`
just means there's no gateway-side trace id to correlate with.

**You don't need to do anything different based on which path** —
the call reached you, you're authorized. Read `sa` only for audit
detail and (rarely) for caveat-aware behavior.

### What you CAN do with the principal

**Audit attribution**: let the host's structured `ToolboxSession` audit sink
record principal references. Ordinary plugin logs should use the scoped
authorization/invocation correlation ID, not principal or tenant identifiers.

**Data filtering**: if your tool returns lists scoped to "things
the principal owns", filter by `p.ID` or `p.OrgID`. This is NOT
authorization — you're just choosing what data to RETURN.

**Branching display**: agents and humans might want different
output verbosity. `p.Kind == policy.KindAgent` for terse machine-
parseable; `p.Kind == policy.KindHuman` for richer.

### What you MUST NOT do with the principal

**DON'T gate behavior on it for security:**

```go
// ❌ WRONG — never check authorization in plugin code:
if p == nil || p.Kind != policy.KindAgent {
    return nil, errors.New("only agents may call this")
}
```

If you find yourself writing checks like this, you're rebuilding
the auth layer in the wrong place. The PDP already gates the
call before it reaches you. If "only agents may call this"
needs enforcing, declare a permission and let the PDP enforce
it via the role grant.

**DON'T fabricate or modify the principal:**

```go
// ❌ WRONG — creating a fake principal to elevate authority:
p := &policy.Principal{Kind: policy.KindAgent, OrgID: "any"}
ctx = policy.WithPrincipal(ctx, p)
```

Plugin code can't elevate. The principal stamped by the
interceptor is the only authoritative one. Constructing your
own does nothing for downstream PDP calls — the host's PDP
holds the authoritative principal from the inbound token.

**DON'T cache the principal across calls:**

```go
// ❌ WRONG — principals can be revoked between calls:
var cachedPrincipal *policy.Principal
func handler(ctx context.Context, ...) {
    if cachedPrincipal == nil {
        cachedPrincipal = policy.PrincipalFrom(ctx)
    }
    // use cachedPrincipal
}
```

Each call has its own principal. A revocation between calls
should immediately deny — caching breaks that property.

### When the principal is nil

`policy.PrincipalFrom(ctx)` returns nil when:

- The host called `WithoutPrincipal()` at load
- The host called `Load` without picking either option (legacy)

Malformed spawn authority and per-call principal replacement are rejected as
`Unauthenticated` before the handler. In explicit no-principal local mode, your
behavior:

- **DON'T crash**: a nil principal is a valid state.
- **DON'T fail closed in the handler**: the PDP did or didn't
  authorize the call before you got it. You're past authz.
- **DO let the host audit sink record anonymous attribution**; do not add raw
  identity data to ordinary plugin logs.

---

## 2.5. CHECK SUB-OPERATIONS — `Authorized(action, resource)`

The outer tool call is gated by the Guard before reaching your
handler. But sometimes you need to gate a sub-operation INSIDE the
tool — e.g. include secrets only if the principal can read them,
or fail fast on a bulk operation if the principal isn't allowed
the per-item action.

For these cases, call `policy.AuthorizerFromContext(ctx).Authorized
(ctx, action, resource)`:

```go
import "github.com/codefly-dev/core/policy"

func (s *server) GetData(ctx context.Context, req *Req) (*Resp, error) {
    auth := policy.AuthorizerFromContext(ctx)

    data := getBasicData()

    // Optional secret inclusion — degrade gracefully if denied.
    allowed, _, err := auth.Authorized(ctx, "data.read_secrets", req.ResourceID)
    if err == nil && allowed {
        data.Secrets = getSecrets()
    }

    return data, nil
}
```

Or for bulk operations, fail-fast:

```go
func (s *server) BulkMerge(ctx context.Context, req *Req) (*Resp, error) {
    auth := policy.AuthorizerFromContext(ctx)
    for _, pr := range req.PRs {
        allowed, reason, err := auth.Authorized(ctx, "github.merge_pr", "pr:"+pr.ID)
        if err != nil {
            return nil, fmt.Errorf("permission check failed: %w", err)
        }
        if !allowed {
            return nil, fmt.Errorf("denied for %s: %s", pr.ID, reason)
        }
    }
    return s.doBulkMerge(ctx, req.PRs)
}
```

### Three return values, three failure modes

```go
allowed, reason, err := auth.Authorized(ctx, action, resource)
```

| `(allowed, reason, err)`   | What it means                          | What to do                                    |
|---------------------------|----------------------------------------|-----------------------------------------------|
| `(true, "", nil)`         | Policy allows — proceed                | Run the gated logic                           |
| `(false, "REASON", nil)`  | Policy denies cleanly                  | Skip OR return the reason to the model        |
| `(false, "...", err)`     | Backend error (network, timeout, etc.) | Fail closed; surface err for incident tracing |

**Why the distinction matters:**
- A clean policy deny is the model's responsibility to handle —
  retry differently, ask for help, or skip gracefully.
- A backend error is an operational issue (auth backend down) —
  the model can't recover by retrying with different args; the
  error needs ops attention.

### How it works under the hood

1. The host called `manager.Load(WithPermissionsCallback(decider))`
   — this stood up a UDS-bound HTTP server in the host process.
2. The host set `CODEFLY_PERMISSIONS_SOCKET=/tmp/codefly-perms-...sock`
   in your plugin's environment.
3. `policy.AuthorizerFromContext(ctx)` returns a `callbackAuthorizer`
   that lazily dials this UDS on first call.
4. Your `Authorized()` call → JSON POST `/authorize` over UDS →
   host's PermissionsCallbackServer → host's Decider → verdict
   back to your plugin.
5. The host **uses the spawn-time principal**, not anything your
   plugin might claim — even a compromised plugin can't
   impersonate to escalate.

### When the callback isn't configured

If the host didn't pass `WithPermissionsCallback`, your plugin's
`AuthorizerFromContext` returns a `disabledAuthorizer` that
returns `(false, "no permissions callback configured", nil)` for
every call.

This is the safe default: **fail-closed**. Plugin code that calls
`Authorized()` in a hosting environment without a callback
gracefully degrades (the gated sub-operation is skipped); plugin
code that REQUIRES the answer surfaces the failure.

### Performance

- The first `Authorized()` call dials the UDS (a few hundred
  microseconds). Subsequent calls reuse the connection.
- Each call is a JSON round-trip over UDS (~1ms typical).
- The host's PDP caches positive decisions (5-30s TTL configured
  by the operator) — repeated checks for the same `(principal,
  action, resource)` short-circuit at the host without hitting
  saas-starter.
- If you're looping over many resources, the cache amortizes
  across the loop.

### What you SHOULDN'T use Authorized() for

- **Outer tool authorization.** That's already done by the Guard
  before your handler runs. Don't re-check `Authorized(ctx, req.Name,
  ...)` at the top of your handler — the answer is "yes, you're
  here, you're allowed".

- **Authorization that the manifest already declares.** If your
  manifest says `required: [github.merge_pr]`, the install-time
  grant covers `github.merge_pr` calls in your tool. You don't
  need to re-check at runtime — the user already approved.
  Authorized() is for *finer-grained* checks beyond what the
  manifest declares (per-resource, conditional sub-operations).

## 3. REQUEST — `policy.RequestEscalation` and `policy.AuthorizedOrEscalate` (M7)

When your plugin's role grant doesn't cover an action it needs,
it can request escalation:

```go
ctx, err := policy.RequestEscalation(ctx, policy.EscalationRequest{
    Principal:     policy.PrincipalFrom(ctx),
    Action:        "github.merge_pr",
    Resource:      "pr:codefly-dev/codefly.dev/456",
    Justification: "PR has 2 approvals, CI green, auto-merge label",
    Grantor:       "team:engineering",          // who to ask
    Timeout:       5 * time.Minute,
})
if err != nil {
    return nil, err  // denied or timeout
}
// ctx now carries a scoped-auth token; the action is authorized
return github.MergePR(ctx, prID)
```

The returned `ctx` has the scoped-auth token attached as
**outgoing gRPC metadata** — your next plugin call propagates it
automatically. For non-gRPC channels, read the verified token via
`policy.ScopedAuthFrom(ctx)` and attach however the channel
expects.

**Convenience wrapper for the common case** — try authorization
once, escalate on deny:

```go
ctx, err := policy.AuthorizedOrEscalate(
    ctx,
    nil, // pulls Authorizer from ctx
    "github.merge_pr",
    "pr:codefly-dev/codefly.dev/456",
    "PR has 2 approvals, CI green, auto-merge label",
)
if err != nil {
    // ErrEscalationDenied / ErrEscalationTimedOut / infrastructure error
    return nil, err
}
return github.MergePR(ctx, prID)
```

Three outcomes:

| Result | Behavior |
|---|---|
| Authorizer says allowed: returns original ctx, nil | Run the action |
| Authorizer denies, grantor approves: returns elevated ctx, nil | Run the action with elevated authority |
| Authorizer denies, grantor denies/times out: returns ctx, error | Surface the refusal |

The host:

1. Records the request as a `delegation_grants` row in saas-starter
2. Notifies the grantor (Slack, in-app, email)
3. Blocks until the grantor approves, denies, or it expires
4. On approve: mints a Biscuit token attenuated to the requested
   action+resource, returns it on `ctx`
5. The next handler call uses the elevated token; PDP allows

The plugin code does NOT mint the token, verify the approval, or
manage the grant. It declares the request, awaits the result.

**When to use escalation:**

- Your role grants `read` but a one-off operation needs `write`
- You're a routine bot that needs a human in the loop for high-
  risk actions

**When NOT to use escalation:**

- For every-day actions: declare them in the manifest, get role
  grants, no escalation needed
- To work around a missing manifest declaration: re-install with
  the right manifest instead
- To pretend you have authority you don't: the manifest is still
  the ceiling. Escalation works WITHIN your declared permissions
  by getting role-grant additions, NOT by exceeding the manifest.

---

## End-to-end example

Putting it together, here's a complete plugin:

### `toolbox.codefly.yaml`

```yaml
name: github-bot
version: 0.1.0
agent:
  kind: codefly:toolbox
  publisher: codefly.dev
  name: github-bot
  version: 0.1.0
sandbox:
  read_paths: ["${WORKSPACE}"]
  network: open
permissions:
  required:
    - action: github.read_pr
      resource: "repo:${ORG}/*"
      reason: "Inspect PRs to decide auto-merge eligibility"
    - action: github.merge_pr
      resource: "repo:${ORG}/*"
      reason: "Auto-merge approved PRs"
  risk_levels:
    github.merge_pr: medium
```

### `main.go`

```go
package main

import (
    "context"

    "github.com/codefly-dev/core/agents"
    toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
    "github.com/codefly-dev/core/toolbox"
    "github.com/codefly-dev/core/toolbox/registry"
    "github.com/codefly-dev/core/toolbox/respond"
)

type server struct {
	*registry.Base
}

func newServer(version string) *server {
	s := &server{}
	s.Base = registry.NewBase(registry.Descriptor{
		Name: "github-bot", Version: version,
		Description: "Read and merge approved GitHub pull requests.",
	}, s.tools()...)
	return s
}

func (s *server) tools() []*registry.ToolDefinition {
	object := respond.Schema(map[string]any{
		"type": "object", "additionalProperties": false,
	})
	return []*registry.ToolDefinition{
		{
			Name: "github.read_pr",
			SummaryDescription: "Read pull-request status and review metadata.",
			InputSchema: object,
			Tags: []string{"read-only", "network"},
			Idempotency: "idempotent",
			Handler: s.readPR,
		},
		{
			Name: "github.merge_pr",
			SummaryDescription: "Merge one policy-approved pull request.",
			InputSchema: object,
			Destructive: true,
			Tags: []string{"destructive", "network"},
			Idempotency: "side_effecting",
			Handler: s.mergePR,
		},
	}
}

func (s *server) readPR(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	// Call GitHub and return typed content. No permission check here.
	return respond.Struct(map[string]any{"state": "open"})
}

func (s *server) mergePR(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	// Perform the declared operation. The host and Guard already authorized it.
	return respond.Struct(map[string]any{"merged": true})
}

func main() {
	agents.ServeToolbox(newServer(toolbox.Version()))
}
```

That's the complete plugin. **No security checks in the code.** The
manifest declares the contract, the host enforces it via the PDP,
the handler trusts that authorized calls are already authorized.

---

## Common questions

### "What if my plugin needs to know which user invoked it?"

Read `policy.PrincipalFrom(ctx).ID` (or `DisplayName`) for audit
or display. Don't gate logic on it.

### "What if my plugin needs different behavior per user?"

Filter the data you return — don't gate the action. If the action
needs different authorization for different users, that's a role
grant in saas-starter (admin role gets one set of grants; member
role gets another). Both call the same tool; both reach your
handler; both run the same code. The DATA they see differs based
on what saas-starter grants them.

### "What if my plugin needs admin-only operations?"

Declare the admin operations as separate actions
(`github.admin.delete_repo`). Set their risk level to `critical`.
At install, the user grants them only to admin roles. PDP enforces
the role-grant at call time.

### "What if my plugin needs to make calls that aren't declared?"

It can't. The manifest is the ceiling. If you didn't declare it,
the PDP denies it. **This is by design** — if you need an action
you didn't declare, re-publish the plugin with an updated manifest;
users review the new declaration and re-approve.

### "Can my plugin spawn another plugin or call another agent?"

Yes (M3+). Sub-agent spawns inherit the principal context: the
parent's authority is the maximum the child can have. M6+ adds
Biscuit attenuation so children can be FURTHER restricted; never
expanded.

### "What happens during deny?"

Your handler is NOT called. The Guard layer (host-side, before
your handler) returns a `CallToolResponse{Error: reason}` to the
caller. The `Error` field carries the deny reason verbatim — the
agent SDK on the caller side surfaces this to the model so it can
plan around the limitation.

### "Can I read the role grants my plugin has?"

Not from inside the plugin. The grants live in saas-starter; the
plugin sees the OUTCOME (call allowed or denied). If you need to
introspect, the host has APIs for that — call from the host, not
from inside the plugin.

### "What about a plugin that doesn't have a permissions block?"

It is not production-admissible. Add explicit sandbox and permission
declarations matching the runtime catalog; production startup rejects empty or
drifting manifests before resolving or launching the binary.

---

## Where to look in the code

- `core/policy/principal.go` — `Principal` type, `PrincipalFrom`
- `core/policy/permissions.go` — `PermissionPolicy`, `SandboxPolicy`
- `core/policy/pdp.go` — PDP interface
- `core/policy/pdp_ceiling.go` — manifest-ceiling enforcement
- `core/policy/pdp_shadow.go` — observability-only mode
- `core/policy/pdp_mode.go` — env-driven mode resolver
- `core/agents/principal_interceptor.go` — gRPC interceptor that
  stamps the principal on ctx
- `core/agents/manager/loader.go` — `WithPrincipal` LoadOption
- `core/toolbox/policyguard/guard.go` — Toolbox→PDP wrapper
- `core/toolbox/registry/registry.go` — single-source tool definitions
- `core/toolbox/session/session.go` — supported host lifecycle
- `core/toolbox/conformance/host/harness.go` — external-host proof
- `core/toolbox/launch/cmd/network-victim-toolbox/main.go` —
  reference implementation including the `who.am.i` tool that
  reads the principal context

End-to-end tests in `core/toolbox/session/session_test.go` prove the full
two-phase, guarded, sandboxed, correlated, and cleaned-up wire.
