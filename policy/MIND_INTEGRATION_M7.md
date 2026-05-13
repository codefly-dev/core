# M7 — Synchronous escalation flow (implementer guide)

This document is for engineers implementing the **saas-starter
side** of the M7 escalation flow. The codefly core side is
already in place (`core/policy/escalation.go`); this doc covers
what saas-starter needs to add and how it wires into core.

For plugin authors using escalation, see PLUGIN_AUTHORS.md
section 3 ("REQUEST"). For the broader two-level model, see
TWO_LEVEL_AUTHZ.md.

---

## TL;DR

Saas-starter adds:

1. **`delegation_grants` table** — already shipped as migration
   `38_create_delegation_grants.up.sql`.
2. **Three RPCs** on `PermissionService`:
   - `RequestDelegation` — agent submits an escalation request
   - `WaitForDelegation` (server-stream) — agent blocks until
     decided
   - `DecideDelegation` — grantor approves or denies
3. **`SaasStarterEscalationGrantor`** — adapter implementing
   core's `policy.EscalationGrantor` interface; lives in your
   host binary (Mind / CLI / wherever the saas-starter gen
   client is imported).
4. **Approval UI** in saas-starter frontend — pending-request
   list + approve/deny actions.
5. **Notification fan-out** — Slack / email / in-app for new
   pending requests.

Items 1-3 are CODE. Items 4-5 are FRONTEND/INTEGRATION work.

---

## 1. Database schema (done)

Migration `38_create_delegation_grants.up.sql` adds:

- `delegation_grants` table with both M7 (`one_shot`) and M8
  (`pattern`) shapes. M7 uses only `kind='one_shot'`.
- Indexes for the approval-queue UI, audit lookups, and pattern
  matching.
- Postgres NOTIFY trigger that fires on every status change.
  The streaming RPC's handler LISTENs to per-row channels
  (`delegation_grants:<uuid>`) and wakes immediately when the
  grantor decides — no polling.

Key columns:

```sql
id              UUID
org_id          UUID    -- multi-tenant scope
actor_principal_id  UUID  -- the agent / user requesting
grantor_principal_id UUID -- NULL until decided
action          TEXT
resource        TEXT
justification   TEXT NOT NULL CHECK (length(trim(...)) > 0)
request_context JSONB
risk_level      TEXT    -- low|medium|high|critical
kind            TEXT    -- one_shot|pattern
status          TEXT    -- pending|approved|denied|expired|cancelled|active
expires_at      TIMESTAMP
minted_token_id TEXT    -- audit correlation to the scoped-auth token
request_hash    TEXT    -- idempotency
```

Read paths:

- **Approval queue UI**: `WHERE org_id=$1 AND status='pending' ORDER BY created_at DESC`. Indexed.
- **Streaming RPC**: `LISTEN 'delegation_grants:<uuid>'` + initial `SELECT WHERE id=$1`. NOTIFY-driven.
- **Audit "what did actor X do"**: `WHERE actor_principal_id=$1 ORDER BY created_at DESC`. Indexed.
- **Audit "what did grantor Y approve"**: `WHERE grantor_principal_id=$1 AND status='approved' ORDER BY decided_at DESC`. Indexed.

---

## 2. RPC sketches

Add these to `api.proto` alongside the existing `PermissionService`:

```proto
service PermissionService {
  // ... existing RPCs (CheckPermission, Decide, etc.) ...

  // RequestDelegation creates a new pending escalation request.
  // Idempotent on (org_id, request_hash) — same actor making the
  // same request returns the same row.
  rpc RequestDelegation(RequestDelegationRequest)
      returns (RequestDelegationResponse) {
    option (google.api.http) = {
      post: "/v1/delegations"
      body: "*"
    };
  }

  // WaitForDelegation streams state changes for a single request
  // until terminal (approved / denied / expired / cancelled).
  // Plain server-stream; the agent SDK reads exactly one event
  // (the terminal decision) and disconnects.
  rpc WaitForDelegation(WaitForDelegationRequest)
      returns (stream DelegationEvent) {
    option (google.api.http) = {
      get: "/v1/delegations/{id}:wait"
    };
  }

  // DecideDelegation is the grantor's approve/deny action. Called
  // from the approval UI; updates the row + fires NOTIFY.
  rpc DecideDelegation(DecideDelegationRequest)
      returns (DelegationGrant) {
    option (google.api.http) = {
      post: "/v1/delegations/{id}:decide"
      body: "*"
    };
  }

  // Optional: list pending requests for the approver UI.
  rpc ListPendingDelegations(ListPendingDelegationsRequest)
      returns (ListPendingDelegationsResponse) {
    option (google.api.http) = {
      get: "/v1/delegations:pending"
    };
  }
}

message RequestDelegationRequest {
  string org_id = 1;
  string actor_principal_id = 2;
  string action = 3;
  string resource = 4;
  string justification = 5 [(buf.validate.field).string.min_len = 1];
  google.protobuf.Struct context = 6;
  string risk_level = 7;
  google.protobuf.Duration timeout = 8;
  // Optional explicit grantor target ("user:antoine" /
  // "team:engineering"); empty → default approver chain.
  string grantor = 9;
  // Idempotency key — server hashes if empty.
  string request_hash = 10;
}

message RequestDelegationResponse {
  string id = 1;
  string status = 2;     // typically "pending"; could be "approved"
                         // immediately if a matching pattern grant exists
  google.protobuf.Timestamp expires_at = 3;
}

message WaitForDelegationRequest {
  string id = 1;
  string org_id = 2;
}

message DelegationEvent {
  string id = 1;
  string status = 2;        // approved | denied | expired | cancelled
  google.protobuf.Timestamp decided_at = 3;
  string grantor_principal_id = 4;
  string reason = 5;
  // On approve: the gateway's freshly minted scoped-auth token.
  // Empty on deny / expired / cancelled.
  string scoped_auth_token = 6;
}

message DecideDelegationRequest {
  string id = 1;
  string grantor_principal_id = 2;
  // "approved" | "denied"
  string decision = 3;
  string reason = 4;
}

message DelegationGrant {
  string id = 1;
  string org_id = 2;
  string actor_principal_id = 3;
  string grantor_principal_id = 4;
  string action = 5;
  string resource = 6;
  string justification = 7;
  string status = 8;
  string risk_level = 9;
  google.protobuf.Timestamp created_at = 10;
  google.protobuf.Timestamp decided_at = 11;
  google.protobuf.Timestamp expires_at = 12;
}

message ListPendingDelegationsRequest {
  string org_id = 1;
  int32 page_size = 2;
  string page_token = 3;
}

message ListPendingDelegationsResponse {
  repeated DelegationGrant grants = 1;
  string next_page_token = 2;
}
```

---

## 3. SaasStarterEscalationGrantor adapter

Lives in your host binary (Mind / CLI / wherever the saas-starter
gen client is). One implementation per host:

```go
package mindhost

import (
    "context"
    "errors"
    "time"

    "github.com/codefly-dev/core/policy"
    api "yourrepo/saas-starter/gen"
)

type SaasStarterEscalationGrantor struct {
    Client api.PermissionServiceClient
    Secret []byte // shared with manager.WithScopedAuthSecret
}

func (g *SaasStarterEscalationGrantor) Request(
    ctx context.Context,
    req policy.EscalationRequest,
) (*policy.EscalationResult, error) {
    // 1. Submit the request.
    submission, err := g.Client.RequestDelegation(ctx, &api.RequestDelegationRequest{
        OrgId:             req.Principal.OrgID,
        ActorPrincipalId:  req.Principal.ID,
        Action:            req.Action,
        Resource:          req.Resource,
        Justification:     req.Justification,
        Context:           toStruct(req.Context),
        RiskLevel:         "medium", // derive from manifest if available
        Timeout:           durationpb.New(req.Timeout),
        Grantor:           req.Grantor,
    })
    if err != nil {
        return nil, fmt.Errorf("RequestDelegation: %w", err)
    }

    // 2. Stream the decision.
    stream, err := g.Client.WaitForDelegation(ctx, &api.WaitForDelegationRequest{
        Id:    submission.Id,
        OrgId: req.Principal.OrgID,
    })
    if err != nil {
        return nil, fmt.Errorf("WaitForDelegation: %w", err)
    }

    // Receive exactly one terminal event.
    event, err := stream.Recv()
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return &policy.EscalationResult{Decision: policy.EscalationTimedOut}, nil
        }
        return nil, fmt.Errorf("stream Recv: %w", err)
    }

    switch event.Status {
    case "approved":
        // event.ScopedAuthToken was minted server-side using the
        // SAME shared secret. We could re-mint client-side instead;
        // server-mint is simpler and keeps secret rotation centralized.
        sa, verr := policy.Verify(event.ScopedAuthToken, policy.VerifyExpectations{
            Action: req.Action, Resource: req.Resource,
        }, g.Secret)
        if verr != nil {
            return nil, fmt.Errorf("server minted invalid token: %w", verr)
        }
        return &policy.EscalationResult{
            Decision:      policy.EscalationApproved,
            Token:         event.ScopedAuthToken,
            Authorization: sa,
            Decider:       event.GrantorPrincipalId,
            GrantID:       event.Id,
            Reason:        event.Reason,
        }, nil

    case "denied":
        return &policy.EscalationResult{
            Decision: policy.EscalationDenied,
            Reason:   event.Reason,
            Decider:  event.GrantorPrincipalId,
            GrantID:  event.Id,
        }, nil

    case "expired", "cancelled":
        return &policy.EscalationResult{
            Decision: policy.EscalationTimedOut,
            Reason:   event.Reason,
            GrantID:  event.Id,
        }, nil
    }
    return nil, fmt.Errorf("unknown decision status: %s", event.Status)
}
```

Wire at host startup:

```go
g := &SaasStarterEscalationGrantor{
    Client: yourPermissionServiceClient,
    Secret: spawnSecret, // SAME as manager.WithScopedAuthSecret(secret)
}
policy.SetGlobalEscalationGrantor(g)
```

---

## 4. Server-side mint of the scoped-auth token

The DecideDelegation handler (saas-starter side) must mint a
fresh `ScopedAuthorization` when status='approved' and write
the encoded token into `delegation_grants.minted_token_id` (just
the id, not the full token — the token returns via the streaming
event).

Pseudocode:

```go
func (s *PermissionServer) DecideDelegation(ctx context.Context, req *api.DecideDelegationRequest) (*api.DelegationGrant, error) {
    // 1. Authz: caller must be the grantor or an org admin.
    // 2. Atomic UPDATE on delegation_grants WHERE status='pending'
    //    SET status=$decision, decided_at=now(), grantor_principal_id=$caller, decision_reason=$reason
    //    RETURNING ...
    grant := atomicUpdate(...)

    // 3. If approved: mint scoped-auth token using the shared secret.
    if req.Decision == "approved" {
        actor, _ := loadPrincipal(ctx, grant.ActorPrincipalID)
        encoded, sa, err := policy.Mint(policy.MintInput{
            Principal:     actor,
            Action:        grant.Action,
            Resource:      grant.Resource,
            AudienceID:    grant.Audience, // from request_context if set
            TTL:           5 * time.Minute,
            MaxUses:       1,
            // Caveat: bake "via_approval" so audit shows the
            // call traveled through M7 escalation.
            Caveats: map[string]any{
                "via_approval": map[string]any{
                    "grant_id":    grant.ID,
                    "grantor_id":  grant.GrantorPrincipalID,
                    "approved_at": grant.DecidedAt.Unix(),
                },
            },
        }, sharedSecret)
        if err != nil {
            return nil, err
        }
        // 4. Persist token id for audit correlation.
        UPDATE delegation_grants SET minted_token_id = sa.ID WHERE id = grant.ID
        // 5. Return token in the streaming event (next NOTIFY fires).
    }

    // 6. NOTIFY trigger fires automatically on the UPDATE.
    return toProto(grant), nil
}
```

Plugins that verify these tokens need to register a verifier for
the `via_approval` caveat. Add to your plugin's startup:

```go
guard := policyguard.New(...).
    WithCaveatVerifiers(map[string]policy.CaveatVerifier{
        "via_approval": func(v any) error {
            // Plugin can audit-log the grant_id/grantor_id;
            // no semantic check needed (the token's signature
            // already proves the gateway approved it).
            return nil
        },
    })
```

OR register `via_approval` as a global caveat via
`policy.RegisterCaveat` for all plugins.

---

## 5. Approval UI surface

Front-end work (out of scope for this doc, but the schema +
RPCs are designed for):

- **Pending queue**: paginated list of `status='pending'` grants
  for the current org. Sorted by `risk_level DESC, created_at DESC`
  so high-risk requests rise to the top.
- **Detail view**: actor, action, resource, justification,
  request_context (rendered as key-value pairs), expires_at
  countdown.
- **Approve / Deny actions**: `POST /v1/delegations/{id}:decide`
  with optional reason. Approving instantly mints the token and
  unblocks the agent.
- **Bulk operations**: "approve all auto-merge requests for
  this org from the last 5 minutes". Reduces approval fatigue.
- **My approvals view**: history of decisions for the current
  user (audit-friendly).

Notification routes (Slack / email / in-app push) integrate via
saas-starter's existing notification machinery — fire on
`status='pending' INSERT`.

---

## 6. Test surface

Saas-starter integration tests should cover:

- **Idempotency**: two RequestDelegation with the same hash →
  same row id returned.
- **Streaming wake-up**: WaitForDelegation blocks until
  DecideDelegation fires NOTIFY → event arrives within ~10ms.
- **Timeout via expires_at**: pending grant past expires_at
  → status auto-transitions to 'expired' (cron job).
- **Authz on DecideDelegation**: only the grantor or org admin
  can decide; others get 403.
- **Token mint on approve**: scoped-auth token verifiable with
  the shared secret + carries the via_approval caveat.

Core-side tests already cover: the agent SDK calling the
grantor, handling all decision shapes, retrying with elevated
ctx. See `core/policy/escalation_test.go`.

---

## 7. Where this fits in the broader plan

| Phase | What | Done? |
|---|---|---|
| M1-M3 | Principal model, identity wire, callback | ✅ |
| M4 | Manifest as ceiling | ✅ |
| M5 | Enforce-mode rollout | (operational) |
| **M7** | **Synchronous escalation** | **core ✅, saas-starter pending** |
| M8 | Pattern grants (auto-approve patterns) | (extends M7's `kind='pattern'`) |
| M9 | Audit UI for delegation chains | (uses delegation_grants tables) |
| M10 | Hardening (TRL, anomaly detection) | |

Once saas-starter ships items 1-3 of this doc, M7 is end-to-end
operational. M8 layers on top by adding pattern-match logic in
RequestDelegation (auto-issue when an active pattern matches the
request).
