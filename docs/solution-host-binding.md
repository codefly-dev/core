# The static half: presence, authority, and the signature both travel in

A solution or a module is **present** on a host because delivery declared it,
and holds **authority** because a reviewed, signed document granted it to one
exact build. Never because its process announced itself, and never for a build
other than the one approved.

[`solutionhost/`](../solutionhost) is where that is written down. It holds two
document types, the signature envelope around them, and the rule that binds the
two together:

| | declares | parser |
| --- | --- | --- |
| `SolutionHostBinding` | **presence**: one deployment instance of one solution *or one module* on one host, at a generation, naming the workloads it runs, the exact build each must be running, and the identity each must present | `Parse` |
| `AuthorityDocument` | **authority**: which bindings a principal holds, within a ceiling the caller supplies, effective for one approved build and from one presence generation onwards | `ParseAuthority` |
| `Signed` | the detached signature over either one's canonical bytes | `ParseSigned` |

Core owns the types, their validation and their verification. It does not
render one (`codefly-dev/cli`), reconcile one (the host in
the host that reconciles these documents), **sign** one, or observe what
actually runs.

## The two halves that never straddle

**Static, deploy-time** is this package: both documents are
generation-versioned, signed out of cluster, and grant no organisation
anything.

**Dynamic, runtime** is the host's: the envelope (the ceiling a platform
administrator writes), an installation (one organisation's use of a module,
with a scope ⊆ the ceiling and a revision), and team exposure. Core needs only
their *identities and revisions*, so that a document can be held against them
and a credential can seal them — see [work-context.md](work-context.md).

An `Envelope` is therefore always **supplied by the caller** and never read out
of a document. An envelope a document carried would be a document declaring its
own ceiling.

## One schema, and no v1 to fall back to

Each document declares exactly one schema this package reads:
`codefly/solution-host-binding/v2`, `codefly/solution-authority/v1`,
`codefly/solution-host-signed/v1`.

The presence schema moved from v1 to v2 and **v1 is not accepted**. A document
that omits the build a workload must be running, the identity it must present,
and the domain its delivery speaks for is a document a host can verify nothing
against; keeping a reader for it would leave that weaker shape permanently
available to anything that can write a delivery document. A v1 document fails
with `ErrSchema` — a loud version skew, in the one place that can fix it — and
`superseded-schema` is shipped as a fixture so both consumers pin the refusal
rather than discover it.

The schema is checked **before** the strict decode, and that order is
load-bearing: an older document has fields this one does not, so a strict decode
would refuse it for an unknown field and tell the caller its document is
malformed. It is not malformed; it is older than the reader.

Two consequences of the schema step, both deliberate:

- `SchemaV1` is gone as a constant. Pinning it was the contract; so is its
  removal.
- **There is no in-place migration for `Applied`, and core provides no
  mechanism for one.** An earlier draft of this document claimed a host could
  "treat stored digests as stale"; that was wrong, and the review that caught it
  was right. A v1-era record fails the new required-`Domain` check, and
  supplying a domain does not rescue it: the stored digest was computed over v1
  canonical bytes, so `decide` sees the same generation with different bytes and
  returns `ErrRewrittenGeneration` — an error that accuses delivery of
  tampering.

  The required-`Domain` failure itself now answers `ErrAppliedUnusable` rather
  than `ErrInvalid`, because the two accuse different parties and the wrong
  accusation sends the reader to the wrong repository. A host that has not yet
  persisted the column gets an error naming its own stored state, which is the
  only thing that can repair it: applied state is read once for the whole set,
  so one unusable record withholds **every** binding on every pass,
  indefinitely and without crashing. `AppliedFrom` fills the field, so a host
  that builds the record with it rather than by hand cannot reach this.

  The cutover is therefore cold on the host's side too: a host **discards its
  applied records** and re-admits the delivered set. That is safe here, and only
  here, for a specific reason — every v2 document is new, and a v1 document is
  refused outright by `ErrSchema`, so there is no older generation left that
  discarding could let back in. What discarding does lose is core's record of
  which generation was applied and which bindings were tombstoned; a host that
  keeps registry history of its own (late-heartbeat refusal, withdrawal records)
  keeps that separately and must not discard it. Core's `Applied` is a
  reconciliation input, not the host's audit log.

## The presence document

```yaml
schema: codefly/solution-host-binding/v2
kind: solution                     # or module — declared, never inferred
binding: alpha-region-a-01          # stable ID of one deployment instance
generation: 4                      # strictly monotonic per binding ID
ownership_domain: alpha              # the slice of the host's binding space this delivery speaks for
envelope_revision: 7               # the ceiling it was validated against
host:
  coordinate: example/prod/region-a
  component: solution-host
release:
  publisher: example
  name: alpha
  version: 1.4.0
  digest: sha256:…                 # REQUIRED
routes:
  - {alias: alpha, surface: frontend}
artifacts:                         # every rendered artifact, digest required
  - {surface: frontend, name: web, release: example/alpha@1.4.0, digest: "sha256:…"}
  - {surface: backend,  name: api, release: example/alpha@1.4.0, digest: "sha256:…"}
workloads:                         # what the host runs, and what must be true of it
  - name: alpha-api
    artifact: api                  # the artifact that renders it, by NAME
    container: api                 # the ONE container that authenticates
    image:
      repository: registry.example/alpha-api   # no tag, no digest
      digest: sha256:…             # the OCI image MANIFEST digest: the approved build
    identity:
      audience: https://prod.region-a.example/solutions
      subject: system:serviceaccount:alpha-region-a-01:alpha-api
      spiffe_id: spiffe://prod.region-a.example/ns/alpha-region-a-01/sa/alpha-api
    non_authenticating: [envoy, migrate]   # must never be accepted as the authenticator
modules:
  - {module: alpha, package: example/alpha-core, version: 1.4.0}
endpoints:                         # named, never addressed
  - {name: api, service: alpha, module: alpha, api: grpc, visibility: internal}
```

A tombstone is the same document with `removed: true`, a higher generation, and
no routes, artifacts, workloads, modules or endpoints. It still names the
release it removes **and the ownership domain it was applied under**, which is
what makes the removal attributable.

### Three digests, and why they are three types

A generation pins three different things, and all three are `sha256:` strings:

| type | pins | held against |
| --- | --- | --- |
| `ReleaseDigest` | the immutable release | the release an authority document approved |
| `RenderedDigest` | the rendered bytes of one artifact in the delivery repo | nothing at runtime; it records what delivery wrote |
| `ImageDigest` | an OCI image manifest — the approved build | the resolved image of a running container |

They are **distinct named types** so a mix-up is a compile error rather than a
silent one. Swapping two type-checks, matches the same digest pattern,
validates, and surfaces much later as a credential refusal on a healthy pod
with no readable cause.

**No rule in this package compares one digest to another.** The relationship
between a workload and its artifact is declared by NAME and validated as a name
reference. `Validate` additionally refuses a document in which two digests of
*different kinds* are the same string (`ErrDigestConfusion`): that would need a
SHA-256 collision between rendered bytes, an OCI manifest and a release, so
equality is evidence of a confusion, caught where a renderer assembling YAML by
hand is outside the compiler's reach.

### Workloads, and the biconditional

A present generation declares workloads **if and only if** it renders a
`backend` artifact, and `Workload.Artifact` must name a declared artifact whose
surface *is* `backend`.

Both directions matter. A generation that renders something to run and declares
no workload leaves the host with no build and no identity to hold a container
to. A workload declared by a generation that renders nothing to run it points at
nothing. The rule is derived from the document's own content rather than gated
on `kind`, which is better: a module and a solution are reconciled the same way,
and a frontend-only generation of either declares no workload and is correct.

Three further refusals, each choosing a refusal over a guess:

- `Container` may not also appear in `NonAuthenticating`. A document asserting
  both says nothing a host can act on, and the safe reading is not obvious
  enough to pick one.
- `NonAuthenticating` is always serialized, empty included. A host that cannot
  tell "there are none" from "nobody said" has to choose between refusing every
  workload and trusting any container that asks.
- `Image.Repository` may carry neither a tag nor a digest. Either would give one
  container two answers about what it runs, and the mutable one usually wins.

`spiffe_id` is validated as a SPIFFE ID and not as a name: scheme `spiffe`, a
lowercase non-empty trust domain, no userinfo, query, fragment or port, and a
non-empty normalized path. A subject that happens to parse as a URL is not an
SVID, and a trust domain alone would let every workload in the domain present
the same ID.

### The ownership domain is enforced, not merely carried

`OwnershipDomain` is what lets a module-scoped delivery express *removal*
without "remove everything else" being expressible at all. The string alone is
not the enforcement — three things are:

1. **The host declares what it accepts.** `Host.Domains` is required whenever
   `Host.Coordinate` is set, and an unstated list is an error rather than a
   permissive default. It exists because the applied record cannot bound a
   binding's *first* generation: there is nothing to compare against yet, so
   without it any delivery could claim any unseen binding ID under any domain it
   wrote and own it from then on. It is the host's to declare because core cannot
   know which delivery is entitled to a name it has never seen. A renderer
   pre-checking a set leaves both fields empty and is unaffected.
2. **The applied record's domain says who may change the binding.** A document
   arriving under another domain is refused whatever its generation —
   **tombstones included**, which is the case that matters. Otherwise any
   delivery the host accepts at all could withdraw any binding by declaring a
   higher generation under its own domain.

Both answer `ErrWrongDomain`, both are **per document**, and the domain is
inside the signed canonical encoding, so a carrier that relays a document
cannot widen it.

### "One delivery speaks for one domain" is not a rule `Admit` can enforce

A delivery that straddles two domains is authored wrong: a renderer writing one
calls `solutionhost.OneDelivery(docs...)` on the set it is about to write, and a
straddle is refused where it was authored.

A **host** does not call it on its mount. A mount is the union of however many
deliveries reached it, so it legitimately carries one domain per delivery, and
refusing that inside `Admit` would refuse the normal case the moment a second
module delivered to the same host. So `OneDelivery` is deliberately a separate
call rather than part of `Admit`, because putting it in `Admit` makes core
decide that a host's mount is one delivery — which is exactly the question the
renderer and the host have not settled between them (one ConfigMap per solution
namespace, or one tree in the host's).

### Absence is never removal, and there is no API that makes it one

An earlier draft of this document told a host to group its mount by domain and
treat a binding of that domain *absent at a higher generation* as removed. That
was wrong twice over, and a review caught it:

- It contradicts this package's own invariant two sections above — **removal is
  a generation, never an absence**. The signed tombstone is the removal
  mechanism, and it is signed precisely so that a withdrawal is attributable to
  the delivery that authored it.
- A set assembled from a mount silently omits every document that failed to
  parse. One malformed document beside a sound one in the same domain would
  have read as a *withdrawal* of the sound one — a parse error turning into a
  deletion. And nothing in a delivered set establishes that the set is
  complete, so "absent" cannot be distinguished from "not delivered yet".

The grouping helper that guidance named is deleted rather than documented away.
A host removes a binding when it admits a tombstone for it, and at no other
time. If complete-set replacement is ever wanted, the complete set has to be
modelled and signed as such before any deletion is authorized by it — which is
a new document type, not a grouping of the ones here.

## The authority document

```yaml
schema: codefly/solution-authority/v1
authority: alpha-region-a-01-authority
generation: 2                      # monotonic per authority ID
host: {coordinate: example/prod/region-a, component: solution-host}
ownership_domain: alpha
envelope_revision: 7
approved_build: sha256:…           # one exact OCI image manifest
effective_from: 4                  # the presence generation it is effective from
principals:
  - principal: principal:operator
    bindings:
      - id: binding:alpha:reconcile  # opaque to core
        revision: 3
        audience: https://prod.region-a.example/operations
        scope: reconcile
        queue: reconcile.default    # OPTIONAL
        namespace: alpha-region-a-01 # OPTIONAL
```

### "Verified" was part of a function name, and nothing more

`Parse` and `ParseAuthority` returned the same types `PresenceFromVerified` and
`AuthorityFromVerified` returned, and `Admit` and `Activate` accepted them. So a
host that never verified a carrier had no way to find out, and the ordering the
whole signature design rests on was a convention about naming.

`Admit` takes `*Delivered` now, and `Activate` takes `*DeliveredAuthority` and
`*Delivered`. Each is produced only by `VerifyDelivered` /
`VerifyDeliveredAuthority`, which take a caller-supplied `BundleVerifier`, call
it **first**, and build the document from the payload only when it accepts.
Core implements no `BundleVerifier` and never will — signing is keyless over a
workload identity, verifying is sigstore-go against an identity policy and a
trust root the verifier holds — but core owns the **ordering**, and the
unverified path is now unexpressible rather than merely discouraged. The
`workcontext` half of this same change uses `*Verified`, `*Authenticated` and
`*Inspected` for exactly this reason.

A malformed document now has three layers between it and `Admit`: it has no
canonical encoding, so `Carrier` will not wrap it, so it cannot be delivered.
None of those is a set-wide decision — which is the withdrawn `ByDomain`
guidance's invariant from the other side.

### Who may speak for a domain

A document **asserts its own** ownership domain. `Host.Domains` bounded which
domains a host accepts at all; it did not bound who may speak for one, so any
signer the host accepted could write any accepted domain and take over bindings
in it. That is the keyless form of "a document nominates its own authority",
one layer up from the key.

`Host.DomainsBySigner` maps each attested identity to the domains it may
deliver under, and is required whenever `Coordinate` is set — an unstated
policy would let every accepted signer claim every accepted domain.
`ActivationRequest.DomainsBySigner` applies the same policy to both halves of a
tuple, because activation is the other place a self-asserted domain would
otherwise be taken at its word. Core still establishes nothing about who a
signer is: the caller's `BundleVerifier` names the identity, and this says what
that identity is allowed to deliver.

### What activation requires, and the three things it did not

`Activate` takes an `ActivationRequest` — both documents, the build, **the
current envelope**, and **the applied authority record**. It is a struct
because three of those were added by review, and because a caller reading it
can see that the envelope and the record are not optional.

Three gaps, each found by review and each real:

- **The authority document named no target.** It matched on host, ownership
  domain, envelope revision and build membership only, so an authority
  document activated *any* binding in the same host and domain running the same
  image — including a replacement instance that had taken a tombstoned
  binding's alias. Worse, `Activation.Binding` was copied from whichever
  presence document the caller passed in, so the result reported a target as
  though it had been checked. `binding:` is now a required field on the
  authority document and must equal the presence document's own.
- **There was no generation fold.** The `generation` field's own comment said
  "a replayed older document is detectable the same way a replayed presence
  generation is" — and nothing detected it. Keyless signatures do not expire,
  so a genuinely signed generation N-1 re-granted everything generation N had
  withdrawn. `AppliedAuthority` and `AppliedAuthorityFrom` are the host's
  record, and `decideAuthority` folds a document against it: stale, rewritten,
  domain-moved, binding-moved and withdrawn are each refused.
- **The envelope was the caller's to remember.** Narrowing a ceiling did not
  reach activation, because `ValidateAgainst` was a separate call a caller was
  expected to make first. An ordering requirement a caller can forget is not a
  rule, so `Activate` checks the envelope it is given.

`AuthorityBinding.ID` is **opaque to core**: core compares IDs and never
derives, parses or subsets one, so an exact lookup can never quietly become a
search. That is the property the credential contract rests on. Whether delivery
derives the ID from the contract it renders is delivery's business, and
deterministic is better than random because it is stable across renders —
predictability costs nothing, since an ID is neither a secret nor a capability
and authority comes from the signed document that lists it. IDs are unique
across the whole document, not per principal: one ID naming two units of
authority would make that lookup ambiguous in the one place that must never
guess.

**`Queue` and `Namespace` are optional.** A module that owns no queue is a real
case, and requiring the field would leave every such module with no derivable
authority document at all. Absence means the binding grants **no** authority on
that dimension — never *every* queue. What makes that safe is the containment
rule below: an absent queue matches only an absent queue in the envelope, so
absence cannot widen into a wildcard, and a document naming a queue is not
granted by an envelope entry without one. Both directions are pinned by test,
because the safety of the optional field rests entirely on absence and presence
not being interchangeable.

Withdrawal is a generation here too, and a withdrawn generation names no
principal, approves no build and is effective from nothing. Carrying any of
those would be a withdrawal that still says what it grants.

### Inside the envelope means exact element inclusion

`(*AuthorityDocument).ValidateAgainst(envelope)` checks that the envelope
revisions agree, that `approved_build` is one the envelope approved, and that
every binding granted is one the envelope **holds exactly** — same ID, same
revision, and the same audience, scope, queue and namespace.

It deliberately does not mean "some envelope binding subsumes this one". A
subsumption rule is the same rule as searching the bindings for one that
contains a set of scopes, and it fails the same way: whoever writes the
predicate decides what authority means, and a predicate one case too generous
grants authority nobody reviewed. The `outside-envelope` fixture grants a
plausible *widening* of a binding the envelope does hold, which is exactly how
a subsumption rule would let it through.

A withdrawal claims nothing, so it is inside every envelope of its revision.

### Activation: neither half alone

```go
activation, err := solutionhost.Activate(solutionhost.ActivationRequest{
    Authority:       deliveredAuthority, // both halves DELIVERED, never parsed
    Presence:        deliveredPresence,
    Build:           build,
    Envelope:        envelope,           // the current ceiling, checked here
    DomainsBySigner: host.DomainsBySigner, // required
    Applied:         appliedAuthority,   // the AUTHORITY record for this binding
    AppliedPresence: appliedPresence,    // and the PRESENCE record for it
})
```

A matched `(authority, presence, build)` tuple is the only shape in which
authority is active. `Activate` requires: both halves re-derived from their
attested bytes, both signers allowed by the host's policy to speak for the
domain they claim, the authority inside `Envelope`, the generation fold against
**both** `Applied` and `AppliedPresence`,
`authority.PresenceBinding == presence.Binding`, the same host coordinate *and*
component, the same ownership domain, both halves naming the caller's envelope
revision, neither document withdrawn, `authority.ApprovedBuild == build`,
`build ∈ presence.Builds()`, and
`presence.Generation >= authority.EffectiveFrom`.

**Both folds, because a fold on one signed half is a fold on neither.** The
presence record was not consulted at all: a binding tombstoned at generation 5
refused `Admit` of generation 4 and *activated* the same signed generation-4
document, because `Activate` held only the authority record. Whoever replays
presents the half that is not checked.

**The authority fold is keyed on the BINDING, not the authority ID**, and the
withdrawal check runs before the ID comparison. Keyed on the ID, the same
authority re-signed under a new ID had no applied record and the withdrawal did
not reach it — a tombstone defeated by renaming. Authority is granted over a
binding, so a withdrawal over that binding refuses every later authority ID.

**`FirstActivation` states that the host holds no record**, and activation with
no records and no marker is refused. The zero value could not be told from a
caller that forgot the records, and "nothing applied" is the most permissive
input this call takes — the same conflation as an empty signer policy meaning
"a renderer", and an empty digest meaning "bears no execution".

`DomainsBySigner` is **required**. It was consulted only when non-empty, with
the field documented as "empty means a renderer", which made a security check
optional and indistinguishable from its absence: a named host stating no policy
lets every signer it accepts speak for every domain it accepts, and nothing in
the call says whether the caller waived the check or forgot the field. Deleting
the whole branch left the entire suite green, which is how a check nobody tests
behaves.

This paragraph previously argued that a renderer **cannot obtain** the
`*Delivered` halves, their fields being unexported. That is **false** and
A host consumer refuted it in nine lines: `BundleVerifier` is an
interface the *caller* supplies, so a permissive implementation returning any
identity produces a `*Delivered` with nothing behind it — as `DeliveredBy`'s
own comment says, the signer "is a string the caller handed it". The correction
is recorded rather than quietly edited because the false version was
load-bearing: applied one file over it says `Host.admit`'s `host.Coordinate !=
""` guard is pointless, and removing that fails every test that reaches the host-free checks through a zero Host, since
`AdmitRenderedSets` routes through `Host{}.admit`.

What `*Delivered` does buy is **ordering within one codebase**, held by the
compiler: no sequence of exported calls reaches a judgement without some
verifier having accepted those exact bytes. It is not evidence about *who*
signed.

A renderer wanting the same tuple rules over documents it has not signed yet
calls `ActivateRendered`, which answers a `RenderedMatch` — deliberately not
an `Activation`, because nobody attested either half, so it cannot be the basis
of an authorization decision and no sequence of calls converts one into the
other. Same split, same reason, as `AdmitRenderedSets` beside `Admit`.

```go
match, err := solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
    Authority:        authorityDocument, // PARSED, not delivered
    Presence:         presenceDocument,
    Build:            build,
    EnvelopeRevision: revision, // a NUMBER, not an Envelope
    Applied:          solutionhost.AppliedAuthorityFrom(prior),
    AppliedPresence:  solutionhost.AppliedFrom(priorPresence),
})
```

**It takes a revision, not an `Envelope`, and that was a correction.** It
shipped taking an `Envelope` and was therefore uncallable. A renderer holds no
envelope by design: the render derives an authority document from a module
contract, which is a *request*, and the platform checks it against the ceiling
at apply — a composition carries `host.envelope_revision` and nothing else of
the envelope, and `Envelope.ApprovedBuilds` is the platform's approval record
that a publish is *proposing* a build to, not reading. So the only `Envelope` a
renderer could pass is one assembled from the document under check, and
`ValidateAgainst` tests that document's own `ApprovedBuild` against the
envelope's approved list and its own bindings against the envelope's. The call
would answer itself — the shape this page's `Envelope` rule already refuses,
"an envelope a document carried would be a document declaring its own
ceiling". A zero `Envelope` was no escape either, being refused outright. An
entrypoint whose only possible caller must derive a required input from the
thing under check is the defect — see
[`docs/architecture.md`](architecture.md), "A required input needs an
independent source".

So `ValidateAgainst` stays the **host's**, and both entrypoints share the rest.
What a renderer therefore cannot check is who signed either half and whether
the authority fits the ceiling — both needing host state, both the same split
`AdmitRenderedSets` already makes.

One rule got **stronger** in the move: both halves must name the envelope
revision *the caller named*, where the shared body previously asked only that
the two halves agreed with **each other** — which a pair stamped against a
superseded ceiling satisfies between themselves. Naming no revision is refused
rather than read as "no ceiling".

Everything that can fail answers one sentinel, `ErrNotActivated`, because the
response is the same for all of them — nothing is active — and the message names
the half that is wrong.

Two shapes that look like conveniences and are not:

- It takes **both** documents rather than offering a way to ask the question of
  either one. An authority document alone grants nothing, because nothing says
  the build it approves is the build that is present; a presence document alone
  grants nothing, because nothing says anyone was granted authority over it. A
  caller holding one of them has its answer already.
- The build is passed **in**, not read out of the document that approves it.
  A host asks "is this authority active for the build I am actually running",
  and reading the build from the authority would make the question answer
  itself.

`Activate` **does** check the envelope, against `ActivationRequest.Envelope`.
This said the opposite — that a caller runs `ValidateAgainst` first and then
activates — and an ordering requirement a caller can forget is not a rule:
narrowing an envelope has to reach activation to mean anything. It also
requires `Coordinate`, and compares it against both halves, because activation
bound to no host at all and a tuple written for one host activated on another.

### What a matched tuple does not establish

`Activate` answers one question — are these two documents talking about the same
build on the same host in the same domain — and it is worth being exact about
what that leaves open, because the fields look like they settle more than they
do:

- It checks build **membership**: that the build is one the presence document
  declares. It does not identify a particular authenticated workload or pod, and
  nothing here binds a credential to one. A host that wants that binds the
  authenticated pod's identity to the live workload and compares the designated
  container's resolved image digest itself.
- `effective_from` is compared against a presence **generation**, with no
  reference to a particular binding beyond the one in hand.
- **Same-image workloads and sidecar credential presentation are unresolved.**
  Naming one authenticating container says which container *should* present the
  credential; it does not prove which one did, and two workloads running the
  same approved build are not distinguished by anything in these documents. The
  `build_incarnation` sealed into a credential is what separates two executions,
  and it is the host that must bump it per applied generation — never resolve it
  from the workload's own attributes, which is what would let a pod from a
  superseded generation mint into the current one.

## The signature envelope

Signing is **keyless**. A release is attested by CI over its workload's OIDC
identity: the signature is by an ephemeral key a certificate authority binds to
that identity, and **verification is an identity allowlist** — the signing
repository, workflow path, ref pattern and OIDC issuer — checked against a trust
root the verifier holds, never against a key the document names. Rotation
reduces to trust-root updates, and a disconnected perimeter verifies offline
from the bundle against a mirrored trust root.

**None of that is core's.** A trust root and an identity policy are deployment
configuration, and the component that owns them is the component that verifies.
So the carrier carries the bundle and core does not look inside it:

```go
type Signed struct {
    Schema   string          `json:"schema"`   // codefly/solution-host-signed/v1
    Document json.RawMessage `json:"document"` // the canonical bytes, verbatim
    Bundle   json.RawMessage `json:"bundle"`   // the signature bundle, OPAQUE to core
}
```

The division of labour, in order:

1. The caller verifies `Bundle` over `Document` with a Sigstore verifier, its
   own trust root and its own identity allowlist.
2. The caller hands the verified bytes to `PresenceFromVerified(payload)` or
   `AuthorityFromVerified(payload)`, which own the document half.

```go
signed, err := solutionhost.ParseSigned(data)        // shape only
// ... caller verifies signed.Bundle over signed.Document, elsewhere ...
document, err := solutionhost.PresenceFromVerified(signed.Document)
```

**The names are the contract.** `FromVerified` says the caller has already
verified; core verifies no signature, holds no trust root and interprets no
bundle, so a successful return is **not** evidence that anything was signed.

**The bundle's certificate is evidence, never authority.** It is checked against
the caller's identity allowlist. That is exactly why core refuses to interpret
the bundle at all: a library that parsed a certificate out of a delivery
document would be one short step from trusting what it found there, which is the
"a document never nominates its own key" rule in its keyless form.

What core does own, and all three parts matter:

- **Strict decoding**, so a carrier that ships a `public_key`, a `certificate`
  or a URL beside its bundle is refused when parsed rather than quietly
  tolerated. `bundle` is the one opaque field, and that boundary is drawn on
  purpose rather than by omission.
- **The document type**, which is the `schema` string *inside* the signed bytes
  and therefore attested rather than asserted by the carrier. A verified
  authority payload is refused where a presence document was asked for
  (`ErrSchema`). There is no type field on the carrier: an outer one would be an
  unsigned claim about a signed payload.
- **The canonical round-trip** (`ErrNotCanonical`). A payload that is not the
  canonical encoding of the document it decodes to is refused even when the
  attestation over it is genuine, because a signer and a host that disagree about
  which bytes represent the document disagree about what was approved — and the
  digest the host stores would not match the bytes that were attested.

A carrier with **no bundle** is refused: it is a document, not a signed one, and
letting it through would make "signed" a shape rather than a claim. The bundle
must be a JSON **object**, because a base64 string of a bundle also decodes as
valid JSON here and two accepted shapes is two code paths in every consumer.

**Core ships no signer and no trust store.** `SignedPayload` /
`SignedPayloadFor` return the exact bytes to attest, so "what was signed" has
one answer on both sides; `Carrier(payload, bundle)` assembles the delivered
form from bytes and a bundle, and refuses a payload no consumer could
round-trip — so a render that produced non-canonical bytes fails at the render
rather than at the host. A test holds the package's exported declarations
against a list of signing and trust surfaces, so `Sign`, `Signature`, `Anchor`
or a key type cannot reappear.

### The carrier is JSON, under a `.codefly.yaml` name

`SignedFileName` is `solution-host-binding.signed.codefly.yaml` and its content
is JSON. A YAML emitter folds a long scalar across lines, which changes the
attested bytes and turns every delivered signature into a failure nobody can
read. JSON has no folding, and a JSON object is valid YAML, so the carrier still
travels through a pipeline that classifies delivery documents by extension.
`MarshalSigned` also compacts the payload, so the canonical encoding is the only
payload that survives a round trip through it.

## Invariants

| Rule | Where it is enforced |
| --- | --- |
| The kind is `solution` or `module`, never inferred | `Validate`, `ErrInvalid` |
| One generation, one release | `Validate`, `ErrMixedRelease` |
| Every rendered artifact, the release, and every workload image carry a SHA-256 digest | `Validate`, `ErrInvalid` |
| Two digests of different kinds are never the same string | `Validate`, `ErrDigestConfusion` |
| Workloads iff a backend artifact; each names a backend artifact it declares | `Validate`, `ErrInvalid` |
| One authenticating container, which is not also excluded | `Validate`, `ErrInvalid` |
| A SPIFFE ID is an SVID name, not a generic name | `Validate`, `ErrInvalid` |
| Generations are strictly monotonic per binding ID | `Host.Admit`, `ErrStaleGeneration` |
| An applied generation is immutable | `Host.Admit`, `ErrRewrittenGeneration` |
| The host's own applied record must be usable, and saying so is not an accusation of delivery | `Host.Admit`, `ErrAppliedUnusable` |
| Route aliases are unique within a host, compared per coordinate | `Host.Admit` → `composition.ErrCollision` |
| A document delivered to the wrong coordinate is refused | `Host.Admit`, `ErrWrongHost` |
| A host accepts only the domains it declares, and a binding keeps the domain it was applied under | `Host.Admit`, `ErrWrongDomain` |
| One delivery speaks for one ownership domain | `OneDelivery`, `ErrWrongDomain` — a renderer's check, never a host's |
| Removal is a generation, never an absence | `Validate`; an empty set is "nothing declared" |
| A withdrawn binding ID is never reapplied | `Host.Admit` → `decide`, `ErrTombstoned` |
| Each (principal, binding) pair is inside the caller's envelope, by exact inclusion | `ValidateAgainst`, `ErrOutsideEnvelope` |
| An unverified document cannot reach `Admit` or `Activate` | distinct `Delivered` types, produced only through a caller's `BundleVerifier` |
| A domain is deliverable only by a signer the host allows | `Host.Admit` / `Activate`, `ErrWrongDomain` / `ErrNotActivated` |
| Neither half of a tuple activates alone | `Activate`, `ErrNotActivated` |
| Authority is granted over ONE presence binding, and activates no other | `Activate`, `ErrNotActivated` |
| A replayed or rewritten authority generation activates nothing | `decideAuthority` via `Activate`, `ErrStaleGeneration` / `ErrRewrittenGeneration` |
| Activation checks the ceiling it is given, rather than trusting the caller called `ValidateAgainst` | `Activate`, `ErrOutsideEnvelope` |
| A tombstone is terminal, for a binding and for an authority alike | `decide` / `decideAuthority`, `ErrTombstoned` |
| A document never hands a verifier the material it is checked with | strict decoding in `ParseSigned`; the bundle is the one opaque field |
| A signed document carries a bundle, as an object | `ParseSigned`, `ErrUnsigned` |
| An attested payload is the canonical encoding of its document, and its type is attested | `PresenceFromVerified` / `AuthorityFromVerified`, `ErrNotCanonical` / `ErrSchema` |
| Core declares no signer, key type or trust store | held by test over the package's exported declarations |
| Neither document names a credential | there is no field to put one in, held by a schema guard test |
| An unknown field is an error, so adding one is a version step | strict decoding in `Parse`, `ParseAuthority`, `ParseSigned` |

Route-alias uniqueness reuses `composition.ValidateCollisions` with
`composition.CollisionRoute` rather than a second implementation, so a collision
here reads the same as every other composition collision. One consequence:
`base` is composition's reserved-namespace exemption sentinel, so it is not a
usable binding ID.

## How the consumers use it

A **renderer** checks the set it is about to write with `AdmitRenderedSets`, over
**parsed** documents. Its documents are not signed yet — signing happens at
publish, and a `--local` qualification publish is never signed — so there is no
carrier for a `BundleVerifier` to accept:

```go
admissions, err := solutionhost.AdmitRenderedSets(sets...) // RenderedSet per document
if err := solutionhost.OneDelivery(documents...); err != nil { /* straddle */ }
```

`AdmitRenderedSets` takes **no `Host`**, and that is the design rather than a
convenience: a `Host` carries applied state, a coordinate and a signer policy,
and none of those is checkable without an attestation — so a signature cannot
be the thing a renderer forgets, because there is nothing here to forget it
for. Every check that needs no host state still runs: each document validates,
no binding is declared twice, no two documents claim the same route alias, and
each generation is decided against nothing applied.

This entrypoint exists because making `Admit` take `*Delivered` broke the
renderer, and the break was invisible from inside core — a consumer reported it
by starting to re-implement the zero-host checks in its own tree, which is the
two-implementations failure this package exists to end, appearing in the fix
for it.

The set may span every host the product delivers to. Route aliases are unique
within **one** host, so they are compared per `host.coordinate`.

A **host** verifies first and admits second, and the ordering is now enforced
rather than described: `VerifyDelivered` takes the carrier and the host's own
`BundleVerifier`, calls it, and returns a `*Delivered` only when it accepts.
`Admit` takes nothing else:

```go
host := solutionhost.Host{
    Coordinate:      coordinate, // REQUIRED: Admit refuses an unnamed host
    Domains:         accepted,   // required whenever Coordinate is set
    DomainsBySigner: policy,     // required whenever Coordinate is set
    Reserved:        reserved,
    Applied:         applied,
}
admissions, err := host.Admit(delivered...)
```

**`Coordinate` is required.** Every provenance check inside admission is
guarded by "is this host named", so that the zero `Host` can reach the checks
that need no host state for `AdmitRenderedSets`'s sake — but `Host{}` is
constructible by anyone, and `Host{}.Admit` returned `apply` for a document
from an unlisted signer, under an unlisted domain, targeting a foreign
coordinate, with an attestation present to make it look checked. A caller with
no host state wants `AdmitRenderedSets`, which takes no `Host` at all and so has
nothing to forget.

`Admit` returns one `Admission` per document in the order given, plus an error
that is non-nil whenever any document was refused. A caller that checks only the
error applies nothing; a caller that reads the `Admission`s applies what is sound
and reports what is not. Every refusal is per document, so one malformed binding
does not freeze the other nine.

`Applied` names no coordinate of its own, so `Coordinate` is required whenever it
is non-empty, and every record must carry the `Domain` it was applied under — a
record without one is a record no document can be held against.
`DecisionCurrent` means the generation is already applied; `DecisionApply` means
apply it, then persist `solutionhost.AppliedFrom(doc)`.

`Reserved` constrains what delivery is asking for **now**: it is checked against
each incoming document's own aliases, never against aliases an earlier
generation already holds. Otherwise a host that started reserving a namespace
would refuse every unrelated binding until an operator tombstoned the incumbent.

### The applied digest is a compatibility surface

`Applied.Digest` is the SHA-256 of the canonical encoding, and the host stores
it. Two digests are comparable only when both came from the same encoding, so a
Core release that changes it must treat stored digests as stale rather than as
evidence of a rewrite. The encoding emits object keys in name order at every
depth — which keeps it independent of the Go struct's declaration order — sorts
every collection, and sorts `non_authenticating`, whose delivered order a
renderer emitting it from a Go map would otherwise randomize per process,
turning every reconcile pass into an intermittent `ErrRewrittenGeneration`. It
is pinned by test against the shipped fixtures, so it cannot move by accident.

## Fixtures

See [`solutionhost/testdata/README.md`](../solutionhost/testdata/README.md).

## The module contract and the cell

Two more wire contracts of this lifecycle live here, each with one
implementation and a shipped kit, for the reason the presence and authority
documents do: three repositories read them (the CLI's renderer and publisher,
the runtimes that publish contracts, the platform's loader that derives policy
from cells), and a second implementation in any of them is where the next
disagreement about what a file means appears.

**`solutionhost/modulecontract`** is `codefly/module-contract/v1`, the
request a module publishes beside its manifest as
`module.contract.codefly.yaml`: the principal its credentials are issued to,
the operation bindings it redeems (each with a scope ceiling per operation in
one of two spellings — bare actions qualified by the binding's resource-kind
slot, or `{resource_kind, actions}` entries naming the kind literally, never
mixed, never a mapping), the queues and namespaces it declares, its own scope
ceilings and the destinations it exposes. Audience, resource kind and binding
key are **slots** into the composition's workspace configuration
(`{from: <group>/<key>}`), whose key name carries its meaning, and a literal
where a slot belongs is a schema error. `Parse` decodes strictly (an unknown
field — a tenancy, a build digest, an identity — is refused, never ignored),
`Validate` holds every rule — a ceiling in both spellings included, whether
written or constructed, since `Resolve` would mint scopes from both — and
`Resolve` resolves the slots against the values a composition supplies,
reporting every unresolved, secret-classified and ambiguous slot in one error
under its own sentinel (a secret's value never in the message); a key the
composition supplies in two spellings core treats as one (`MODEL_AUDIENCE`
and `model-audience`) is refused, never chosen between. The model writes what
it reads: a ceiling marshals back in the spelling it holds, so a publisher
using the Go model emits a contract this reader reads, and the valid fixture
round-trips whole.

**`solutionhost/cell`** is `codefly/cell/v1`, the inventory of one
environment's cell that a publish writes to the delivery repository and the
platform derives its mesh policy, admission set and RBAC from: per module
namespace, every pod-producing workload with its exact selector, the
module-qualified service it runs, the account and SPIFFE identity it runs
as, the one authenticating container named, every container's pinned image,
the rendered artifact's digest, its release, endpoints with their container
ports and declared consumers, ingress routes, cell bindings and cloud
identity; the delivery Job declared by its labels; and the external reach the
environment grants. `Parse` decodes strictly and `Validate` holds the host
header to all-or-nothing, every identity to the cell's trust domain, every
edge to an endpoint the cell carries, every egress entry to the namespace's
own module (a grant located under a namespace is that module's, never
another's — a local workload is not required, since a managed replacement
runs none), every image to a **canonical repository** (registry and path as
the distribution reference grammar reads them, ports kept, no tag, no
digest — the digest is the field beside it) and an OCI digest, and every
Kubernetes name and label to **Kubernetes' own grammar**: namespaces and
container names are DNS labels, workload and account names DNS subdomains,
selector keys qualified names and selector values label values — an empty
value is legal, as the API server has it, and an uppercase namespace is not.

An endpoint's `visibility` and `allow_modules` are **declarations the platform
derives policy from**, so they are held to core's own vocabulary
(`resources.Visibility*`, plus `*` for the allow-list) — including the two
spellings core marks deprecated, because the render copies a service's declared
visibility verbatim and core itself still assigns `module`: refusing those
would refuse a cell a real publish writes. What a visibility PERMITS is not
decided here; `resources.ValidateEndpointVisibility` and the workspace's own
validation own that. The vocabulary is a literal in `rules.go` and `resources`
is imported by this package's TESTS only, so the platform's loader never links
core's resource tree to read a cell; two tests hold that line
(`TestTheVisibilityVocabularyIsCoreOwn`, `TestTheCellPackageDoesNotLinkResources`).
Ingress routes are held to endpoint order, as endpoints and egress already are.

**Every refusal is one named rule.** Each package holds its rules in a table
(`rules.go`), applied in a fixed order, so a document is refused for one
reason, named — the same reason whichever reader refused it. **The kits.**
`modulecontract.Fixtures()` (44 documents) / `cell.Fixtures()` (65) ship
every accepted and refused document with the sentinel and the message a
refusal must carry and the rule it protects, and `Run(t, read)` drives a
reader's own entrypoint through them. A consumer passes the function it
actually reads the file with — the renderer's load, the publisher's merge,
the loader's parse — never this package's `Parse`, which proves nothing about
the consumer. Each package's self-check (`TestEveryRuleIsProtectedByAFixture`)
proves the kit protects every rule: every rule is named by at least one
refused fixture, and the kit is run with each rule deleted in turn and must
fail on a fixture naming it — a rule that could be dropped silently is a rule
the kit does not protect. The CLI deleted its copies of both models when
these landed (codefly-dev/cli#855 at `b77f5806`), reads every cell through
`cell.Parse` and every contract through `modulecontract.Load`, and runs both
kits through its own entrypoints; the platform's loader is the next consumer
to run the cell kit.
