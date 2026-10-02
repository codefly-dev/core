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
  the gateway and audit reason over. It takes a `*workcontext.Verified`, and
  `Verified`'s fields are unexported with no exported constructor, so `Verify`
  is the only way to obtain one — the derivation cannot run on anything
  unverified, and that is a property the compiler holds rather than a
  convention. It fills `Principal.DelegationChain`, which is therefore
  derived, never hand-built.

## Task identity and hops

Task identity is `(tenant_id, owner_principal_id, task_id)` and is carried
unchanged across every hop. A hop is a new *session*, never a mutation:

| | mints | rule |
| --- | --- | --- |
| `Authority.Start` | the first session of a task | the owner acts directly, so the actor chain is empty |
| `Authority.Child` | one delegation hop | the hop's scopes attenuate the parent's effective scopes, and the child expires no later than the parent |
| `Authority.Grant` | the capability an approval justifies | the one hop that may hold authority the previous hop did not |

A verifier accepts a capability for `Skew` past its expiry, so a caller can
hold a `*Verified` that has already expired. Exchanging one is refused rather
than clamped: a capability minted with an expiry in the past would report
success and fail later, in another process, as an authentication error.
`Start` and `Child` mint at the issuer's *current* authorization revision,
read through `Authority.Revisions` — inheriting the parent's would produce a
child born superseded whenever a revocation landed in between. A `Grant`
keeps the revision the decision was made at, because bumping past it is
exactly what revokes an unspent grant.

`WorkScopeV1` is `(resource_kind, actions, resource_ids)`. Attenuation
(`workcontext.ScopeContained`) lets a child narrow a wildcard parent to
explicit ids and never the reverse, and never name an action the parent does
not hold.

Every exchange deep-copies the parent's claims. Sharing hop or scope messages
would let a mutation of the new capability rewrite what the parent is
*verified* to hold, and a later exchange off that parent would then attenuate
against the rewritten authority and mint a signed widening.

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
- the elevated hop is the same actor on every field a derived identity reads
  — id, kind, agent identity, organization — so a grant cannot keep the id and
  change who the capability resolves to;
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
oldest-first: the owner, then each preceding actor, and **one** link for an
approval grant. So the scenario in the `Principal` doc comment — U invokes A;
A acquires escalation from V — reads `[U, V]` with `Principal.ID` = A and the
grant id on V's link. `Verified.SHA256` is the digest an `ExecutionReceiptV1`
binds its claims snapshot to.

A quorum rides *inside* that one link, as `DelegationLink.Approvers`, each
approver carrying its kind. One approval is one hop of delegation however many
people had to agree to it: spreading a 3-of-5 quorum across five links would
count approver breadth as delegation depth, and `CheckDelegationDepth` — three
hops by default — would refuse the very call the quorum approved.

`Principal.OrgID` comes from `organization_id`, never from `tenant_id`. A
tenant may hold several organizations and authorization is scoped per
organization, so substituting one for the other either denies everything or
matches an organization nobody granted access to. An actor hop may name its
own, for an org-bridge agent acting outside the owner's.

## Fields the model gained

`owner_principal_kind`, `owner_agent_id`, `organization_id` and the actor
hop's `agent_id` and `organization_id` are all optional **on the wire**. A
capability minted before they existed is still a valid capability, and an
archived `ExecutionReceiptV1` embedding one still verifies — receipt
verification validates the whole message before it checks the signature, so a
schema rule added here would retroactively invalidate every receipt ever
attested. The requirement lives where the identity is built:
`PrincipalFromWorkContext` refuses a capability it cannot derive a kind or an
organization from, naming what is missing. Minting always fills them.

## The seal: one installation, one execution

A capability's scopes say what it may do. The **seal** says which installation
and which running build it was minted for, so that authority cannot outlive
either one. `WorkSealV1` is on every capability and carries four values the
issuer holds live:

| field | what moving it invalidates |
| --- | --- |
| `principal_epoch` | every capability minted for that principal, at once |
| `installation_id` | — it is identity, not a counter |
| `installation_revision` | every capability minted against the installation's previous terms |
| `build_incarnation` | every capability minted on a replaced run of the build |

`WorkOperationBindingV1` is on a capability that exercises one unit of
authority, and carries `binding_id`, `revision` and `incarnation`. The
`incarnation` is separate from the `revision` because a binding that was
withdrawn and re-created is not the same binding re-revised: a capability
sealed to the old one must not verify against the new.

`Verifier.Seals` (a `SealSource`) answers the live values, and it is **required**
like the revision source, the replay store and the grant source. A verifier
missing one refuses every capability rather than reading its absence as "sealing
off", because that would make the strongest check in the model the easiest one
to omit.

### Exact equality, and exact lookup

Every sealed number is compared for **exact equality**, not `>=` as
`authorization_revision` is. A capability carrying a revision *higher* than the
issuer's live one is refused too: there is no legitimate way to hold one — it
would mean a capability sealed to a state that has not happened — so the shapes
that produce it are a rolled-back installation and a forged seal, and neither
is a thing to accept.

The operation binding is resolved by **exact lookup on the sealed id**.
`SealSource` has no method that lists bindings or finds one matching a set of
scopes, so "search the bindings for one that contains these scopes" is not
expressible through the interface at all. That search is a predicate somebody
writes, and a predicate one case too generous grants authority nobody reviewed
— it fails in the direction of granting rather than refusing, which is the
wrong direction for the one check that stands between a token and an operation.

A mismatch is `ErrRevoked`, not `ErrInvalid`: the capability is not malformed
and was not forged, it was sound when minted and the state it was sealed to has
moved. A caller distinguishing "re-mint" from "reject this caller" needs those
to read alike. A capability carrying no seal at all is `ErrUnsealed`.

### Minting reads the seal; it never accepts one

`Authority.Seals` is required, and `StartInput` names only the
`InstallationID` (identity, which is the caller's to supply) and optionally an
`OperationBindingID`. Every counter is read from the source. A minter that
accepted the numbers would hand out capabilities nothing verifies, and the
failure would surface in another process as an authentication error rather than
at the mint that caused it.

`Child` and `Grant` **reseal** against the issuer's live state rather than
carrying the parent's sealed numbers forward — the same reason both re-read the
authorization revision. The installation is always the parent's: a delegation
hop narrows authority within one installation and never moves it, so taking an
installation from the hop would be a way to widen across installations.
`Grant` takes a `context.Context` for this reason.

The seal fields are optional **on the wire**, for the reason the previous
section gives: a `buf.validate` rule would retroactively invalidate every
archived capability and every receipt embedding one. The requirement lives in
the verifier instead, so a token without the sealed fields does not verify, and
`Authority.seal` refuses to sign an unsealed capability so a mint path that
forgot cannot ship one.

### Two implementations, one contract

Core's `workcontext` signs the deterministic **binary protobuf** encoding of
`WorkContextV1`. `codefly-dev/sdk-go/workcontext` signs a hand-written
snake_case **JSON** payload, and that is the implementation the product host
uses. The two are wire-incompatible by construction: a token from either looks
structurally right to the other and then fails signature verification, with a
message that reads like a key-rotation problem.

So adding these fields here is **necessary and not sufficient**. The SDK
enumerates its payload fields by hand, and a field added in core but not added
there is silently dropped at mint and silently absent at verify. `sdk-go#47`
carries them across, using the proto field names above verbatim as its JSON
keys so the two encodings name the same things. Because the SDK decodes with
`DisallowUnknownFields`, the cutover is ordered: verifiers upgrade before
minters, or every call fails closed during the window.

Core's `Verifier` is issuer-shaped — it requires a revision source, a replay
store, a grant source and a seal source, which a product module verifying an
incoming capability does not have. It is not a drop-in for the SDK's
consumer-shaped verifier, whatever the duplication costs.
