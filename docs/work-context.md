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
`Verify`, `Authenticate` and `Inspect` all answer it identically and with one
message. It was first written into `Verify` alone — in the same change whose
comment says a bound belongs in the one decode path and not in one entrypoint —
and the caller that paid for that was the one which never verifies a signature
at all: a **mint client** reads its own window through `Inspect`, so a
capability every receiver refuses was reported to its holder as thirty days of
validity. A missing bound tells a caller nothing; that one told it something
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

`build_incarnation` and `image_digest` are **optional and paired** — absent for
a principal that bears no execution. See "A principal that bears no execution"
below; they are read from `SealSource.ApprovedBuild`, keyed on the principal,
not from the installation seal.

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

### The execution belongs to the principal, not to the installation

`SealSource.ApprovedBuild(ctx, principalID) (digest string, incarnation uint64, err error)`
answers the execution the issuer approves for ONE PRINCIPAL. `Seal` answers
installation state and nothing else.

They were one method, and the split is the fix for a blocker rather than a
tidy-up. The execution was read from the **owner's** installation record, so a
capability's execution binding described the owner's workload however many
delegation hops had been added and whoever was actually exercising it. And a
delegated hop's principal does not hold the owner's installation at all — so
there was nothing a derivation could have been attested against even in
principle. The consequence: **minting was execution-bound only at `Start`.** A
caller holding a parent capability derived children whatever it was running,
which is the threat the execution fields exist to stop, left open at every hop
but the first.

So `Child` and `Grant` take an `Execution` too, required on the same terms as
`Start`'s, and the seal's execution describes whoever will EXERCISE the
capability. `ApprovedBuild` takes no attribute of the workload, deliberately:
resolving by (service account, image digest) would answer "approved" for
whatever a superseded pod presents, which is the hole it exists to close.

### A principal that bears no execution

`ApprovedBuild` returns `ErrNoApprovedBuild` when a principal bears no
execution. **That is an answer, not a failure** — it is the correct answer for
a human session, because a person at a terminal runs no approved build.

`build_incarnation` and `image_digest` are therefore `optional` on the wire,
set as a pair or not at all (a schema message rule, not a convention). This is
**not** the compatibility hedge two reviews rejected for the seal itself and
for the actor epochs: that hedge made a field optional so ARCHIVED data would
still parse, trading every live credential's strength for old bytes. This is
optional because the field does not APPLY to a whole class of principal, and
requiring it of a human would force every human session to invent a value —
which is exactly what the field exists to refuse.

The requirement is not weakened, it is made conditional on something the issuer
knows and the schema cannot. A capability carries an execution **exactly when**
its exercising principal bears one, and the verifier enforces the
correspondence in **both** directions:

- an execution-bearing principal whose capability carries none is refused;
- a principal that bears none whose capability carries one is refused too,
  because that is a process claiming to be a workload.

Only the first direction is obvious, and checking only the first is what would
make the optionality a hole: a human's capability could then be sealed to a
build nobody approved for it, and nothing would object.

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
    verifier := conformance.Verifier()      // core's, configured for the kit
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

### The one requirement that rests on the implementer

`WorkSealV1.image_digest` is the **approved** build the issuer holds;
`StartInput.Execution.ImageDigest` is what the caller **attests it is
running**. The mint refuses unless they match, which is what binds a
credential to one execution and refuses a pod from a superseded generation.

**A host that fills the attested execution from the same record it sealed has
written a tautology.** The mint then compares approved against approved, so the
check passes for every caller — including the superseded pod the field exists
to refuse. The defect it closes is reintroduced by the field that closes it.

**Core cannot detect that.** It cannot know where a caller got the bytes, both
fields are strings, and the tautology type-checks. This is the one requirement
in the model that rests on the implementer rather than on a check, and it is
written down here because the first implementer reported that the shortcut was
the only option available to it — the approved digest, from the admitted
presence document, is the only image digest its host can currently reach. That
is how the shortcut gets taken: not carelessly, but because the correct source
needs workload authentication that is not running yet.

So the test that matters is **not** "does a sound execution mint". It is: take
a pod from a **superseded generation** and confirm the mint refuses. If that
passes while the approved build is the only digest the host can reach, the
digest is being read from the wrong place.

### One encoding, enforced at the wire

`Verify` used to accept **any** protobuf the key had signed. The signature
covers whatever bytes were presented, so a second minter emitting a
different-but-valid encoding of the same claims — or one adding a field of its
own — verified, and passed the whole conformance kit. The one-implementation
rule was asserted in prose and unenforced where it mattered most.
`solutionhost` had enforced exactly this for its documents all along, with
`ErrNotCanonical`; the capability had no equivalent.

Two checks now sit in the single decode path, so `Verify`, `Authenticate` and
`Inspect` all get them:

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
  returns `*Authenticated`, never `*Verified`. `policy.PrincipalFromWorkContext`
  takes a `*Verified`, so an authenticated capability cannot become a
  `Principal` by accident; `Authority.Child` and `Authority.Grant` take one too,
  so it cannot be exchanged for a derived capability — which is right beyond
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
changes that. The answer for such a consumer is that the gateway verifies and
the consumer checks what it can, documenting what it does not — not a weaker
core entrypoint.
