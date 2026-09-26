# Work Context: the canonical identity model

`codefly.base.v0.WorkContextV1` is the identity and authority carrier for one
Task/Session hop. It is signed, immutable, and already travels over both gRPC
metadata and HTTP (`wool.WorkContextHeader`). Everything else in core that
talks about *who is acting* is derived from a verified one rather than
assembled beside it.

Two packages own this:

- `workcontext` mints and verifies capabilities. `Authority` holds the signing
  key; `Verifier` is the only way to turn a token back into claims.
- `policy.PrincipalFromWorkContext` derives the `policy.Principal` the PDP,
  the gateway and audit reason over. It takes a `*workcontext.Verified`, so the
  derivation cannot run on anything unverified, and it fills
  `Principal.DelegationChain` — which is therefore derived, never hand-built.

## Task identity and hops

Task identity is `(tenant_id, owner_principal_id, task_id)` and is carried
unchanged across every hop. A hop is a new *session*, never a mutation:

| | mints | rule |
| --- | --- | --- |
| `Authority.Start` | the first session of a task | the owner acts directly, so the actor chain is empty |
| `Authority.Child` | one delegation hop | the hop's scopes attenuate the parent's effective scopes, and the child expires no later than the parent |
| `Authority.Grant` | the capability an approval justifies | the one hop that may hold authority the previous hop did not |

`WorkScopeV1` is `(resource_kind, actions, resource_ids)`. Attenuation
(`workcontext.ScopeContained`) lets a child narrow a wildcard parent to
explicit ids and never the reverse, and never name an action the parent does
not hold. `runnable` applies the same function to `lookup_scopes` against
`invoke_scopes`, so there is one rule rather than two that resemble each other.

## The approval hop

An agent hits a call it has no authority for. Policy answers *approvable*
rather than *refused*: `GatewayEvaluator.EvaluateAndMint` returns
`*policy.ApprovalRequiredError` — which matches `policy.ErrApprovalRequired`
and deliberately **not** `policy.ErrGatewayDeny` — carrying an
`ApprovalRequiredV1` detail on a `FailedPrecondition` status. The detail names
the request id and exactly the tuple a grant would have to pin, so a decision
and the capability that satisfies it are the same shape. The PDP fills it,
because only the PDP knows the request id and the vocabulary the approvals
engine decides in.

Core owns that signal and the shape of the capability that answers it. The
approvals engine — quorum, storage, notification, UI — is product-level.

On approval, the issuer mints a grant capability from the agent's *own*
session. It carries a `WorkGrantHopV1` and is bound six ways:

- `audience` is the tool, so it is not a credential anywhere else;
- `replay_policy` is `single-use`, consumed by the verifier's `ReplayStore`;
- expiry is no later than the grant window *or* the parent session;
- the hop's `subject` and `request_digest` pin it to one subject and one call;
- `granted_scope` is exactly one action on one resource of one kind;
- `authorization_revision` is the issuer's revision at the moment of decision.

The verifier holds the hop against the issuer's own `Grant` record — scope,
subject, request, approvers, audience, window, revocation — so a signed
capability cannot claim an approval that was never given, and revoking the
grant stops a capability already minted. The elevated hop is the *same*
principal as the hop before it: a grant elevates the actor, it never hands the
authority to someone else.

## The resume contract

Immutability is what makes returning to the flow work, and it is a contract
with tests (`workcontext/grant_test.go`, `policy/identity_e2e_test.go`):

- a grant capability is rejected for any other tool, subject, request or
  approver set, and on a second use;
- a rejection for any other reason does not consume the single use;
- the parent session is untouched — the agent still holds it, with the scopes
  it always had, and merging is approvable again rather than allowed;
- a grant capability delegates nothing further;
- bumping the issuer's `authorization_revision`, or revoking the grant,
  invalidates an unused grant capability.

## Audit

`policy.PrincipalFromWorkContext` derives `DelegationChain` as the lenders,
oldest-first: the owner, then each preceding actor, and for an elevated hop the
grant's approvers in place of the actor that would otherwise have lent it. So
the scenario in the `Principal` doc comment — U invokes A; A acquires
escalation from V — reads `[U, V]` with `Principal.ID` = A and the grant id on
V's link. `Verified.SHA256` is the digest an `ExecutionReceiptV1` binds its
claims snapshot to.
