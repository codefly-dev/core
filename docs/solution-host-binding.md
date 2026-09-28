# The solution host binding

`SolutionHostBinding` is the versioned document that declares **which solution
runs on which host**. It lives in [`solutionhost/`](../solutionhost).

It exists to invert how presence is established. Today a solution becomes
present by announcing itself: its runtime heartbeats a registration and the host
mints a token at liveness cadence, so presence is a side effect of a process
being up. With a declared record, delivery states what should run, the host
reconciles towards it, and the runtime only reports health against a binding it
never created. A down deployment stays declared and unhealthy instead of
vanishing, and removal becomes something someone wrote down.

Core owns the type, its parser and its verifier. It does not render one
(`codefly-dev/cli`), reconcile one (the host in
`codefly-dev/module-saas-starter`), sign one, or observe what actually runs.

The shape follows the *desired host binding* record proposed in
[obin-ai/handbook#151](https://github.com/obin-ai/handbook/issues/151) §5 and
its lifecycle in §6. **That proposal is open, not settled contract.** What is
implemented here is the document and the invariants below; the schema is
versioned so a later settlement is a version step rather than a reinterpretation
of bytes already delivered.

## The document

```yaml
schema: codefly/solution-host-binding/v1
binding: crm-eu-west-1-01          # stable ID of one deployment instance
generation: 4                      # strictly monotonic per binding ID
host:
  coordinate: obin/prod/eu-west-1
  component: saas-host
release:
  publisher: obin
  name: crm
  version: 1.4.0
  digest: sha256:…                 # OPTIONAL in v1 — see below
routes:
  - alias: crm                     # unique within a host
    surface: frontend
artifacts:                         # every rendered artifact, digest REQUIRED
  - {surface: frontend, name: web, release: obin/crm@1.4.0, digest: "sha256:…"}
  - {surface: backend,  name: api, release: obin/crm@1.4.0, digest: "sha256:…"}
  - {surface: client,   name: sdk, release: obin/crm@1.4.0, digest: "sha256:…"}
modules:                           # effective pins, exact versions only
  - {module: crm, package: obin/crm-core, version: 1.4.0}
endpoints:                         # named, never addressed
  - {name: api, service: crm, module: crm, api: grpc, visibility: internal}
workload:                          # the identity to expect, not a credential
  audience: https://prod.eu-west-1.obin.example/solutions
  subject: system:serviceaccount:crm-eu-west-1-01:crm-api
```

A tombstone is the same document with `removed: true`, a higher generation, and
no routes, artifacts, modules or endpoints. It still names the release it
removes.

`release.publisher` and `release.name` are one segment each, never a path. Each
artifact references its release by the joined `publisher/name@version`, so a `/`
inside either part would let two different releases render one identity string
and any consumer keying on it would conflate them.

## Why one digest is required and the other is not

**Rendered artifact digests are required from v1.** Renders are already
digest-pinned, so a document without them hides information that exists.

**The release digest is optional in v1** and becomes required at a later schema
version. Signed releases do not exist yet. Requiring the digest now would block
declared presence on the signing work; dropping the field would lose the link
from publication to deployment. Neither is acceptable, so v1 carries it
optionally and the schema version — not a stricter reading of v1 bytes — makes
it required later.

## Invariants

| Rule | Where it is enforced |
| --- | --- |
| One generation, one release — an artifact rendered from another release is refused | `(*SolutionHostBinding).Validate`, `ErrMixedRelease` |
| Every rendered artifact carries a SHA-256 digest | `Validate`, `ErrInvalid` |
| Generations are strictly monotonic per binding ID; an older one is rejected, not merged | `Host.Admit`, `ErrStaleGeneration` |
| An applied generation is immutable; the same number with different bytes is a rewrite | `Host.Admit`, `ErrRewrittenGeneration` |
| Route aliases are unique within a host — compared per coordinate, not across hosts | `Host.Admit`, via `composition.ValidateCollisions` → `composition.ErrCollision` |
| A host's reserved namespaces constrain incoming declarations, not what is already applied | `Host.Admit` → `composition.ErrCollision` |
| A document delivered to the wrong coordinate is refused | `Host.Admit`, `ErrWrongHost` |
| Removal is a generation, never an absence | `Validate` (a tombstone declares nothing present); an empty set is "nothing declared" |
| The document names identities and never credentials | there is no field to put one in, held by a schema guard test |
| An unknown field is an error, so adding one is a version step | strict decoding in `Parse` |

Route-alias uniqueness reuses `composition.ValidateCollisions` with
`composition.CollisionRoute` rather than a second implementation, so a collision
here reads the same as every other composition collision and carries
`composition.ErrCollision`. One consequence: `base` is composition's
reserved-namespace exemption sentinel, so it is not a usable binding ID.

## How the two consumers use it

`Admit` returns one `Admission` per document, in the order given, plus an error
that is non-nil whenever any document was refused. A caller that checks only the
error applies nothing; a caller that reads the `Admission`s applies what is
sound and reports what is not. Validity is per document, so one malformed
binding does not freeze the other nine on a host.

A **renderer** checks the set it is about to write with the zero `Host` — no
coordinate pinned, nothing applied. Every check that does not need host state
still runs, so an alias collision is refused where it was authored instead of
leaving the host to guess which of two claimants meant it.

```go
admissions, err := solutionhost.Host{}.Admit(documents...)
```

The set may span every host the product delivers to. Route aliases are unique
within **one** host, so they are compared per `host.coordinate`: the same alias
on two coordinates is one solution in two regions, not a collision.

A **host** verifies provenance and its expected target first — that is not this
package's job — then admits against what it has durably applied:

```go
host := solutionhost.Host{Coordinate: coordinate, Reserved: reserved, Applied: applied}
admissions, err := host.Admit(documents...)
```

`Applied` state names no coordinate of its own, so `Coordinate` is required
whenever it is non-empty. `DecisionCurrent` means the generation is already
applied; a host re-reads its mounted document every pass, so "unchanged" is an
answer rather than an error. `DecisionApply` means apply it, and then persist
`solutionhost.AppliedFrom(doc)`.

`Reserved` constrains what delivery is asking for **now**: it is checked against
each incoming document's own aliases, never against aliases an earlier
generation already holds. Otherwise a host that started reserving a namespace
would refuse every unrelated binding until an operator tombstoned the incumbent.

### The applied digest is a compatibility surface

`Applied.Digest` is the SHA-256 of the document's canonical encoding, and the
host stores it. Two digests are comparable only when both came from the same
canonical encoding, so a Core release that changes that encoding must treat
stored digests as stale rather than as evidence a generation was rewritten. The
encoding emits object keys in name order at every depth — which is what keeps it
independent of the Go struct's declaration order — and is pinned by test against
the shipped fixtures, so it cannot move by accident.

## Fixtures

The fixtures are embedded, not just in `testdata/`, so another module can reach
them: `solutionhost.Fixtures()` returns each document with the outcome a
conforming implementation must reach against `solutionhost.FixtureHost()`.

| Fixture | Outcome |
| --- | --- |
| `valid` | accepted — `DecisionCurrent` (it is what `FixtureHost` has applied) |
| `tombstone` | accepted — `DecisionApply` |
| `stale-generation` | rejected — older than the applied generation |
| `mixed-release` | rejected — artifacts from two releases in one generation |
| `duplicate-route-alias` | rejected — the alias another binding holds |
