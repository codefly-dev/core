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
`codefly-dev/module-saas-starter`), **sign** one, or observe what actually runs.

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

A set that straddles two domains cannot satisfy "within D the delivered set is
exactly desired" for any single D, so removal within it is not expressible. That
is a real rule — and it is a property of **one delivery**, which is not the same
set as "the documents a host can see". The difference decides the answer:

- A **renderer** writing one delivery calls `solutionhost.OneDelivery(docs...)`
  on the set it is about to write, and a straddle is refused where it was
  authored.
- A **host** does not call it on its mount. A mount is the union of however many
  deliveries reached it, so it legitimately carries one domain per delivery.
  Refusing that inside `Admit` would refuse the normal case the moment a second
  module delivered to the same host. A host uses
  `solutionhost.ByDomain(docs...)` instead: within each group the documents
  present are the desired set, and a binding of that domain absent at a higher
  generation has been removed.

This is deliberately a separate call rather than part of `Admit`, because
putting it in `Admit` makes core decide that a host's mount is one delivery —
which is exactly the question the renderer and the host have not yet settled
between them (one ConfigMap per solution namespace, or one tree in the host's).
Core enforces what is true either way and hands the caller the grouping for
what is not.

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
activation, err := solutionhost.Activate(authority, presence, build)
```

A matched `(authority, presence, build)` tuple is the only shape in which
authority is active. `Activate` requires: the same host coordinate *and*
component, the same ownership domain, the same envelope revision, neither
document withdrawn, `authority.ApprovedBuild == build`, `build ∈
presence.Builds()`, and `presence.Generation >= authority.EffectiveFrom`.

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

`Activate` does not check the envelope: that needs a ceiling neither document
may carry. A caller verifies both signatures, runs `ValidateAgainst`, then
activates.

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
| Route aliases are unique within a host, compared per coordinate | `Host.Admit` → `composition.ErrCollision` |
| A document delivered to the wrong coordinate is refused | `Host.Admit`, `ErrWrongHost` |
| A host accepts only the domains it declares, and a binding keeps the domain it was applied under | `Host.Admit`, `ErrWrongDomain` |
| One delivery speaks for one ownership domain | `OneDelivery`, `ErrWrongDomain` — a renderer's check, never a host's |
| Removal is a generation, never an absence | `Validate`; an empty set is "nothing declared" |
| Authority bindings are inside the caller's envelope, by exact inclusion | `ValidateAgainst`, `ErrOutsideEnvelope` |
| Neither half of a tuple activates alone | `Activate`, `ErrNotActivated` |
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

A **renderer** checks the set it is about to write with the zero `Host` — no
coordinate, no domains, nothing applied. Every check that does not need host
state still runs, so a collision is refused where it was authored:

```go
admissions, err := solutionhost.Host{}.Admit(documents...)
```

The set may span every host the product delivers to. Route aliases are unique
within **one** host, so they are compared per `host.coordinate`.

A **host** establishes provenance first — it verifies the bundle over the
payload with its own trust root and identity allowlist, then reads the document
with `PresenceFromVerified` — and only then admits against what it has durably
applied:

```go
host := solutionhost.Host{
    Coordinate: coordinate,
    Domains:    accepted,   // required whenever Coordinate is set
    Reserved:   reserved,
    Applied:    applied,
}
admissions, err := host.Admit(documents...)
```

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
