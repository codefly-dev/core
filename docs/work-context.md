# Work Context: the canonical identity model

`codefly.base.v0.WorkContextV1` is the identity and authority carrier for one
Task/Session hop. It is signed, immutable, and already travels over both gRPC
metadata and HTTP (`wool.WorkContextHeader`). Everything else in core that
talks about *who is acting* is derived from a verified one rather than
assembled beside it.

Two packages own this:

- `workcontext` mints and verifies capabilities. `Authority` holds the signing
  key; `Verifier` is the only way to turn a token back into claims.
- Who a call is for is no longer derived here. The authority interface
  (codefly-dev/authority) checks a context and its hop stamps the call with
  the identity it answered, so a module reads that identity and never derives
  a `policy.Principal` from a token itself; core's former derivation,
  `policy.PrincipalFromWorkContext`, is deleted. `Verified`'s fields stay
  unexported with no exported constructor, so `Verify` remains the only way to
  obtain one.

## Task identity and hops

Task identity is `(tenant_id, owner_principal_id, task_id)` and is carried
unchanged across every hop. A hop is a new *session*, never a mutation:

| | mints | rule |
| --- | --- | --- |
| `Authority.Start` | the first session of a task | the owner acts directly, so the actor chain is empty |
| `Authority.Child` | one delegation hop | the hop's scopes attenuate the parent's effective scopes, and the child expires no later than the parent |
| `Authority.Grant` | the capability an approval justifies | the one hop that may hold authority the previous hop did not |

### The session is the issuer's to assign

`StartInput.SessionID` and `ChildInput.SessionID` let the issuer bind the
session a capability opens to a session it authenticated — the session of the
access token the caller presented. Empty mints a fresh one, as before; a child
may not reuse its parent's session, because a hop is a new session.

It exists for a party that holds *two* credentials and must tell whether one
was minted under the other: an access token confined to one consumer, and a
capability presented beside it. Matching the capability's session against the
token's needs nothing a hop may not read — `Inspected.SessionID` is already
there — so the pairing never turns a hop into a callee.

That pairing holds only while **every session a capability carries is either
the authenticated caller's own or a fresh one**. An issuer that signs a
caller-named id lets anyone mint a capability carrying another credential's
session, and the pairing admits it. Core cannot see where the issuer got the
bytes, so this rests on the issuer: supply a session you
authenticated, or leave it empty.

### The lifetime has an absolute ceiling

`MaxTTLCeiling` is 24 hours and no configuration raises it. `DefaultMaxTTL`
(one hour) is what an `Authority` mints within when it sets nothing;
`Authority.MaxTTL` narrows that, and a value above the ceiling is clamped to
it rather than honoured.

`DefaultMaxTTL` alone was a **default and not a bound**: `MaxTTL` took whatever
a host configured, so a thirty-day credential was mintable, and nothing on the
receiving side bounded a lifetime either — a verifier asked only "has this
expired", which a thirty-day capability passes for thirty days. A consumer
measured both halves and declined advice to drop its own client-side ceiling on
the grounds that neither layer actually enforced one. It was right.

The bound is now checked in `decodeClaims`, the single decode path, so
`Verify`, `Authenticate`, `Inspect` and `Decode` all answer it identically and
with one message. It was first written into `Verify` alone — in the same change
whose comment says a bound belongs in the one decode path and not in one
entrypoint — and the caller that paid for that was the one which never verifies
a signature at all: a **mint client** reads its own window through `Decode`
(then called `Inspect`), so a capability every receiver refuses was reported to
its holder as thirty days of validity. A missing bound tells a caller nothing; that one told it something
false about the only field it was called for.

Two layers enforcing one rule is not two implementations of one decision. A
consumer pinning its own ceiling to `workcontext.MaxTTLCeiling` reads core's
constant rather than carrying a number that can drift from it.

A verifier accepts a capability for `Skew` past its expiry, so a caller can
hold a `*Verified` that has already expired. Exchanging one is refused rather
than clamped: a capability minted with an expiry in the past would report
success and fail later, in another process, as an authentication error.
`Start` and `Child` mint at the issuer's *current* authorization revision,
read through `Authority.Revisions` — inheriting the parent's would produce a
child born superseded whenever a revocation landed in between. A `Grant`
keeps the revision the decision was made at, because bumping past it is
exactly what revokes an unspent grant.

### Scopes and the resource owner

`WorkScopeV1` is `(resource_kind, actions, resource_ids, resource_owner_id)`.
Attenuation (`workcontext.ScopeContained`) lets a child narrow a wildcard
parent to explicit ids and never the reverse, and never name an action the
parent does not hold. `resource_owner_id` selects the resources one principal
owns (an installed solution's principal), and narrows the same way: a child
may add an owner to a row that names none, never drop or change one. With ids,
a resource is covered only when both match. The owner is at most 512 bytes of
printable ASCII other than space and `*` (`^[!-)+-~]*$`, 0x21–0x29 and
0x2B–0x7E), the schema's rule, which `protovalidate` enforces at mint and at
decode. It is an allowlist on purpose: a list of excluded Unicode categories
let through characters that print as nothing or as another letter (U+034F,
U+FE0F, U+3164, U+2800, NFD accents, Cyrillic lookalikes), and Go and Python
protovalidate disagreed on unassigned code points. With ASCII, two owners that
print alike are the same bytes and every regex engine gives one answer;
`workcontext/testdata/resource_owner_vectors.json` holds the vectors both are
checked against. Principal ids are UUIDs, which fit.

A reader asks with the resource's owner. An ask naming no owner is never
covered by an owner-selected row, so an owner-blind check **built on
`ScopeContained`** (or `ScopesAttenuate`) fails closed rather than reading the
scope as the whole kind. That guarantee is the function's, not the field's. A
reader that inspects `WorkScopeV1` itself — flattening a scope with no ids to
`kind:action`, or copying kind, actions and ids into a type of its own — reads
an owner-selected scope as the whole kind once its core pin knows field 4,
because the unknown-field refusal that used to reject the whole token no longer
fires. Until such a reader compares owners it must refuse, or grant nothing
from, any scope with a non-empty `resource_owner_id`, and an issuer must not
mint an owner-selected scope to its audience. The readers known to inspect
scopes directly, read at each repository's main on 2026-10-25 (reviews r109
and r128). It is the list known so far, not a proof that no other exists:

| Reader | Where | Owner-blind because |
| --- | --- | --- |
| authority hop library (Go) | `libraries/hop/go/hop.go` (`flatten`) | stamps `kind:action` for every scope without ids, so `Identity.Has` widens; authority #18 (open) skips owner-selected scopes |
| authority client (Python) | `authority_client/work_context.py` (`Scope`, `scope_contained`) | the `Scope` tuple drops the owner and containment ignores it; authority #18 (open) keeps the owner |
| authority hop (Python) | `codefly_hop` (`_flatten`) | same flattening as the Go hop; changed in authority #18 (open) |
| authority gateway issuer | `modules/authority/services/gateway/code/pkg/business/issuer/issuer.go:98-118`, `state.go:1156` | a held scope with no ids sets `whole`, which returns every grant (or the human owner's) for the kind |
| module-annotations | `pkg/infra/workcontext/authority.go` | reads kind, actions and ids field by field |
| module-evaluation | `identity.go` | reads kind, actions and ids field by field |
| module-record-store | `adapters/operations.go` | reads kind, actions and ids field by field |
| module-document-store | `modules/documents/services/documents/code/pkg/business/workclaims/claims.go:191-199` | a matching scope with no resource ids returns allow |
| module-runtime | `pkg/runnable/installation.go:225`, `pkg/infra/accounts/authority.go:297` | copies kind, actions and ids into its own `Scope` / `WorkContextScope` and drops the owner |
| saas-sdk-python | `src/saas_sdk/work_context.py:158-163` (`WorkScope`), `:638-646` (`_scopes_of`), `:322-327` (`require_scope`) | `WorkScope` has no owner, `_scopes_of` drops it, and `require_scope` allows the whole kind. It refuses the unknown field today, so it widens once it regenerates from this core |
| module-saas-starter, module-saas-starter-security accounts | `module/services/accounts/code/pkg/adapters/` (`work_context_rpcs.go`, `delegated_read_audience.go`) | copy kind, actions and ids into their own types and drop the owner |
| cli execution recorder | `pkg/executionrecorder/work_context_authority.go` | reads kind, actions and ids field by field |

The authority's `Claims.Allows` builds on `ScopeContained` and fails closed,
but its issuers do not use it: the gateway issuer above reads scopes itself.
Authority #18 (open, not merged when this was written) makes the local issuer
refuse, by name, an issue request or a delegation whose scopes name a resource
owner ("this authority issues no owner-selected scope yet"). That refusal
covers what the local issuer is asked to mint; it does not make the gateway
issuer's resource-grant path owner-aware.

Runnable operations never carry an owner: not in the owner's declaration, not
in an installation's binding, not in a composition's scope-slot selection —
each refusal says which, and says `owner-scoped parent`. The owner comes from
the capability the operation is invoked under. A runtime minting an operation
child under an owner-scoped parent passes the operation's owner-less scopes
through `workcontext.InheritResourceOwner(requested, parent.EffectiveScopes())`
and mints the child with the result: a scope the parent covers as written is
kept, one the parent covers only under an owner gets that owner, and it is
refused — never guessed — when no owner covers it, when several do (mint one
child per owner), when only several owners' rows together cover it (split it
by owner; the refusal says so rather than "not covered"), or when the scope
already names another owner. An inherited owner applies to the whole scope:
under the parent `[doc:read, doc:write@P]` the operation scope
`doc:{read,write}` becomes `doc:{read,write}@P`, so the child reads only P's
documents although the parent reads all of them. That is intended and fails
closed — an operation invoked under an owner-scoped capability is confined to
that owner's resources — and `TestInheritResourceOwner` pins it. Minting the
operation's scopes as written under an owner-scoped parent is refused by
`Child` as a widening, because an owner-less child is never covered by an
owner row.

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
with tests (`workcontext/grant_test.go`):

- a grant capability is rejected for any other tool, subject, request or
  approver set, and on a second use;
- a rejection for any other reason does not consume the single use;
- the parent session is untouched — the agent still holds it, with the scopes
  it always had, and merging is approvable again rather than allowed;
- a grant capability delegates nothing further;
- bumping the issuer's `authorization_revision`, or revoking the grant,
  invalidates an unused grant capability.

## Audit

A delegation chain lists the lenders,
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
attested. The requirement lives where the identity is built, never in the
schema. Minting always fills them.

## The seal: one installation, and the principal's epoch

A capability's scopes say what it may do. The **seal** says which installation
it was minted against and which epoch of its principal, so that authority
cannot outlive either one. `WorkSealV1` is on every capability and carries three
fields, read from `SealSource.Seal` for the installation and
`SealSource.PrincipalEpoch` for the epoch:

| field | what moving it invalidates |
| --- | --- |
| `principal_epoch` | every capability minted for that principal, at once |
| `installation_id` | — it is identity, not a counter |
| `installation_revision` | every capability minted against the installation's previous terms |

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

### What a workload runs is checked where it mints, not sealed

A seal carries **no build**. Fields 4 and 5 of `WorkSealV1` and 8 and 9 of
`WorkActorV1` (`build_incarnation`, `image_digest`) are reserved: they sealed one
approved build per principal into every capability and made every verifier ask
the issuer for that one build. A module that runs as more than one workload —
each its own build — could be approved for one of them only, so its other
workloads could never mint.

Build approval then left the runtime path altogether: a module manifest
carries no digest ([module-manifest.md](module-manifest.md)), and "only approved
images run" is deploy-time admission. What remains is kept by the parties that
own it:

- **The host checks who is calling at the mint, not what it runs.** It attests
  the caller's workload (core `workloadidentity`), finds the one workload of one
  applied module manifest that the attested account names, and mints only for
  an organisation with an active installation of that module.
- **A redeploy revokes through the seal's own counters.** For a module
  principal the issuer derives the installation revision from the whole grant —
  its workloads and the manifest generation included — and advances the
  principal epoch with the applied generation, so a redeploy invalidates every
  capability the replaced generation held, owner position and actor hops alike.


### Where the binding's incarnation comes from — a seam core does not close

`OperationBinding.Revision` changes when a binding's terms change;
`Incarnation` changes when a binding is withdrawn and re-created under the same
ID. **Nothing in core derives either.** An issuer holds them, and the only
rules core states are the monotonicity ones: both only advance, a withdrawal
is terminal, and a reassignment between principals advances the revision.

This is worth naming because the manifest generation in `solutionhost` is
*derived* from the delivered document, so a reader may assume a binding's
counters are too. A binding has no document — there is no carrier for one — so
the issuer is the only source. A host that re-creates a binding under a reused
ID without advancing the incarnation re-admits every capability sealed to the
old one, and core cannot detect it, exactly as it cannot detect a rewound
approved-build digest.

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
to read alike.

A capability carrying no seal at all, or an actor hop carrying no epoch, is
`ErrInvalid` — **the schema refuses both**. They were optional fields once,
enforced only by the verifier, on the stated grounds that a schema rule would
invalidate every archived capability and every execution receipt embedding one.
Two independent reviews named that for what it is: a backward-compatibility
hedge, which the rules in force forbid. It also meant every reader other than
the verifier saw a capability's binding to its installation as optional, so the
strongest check in the model was the easiest one for a consumer not to notice.
Archived capabilities and receipts are historical **data**; if they must be read
after this they get a snapshot type of their own, rather than the live
credential staying permanently weaker than it should be.

The dedicated `ErrUnsealed` sentinel is **deleted** with that change. Once the
schema requires the seal, no branch can produce it — `protovalidate` runs
before anything that could — and a sentinel no branch can produce is worse than
no sentinel at all, because a consumer writes a handler for it and the handler
never runs.

### Minting reads the seal; it never accepts one

`Authority.Seals` is required, and `StartInput` names only the
`InstallationID` (identity, which is the caller's to supply) and optionally an
`OperationBindingID`. Every counter is read from the source. A minter that
accepted the numbers would hand out capabilities nothing verifies, and the
failure would surface in another process as an authentication error rather than
at the mint that caused it.

`Child` and `Grant` **carry the parent's seal forward and refuse the derivation
if any of it has moved.** This paragraph said they *reseal against the issuer's
live state*, which is the laundering an adversarial review reproduced: a parent
refused at revision 7 against an issuer at 8 still produced a child carrying 8
that verified. Restamping current counters onto a derived capability is how
deriving defeats revocation. `carryForwardSeal` runs the verifier's own
`checkSealAgainst` over the parent's claims rather than a subset of it, and
`carryForwardRevision` does the same for the revision.

The EXECUTION is the exception, and it is not restamping: each link attests its
own. The seal carries the owner's; every actor hop carries its own on
`WorkActorV1`, exactly as `principal_epoch` does. One slot on the seal let a
derivation overwrite the owner's execution with the last hop's, so superseding
the owner's build refused the owner's own capability while every child of it
verified — see "The execution belongs to the principal" above.

The installation is always the parent's: a delegation hop narrows authority
within one installation and never moves it, so taking an installation from the
hop would be a way to widen across installations. `Grant` takes a
`context.Context` for this reason.

**The seal and every hop's epoch are `required = true` on the wire.** This
paragraph argued the opposite — that they are optional, with the requirement
living in the verifier, because a schema rule would retroactively invalidate
archived capabilities and the receipts embedding them. **Two independent
reviews called that a compatibility hedge of exactly the kind the rules
forbid, and they were right.** Archived receipts are historical data: if they
must stay readable they get a snapshot type, rather than every live credential
carrying a permanently weaker schema so old bytes still parse.

So the fields are schema-required and `ErrUnsealed` is **deleted** — a
sentinel no branch can produce is worse than no sentinel. The two
`executionreceipt` goldens moved with it, deliberately, and the test says why.


### One implementation, and the gate that keeps it that way

**A wire contract has exactly one implementation, in the repository that owns
the type.** The Work Context is a core proto, so core's `workcontext` — the
deterministic proto marshal, Ed25519 over those bytes — is the only mint and
the only verify. Nothing else may sign, verify or re-encode one.

That rule is written here because it was broken here. A second implementation
in another repository signed a hand-written snake_case JSON payload. Both forms
are `<base64url payload>.<base64url signature>` with an Ed25519 signature, so a
token from either looked structurally fine to the other and then failed
**signature** verification — and `signature does not verify under key X` reads
like a key-rotation or trust-root problem, which is what was investigated while
the actual problem was two encodings.

Three things in this package exist because of that, and none of them is a
comment:

**1. A foreign encoding is refused before the signature, with its own error.**
`ErrNotACoreToken`, never `ErrInvalid` and never a signature failure. The check
is certain rather than heuristic: the first byte of a proto3 encoding is a field
tag, and `{` (0x7b) is field 15 with wire type 3 — the start-group type proto3
does not emit and `Unmarshal` refuses. `[` (0x5b) is field 11, the same. So
neither byte can begin a `WorkContextV1`, and a payload beginning with either is
another format rather than a damaged one of ours. An *empty* payload is
deliberately **not** this error: "not a core token" means "this is another
format", and widening it to cover a malformed token of no format would make it
mean "something was wrong early".

The discrimination is exported as `CheckEncoding(payload []byte) error` — the
base64url-decoded claims, not the token — so that every decoder in the fleet
names the same condition with the same error rather than each inventing a
weaker message for it. A nil return means only "not visibly another format": it
asserts nothing about validity and authenticates nothing.

**2. `workcontext.Fixtures(now)` is the conformance kit, as code.** For every
token form this package mints — session, operation, delegated, delegated
operation, grant — and for every way one is refused, it returns the signed
token, the outcome a conforming verifier must reach, the sentinel a refusal must
match, and, where that sentinel is an umbrella, a substring the message must
contain. `ErrInvalid` covers several unrelated refusals, so the fixture that
exists to be the one *signature* failure names it; without that it would pass
for any invalidity.

Count the kit with `len(Fixtures(now))`. A count written in prose goes stale,
and this one did — the PR body said 18 while the builder produced 21, caught by
a consumer measuring it. The negatives cover a stale installation revision, a
revision *ahead* of the issuer's, a superseded principal epoch, a replaced build
incarnation, an installation the principal does not hold, a binding at a
revision the issuer does not hold, a missing seal, a seal naming no
installation, a tampered payload, another audience, an unknown key, four
malformed shapes — and the look-alike.

Tokens are minted fresh on each call rather than committed as bytes: a
capability carries a validity window, so committed bytes would expire and the
kit would rot into a test that fails for the wrong reason once a year. What is
fixed is everything a verifier is configured with — the keypair, the issuer, the
audience, the identities, every sealed value. **The fixture private key is
public by construction**, derived from a seed written in the source; it signs
conformance tokens and nothing else.

Two fixtures are worth knowing by name. `tampered-payload` is a sound capability
with one byte changed and the signature left alone — that is what a signature
failure is *for*, and it is the one fixture that must report one.
`foreign-encoding` is a genuinely signed JSON-payload token, and its signature
is real precisely so that a verifier checking the signature first would still
refuse it — and would refuse it with the wrong error. The two together are what
make the distinction load-bearing rather than cosmetic.

**3. `workcontext/conformance.Run(t, verify)` is run by the consumer, not by
core.** A consumer passes its own verification entrypoint; the helper drives
every fixture and fails the consumer's build if any outcome differs.

```go
func TestWorkContextConformance(t *testing.T) {
    verifier := conformance.New(now).Verifier() // core's, configured for the kit
    conformance.Run(t, func(ctx context.Context, token string) error {
        _, err := myPackage.VerifyWorkContext(ctx, token)
        return err
    })
    _ = verifier
}
```

`conformance.New(now)` returns the `Settings` an entrypoint must be configured
with — issuer, audience, keys, revision source, replay store, seal source, grant
source, clock — and `Settings.Verifier()` assembles core's `Verifier` from them,
which under the one-implementation rule is what every consumer's entrypoint
resolves to. `Run` also presents the single-use fixture twice, so a verifier
without a working replay store passes everything else and fails there: single-use
is a property of the verifier, not of the token.

**The gate lives in the consumer because core cannot see who re-implements it.**
A consumer that runs core's fixtures against its own entrypoint demonstrates
that it BEHAVES like core's verifier — which is not the same as proving it IS
core's. These fixtures check accept/refuse decisions and each refusal's named
reason, so an equivalent second implementation could pass them. Implementation
identity is proved by the consumer's import gate (an alias of core's `Verifier`
pinned by a compile-time assertion, plus a CI check that the module contains no
second signing or verifying implementation), which lives in the consumer. The
fixtures are the cheaper half of the gate, not the whole of it. A consumer that has quietly grown a second implementation cannot
pass the look-alike fixture, because refusing a foreign encoding before the
signature check with a distinct error is the one behaviour a re-implementation
never thinks to copy — it is the behaviour that exists *because* the
re-implementation happened.

The consumers' half of this is theirs to land: a client SDK deletes its mint,
verify and JSON payload outright and keeps client plumbing only, with a CI gate
that fails on a second implementation; a host switches its call sites to this
package and runs the conformance helper in its own suites. The
cutover is cold — every token changes format at once, and an old token is
refused with `ErrNotACoreToken` rather than accepted.

### One encoding, enforced at the wire

`Verify` used to accept **any** protobuf the key had signed. The signature
covers whatever bytes were presented, so a second minter emitting a
different-but-valid encoding of the same claims — or one adding a field of its
own — verified, and passed the whole conformance kit. The one-implementation
rule was asserted in prose and unenforced where it mattered most.
`solutionhost` had enforced exactly this for its documents all along, with
`ErrNotCanonical`; the capability had no equivalent.

Two checks now sit in the single decode path, so `Verify`, `Authenticate`,
`Inspect` and `Decode` all get them:

- **No unknown fields, recursively.** A capability carrying a field this Core
  does not know was written by some other minter. The kit's `unknown-field`
  fixture nests one inside the *seal* rather than at the top level, because a
  root-only check would pass it.
- **The payload must BE its own canonical encoding.** Re-marshalled
  deterministically and compared byte for byte.

The authorization revision is now compared for **exact equality** too, not
`>=`. The argument was already in the package, written about the seal — "there
is no legitimate way to hold one; the shapes that could produce it are a
rolled-back installation and a forged seal" — and it applied to one lever
while the other accepted a capability claiming a revision *ahead* of the
issuer's.

### The use site's half of the binding check

A verifier checks a sealed operation binding thoroughly — revision,
incarnation, withdrawal, and that it is granted to the exercising principal
within the sealed installation — but only when the capability carries one. A
capability carrying the same authority scopes and **no** binding passed every
check, so revoking the binding did not reach it.

Core does not close that by requiring a binding on every capability: a session
carries the owner's delegated authority and a binding gates one *operation*, so
making it mandatory would collapse the two forms and force every session to
name a unit of authority it does not exercise. Instead `(*Verified).RequireBinding(id)`
lets the place that knows the gate say so. It is a method on the capability
rather than a verifier option because the requirement belongs to the **call**:
two operations behind one verifier legitimately require different bindings.

### Two entrypoints, one implementation, one strength

`Verifier` is issuer-shaped: it requires a revision source, a replay store, a
grant source and a seal source, all four load-bearing. A party that verifies
without minting — the host's gateway — holds the sealed state and the replay
store but not the approvals engine's records, and generally cannot read the
issuer's authorization revision live. It needs a verify-only entrypoint, and
the question is how to have one without having two *strengths*: a consumer
reaching for the weaker one where the stronger was needed is a silent
downgrade, and that is the same class of failure as the two encodings, which
also looked fine right up to the point where it mattered.

`Authenticator.Authenticate` is that entrypoint, and it is built so the second
strength does not exist:

- **It is not a second implementation.** `Authenticate` assembles a `Verifier`
  and calls `Verify`. Every rule is applied by the same code in the same order,
  so a change to a rule reaches both entrypoints or neither. A test holds all
  of core's fixtures against both and requires the refusals to be identical
  *including the message text*, which is a stronger claim than identical
  outcomes: two implementations can agree on accept/refuse for every case
  anyone thought to test and still differ on which rule fired, and that is the
  failure `ErrNotACoreToken` exists because of.
- **What it lacks becomes a refusal, not a skipped check.** It holds no grant
  records, so a capability carrying a grant hop is refused with
  `ErrNeedsIssuer` — a routing fact ("present this to the party that holds
  those records"), not a judgement about the capability. Returning a
  synthesised grant that matched whatever the capability claimed would make the
  hop self-authorizing, and would be reached by a verifier that looked entirely
  correct.
- **It is a `Verifier` minus exactly one source.** An earlier draft also
  replaced `Revisions` with a stated `uint64`, arguing that a party which does
  not mint cannot read the issuer's revision live, so a number put that limit
  where a reviewer sees it. **That was wrong, and a consumer refused it with
  the evidence.** `RevisionSource` is per *tenant* by its own signature, so one
  number against a multi-tenant issuer either refuses every tenant not at it,
  or — the dangerous one — goes on accepting capabilities minted at a
  *superseded* revision for every tenant except the one it names. The field
  read as if revocation were enforced while enforcing it for at most one
  tenant, which is the class of defect this package exists to remove. A
  single-tenant caller supplies `FixedRevision`, whose own documentation says
  it answers one number for every tenant — a better place for the posture to be
  legible than a bare field.
- **`Seals` is still required, and there is no seal-less mode in any
  entrypoint.** Sealed state is not the issuer's bookkeeping: it is the live
  binding of the presented capability to one installation, one epoch, one build
  and one operation binding, and it is what makes every revocation here reach a
  capability already in flight. A caller holding none cannot authenticate at
  full strength, and core does not offer it a way to appear to.
- **The result type carries which question was answered.** `Authenticate`
  returns `*Authenticated`, never `*Verified`. `Authority.Child` and
  `Authority.Grant` take a `*Verified`, so an authenticated capability cannot
  be exchanged for a derived capability — which is right beyond
  types, since deriving holds the parent's inherited seal against live state and
  that is the issuer's to answer. There is no conversion in either direction and
  a test guards against one being added, because that addition is the whole
  downgrade in one function.

`conformance.RunAuthenticator` is the kit's second mode, and it is **stricter**
than `Run` rather than more lenient. For every fixture that does not need the
issuer's own records it requires the same outcome and the same named reason; for
the fixtures that do, marked `NeedsIssuer` in the kit, it requires a refusal
naming `ErrNeedsIssuer`. That last assertion is the one a downgraded
authenticator fails, because an entrypoint that accepted an approval it never
checked *accepts* those fixtures. A kit that simply passed a verify-only
verifier unchanged would have been certifying the downgrade.

What remains a seam, stated rather than implied: a consumer that holds no
sealed state at all still cannot verify at full strength, and nothing here
changes that. The answer for such a consumer is not a weaker core entrypoint.
If it forwards, it inspects (next section); if it acts, it is a callee and
needs a callee's inputs.

### The forwarding hop inspects; the callee verifies

A **forwarding hop** — the host's gateway, any proxy that routes a capability
to its callee — is a third party with a third shape, and the two entrypoints
above both fail it. `Verify` consumes a single-use nonce as its last step, so a
gateway that verified in flight left the callee refusing a legitimate
capability as replayed; and both entrypoints require the issuer's live state,
which an edge holding a JWKS cache and a routing table does not have. A host
measured exactly that and kept a hand-written signature check at its edge: a
second implementation of the wire contract, because core offered nothing
shaped like an edge.

`Inspector.Inspect(token, audience)` is the entrypoint shaped like an edge, and
it is built the way `Authenticate` is so that it is not a third **strength**:

- **One body.** `Inspect` assembles a `Verifier` shell from the trust root and
  the route and runs `(*Verifier).inspect` — the exact prefix `Verify` runs
  before it reaches for live state: the decode path (shape, encoding, schema,
  the lifetime bound, no unknown field, the canonical encoding), the issuer
  pin, the signature under the key the capability names, the audience, the
  window, the chain's attenuation and the grant hop's shape. A test holds every
  fixture a hop can see to `Verify`'s refusal including the message text; a
  second body would break it.
- **The route target is the hop's, never the token's.** `audience` is a
  per-call argument because a hop serves many routes and because of where the
  value must come from: the target the hop resolved from the request it is
  routing. Reading the audience off the token and comparing it to itself
  passes everything, and so does an empty expectation treated as a wildcard —
  a host found the second one in its own gateway, as an empty-string sentinel
  meaning "do not check". An empty route target is refused by name, and the
  kit's `empty-audience` fixture holds that no capability can match one: the
  schema refuses an empty audience in the decode path, before any comparison.
- **What it cannot see, it forwards.** The authorization revision, the sealed
  installation's revision, every principal's epoch, an
  operation binding's revision, incarnation, withdrawal and association, and
  the issuer's record of a grant are the callee's to hold a capability
  against. A hop that refused on any of them would be claiming a check it
  cannot make; one that answered them from a cache would be a second verifier
  with stale state. So the kit marks every such fixture `NeedsLiveState` — a
  test pins that this is exactly the set refused with `ErrRevoked` — and
  requires `Inspect` to **accept** each one. A grant capability is forwarded
  too. `Authenticator` refuses one with `ErrNeedsIssuer` because an
  authenticator *acts* on what it accepts; a hop does not act, so for a hop
  the forwarding is the deferral.
- **It consumes nothing.** There is no replay store to consume with and no
  nonce is read: `Inspector` has no field through which it could consume, read
  a revision, resolve a grant or compare a seal, and a test holds the struct's
  shape by reflection. A single-use capability inspects every time it is
  presented, and the callee burns it exactly once. **One consumer per
  capability**: the hop inspects, the callee verifies.
- **It grants nothing and derives nothing.** `*Inspected` carries only what a
  hop routes on — the audience it was checked against, the tenant, the sealed
  installation, the task and the session, every one a bare string — and no
  function in `workcontext` or `policy` takes it. The AST guard that keeps an
  `Authenticated` from becoming a `Verified` covers it too. Nothing in it is
  for stamping onto the forwarded request: the callee verifies the token
  itself, and a header a hop writes is a header a caller can write.

`conformance.RunInspector` is the kit's third mode, and it is stricter in both
directions. Every fixture a hop can see must reach the full verifier's outcome
with the same named reason. Every fixture refused only against live state must
be accepted. The single-use fixture is presented twice and must inspect both
times, then core's verifier built from the same settings — the same replay
store the consumer was handed — must accept it once and refuse it the second
time: the first acceptance proves the hop consumed nothing, the second refusal
proves the first was not vacuous. And a sound capability presented for a route
it is not addressed to, and for no route at all, must both be refused. `Verify`
and `Authenticate` both fail the mode, correctly, and tests hold that they do.

**Why `Seals` and `Revisions` stay mandatory on `Verify` and `Authenticator`.**
The hop's entrypoint is not a relaxation of either. Those two are *consumers*:
a consumer that acted on a capability it had not held against live state would
be acting on a revoked one, and a seal-less mode on either would be a flag that
turns the strongest check in the model off for whoever sets it. The hop needs
no such mode because it acts on nothing. Core keeps one strength per question:
a callee verifies at full strength or not at all, and a hop inspects.

**What this unblocks, and what it does not.** A host whose edge held only a
JWKS cache could call no core entrypoint; it can now call this one and delete
its own signature check. That is the prerequisite for the host's cutover from
a JSON-signing SDK to this encoding — a cutover that is *atomic* (core refuses
a JSON token as `ErrNotACoreToken` before the signature, so there is no
dual-read window) and is the host's to make, in one move for its mint and
every verifier. Core's encoding does not change for it.

### The issuer reads back; it does not consume

A fourth party asks a fourth question. A capability's **holder** — usually the
consumer that has just verified it — asks the **issuer** something about it:
"may this capability read record R?", "which collections does it reach?",
"page two of that listing, please". The capability is the subject of the
question, not a credential presented for an effect. `Verify` cannot answer: it
consumes a single-use capability, so the consumer that verified first could no
longer ask, and a paginated answer died on its second page — a host shipped
exactly that. `Recheck` cannot answer either: it needs the `*Verified` only the
consumer holds.

`(*Authority).ReadBack(ctx, token, ReadBackCheck{Audience, Grants})` is that
entrypoint, and it is again one body, not a new strength:

- **Everything `Verify` checks, by `Verify`'s body.** `Verify` and `ReadBack`
  share one function; the only argument they differ in is whether the
  capability is consumed. The signature is checked under the authority's own
  key and key id, the audience is the one the caller names, and the revision,
  seal, epochs, execution, binding and grant record are held against the
  authority's live state. A test holds that every refusal is `Verify`'s, with
  the same message.
- **It consumes nothing, structurally.** No replay store is reachable from a
  read-back: a single-use capability its consumer already consumed can still
  be asked about, and one not yet consumed is left for its consumer, whose
  `Verify` then consumes it exactly once. A test asserts both.
- **Only the issuer can call it.** It is a method on `Authority`, and an
  `Authority` is built with the issuer's private key or signer. A consumer
  holds public keys and cannot construct one, so the question of a consumer
  "reading back" instead of verifying does not arise.
- **It authorizes nothing.** The result is a `*ReadBackCapability`: its claims
  are readable, and nothing else is available. It is not a `*Verified`, so it
  cannot be passed to `Child`, `Grant` or `Recheck`, and an AST test keeps any
  function from converting one. This is why it is not a replay hole: single-use
  bounds how often a capability is *presented and acted on*, and a read-back is
  the issuer reading its own signed statement back to answer its holder. An
  issuer that performed the capability's effect on the strength of a read-back
  would have used the capability without consuming it — that is a misuse of
  this entrypoint, and the reason it returns a type that can do nothing.
