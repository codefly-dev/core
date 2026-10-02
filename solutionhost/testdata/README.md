# The solution host conformance fixtures

These documents are **shipped**, not test scaffolding. `codefly-dev/cli` renders
them, the host in `codefly-dev/module-saas-starter` reconciles and verifies
them, and all three repositories drive the same bytes — so the renderer, the
host and core cannot drift on what a rule means while each stays green against
its own reading of it.

They are embedded, so another module reaches them through the API rather than
through this directory:

```go
for _, shipped := range solutionhost.FixturesOf(solutionhost.DocumentTypePresence) {
    // shipped.Document, shipped.Outcome, shipped.Decision, shipped.Reason
}
```

`solutionhost.Fixtures()` returns all of them, each carrying its `Type`;
`FixturesOf(type)` narrows; `FixtureDocument(type, name)` fetches one;
`FixtureFS()` hands back this directory for a consumer that would rather walk it.

## Drive them one at a time

Each type is checked against its own piece of fixture state:

| type | state | call |
| --- | --- | --- |
| `presence` | `solutionhost.FixtureHost()` | `Parse`, then `Host.Admit` |
| `authority` | `solutionhost.FixtureEnvelope()` | `ParseAuthority`, then `ValidateAgainst`, then `Activate` against the `valid` presence fixture |
| `signed` | `solutionhost.FixtureAnchor()` | `ParseSigned`, then `VerifyPresence` or `VerifyAuthority` |

**One at a time.** Several fixtures are deliberately contradictory — `tombstone`
and `tombstone-foreign-domain` are the same withdrawal of the same binding, from
two different deliveries — so admitting the whole set in one call is not what any
of them means. `Fixture.Reason` says which rule decides each outcome; a rejected
fixture may be refused by `Parse`, by `Validate`, by `Host.Admit`, by
`ValidateAgainst`, by `Activate` or by verification, and the reason says which.

A fixture whose own rules reject it never reaches a host at all, which is itself
the required outcome. A consumer's loop should treat a parse failure on a
`rejected` fixture as a pass.

## The fixture state

- `FixtureCoordinate` is `obin/prod/eu-west-1`; every fixture targets it, so a
  consumer never has to guess which host a document is written against.
- `FixtureDomain` is `crm`. Every fixture speaks for it **except**
  `tombstone-foreign-domain`, which is the point of that fixture.
- `FixtureHost()` is the `valid` presence fixture, applied — the relational
  rules need it. An older generation is only old against an applied one, a
  route alias only collides with one something else holds, and a domain is only
  foreign relative to the domain a binding was applied under. It accepts
  `FixtureDomain` and nothing else.
- `FixtureEnvelope()` is **assembled in Go, not shipped as a file.** An
  envelope is the one thing a document must never carry, and a fixture envelope
  sitting in this directory next to the documents it bounds would be a ceiling
  delivered alongside the thing it is supposed to bound. It holds two approved
  builds: the one the `valid` presence fixture runs, and a second one no
  presence fixture runs — so `authority/other-build`'s refusal is "the presence
  document does not name this build" and never "the envelope did not approve
  it".
- `FixtureAnchor()` holds two **public** keys, `fixture-2026-10` (active) and
  `fixture-2026-09` (revoked), read from `signed/anchor.json`. The revoked key
  is deliberately still in `Keys` as well as in `Revoked`: revoking has to hold
  even while the key is present and still verifies arithmetically.

## Presence fixtures

| fixture | outcome | the rule it pins |
| --- | --- | --- |
| `valid` | accepted, `DecisionCurrent` | a complete v2 document for a solution instance, and the generation `FixtureHost` has applied |
| `module-presence` | accepted, `DecisionApply` | presence covers modules too, and the kind is declared rather than inferred |
| `tombstone` | accepted, `DecisionApply` | removal is a generation, under the domain the binding was applied with |
| `tombstone-foreign-domain` | rejected | the same withdrawal from a delivery that speaks for another domain; the generation is *newer*, and ownership is what refuses it |
| `stale-generation` | rejected | an older generation is refused, not merged |
| `mixed-release` | rejected | one generation, one release |
| `duplicate-route-alias` | rejected | route aliases are unique within a host |
| `wrong-kind` | rejected | the kind is neither `solution` nor `module`, and is not guessed |
| `missing-identity` | rejected | no SPIFFE ID, so nothing a host could verify an SVID against |
| `digest-confusion` | rejected | an image digest and a rendered digest are the same string |
| `superseded-schema` | rejected | a v1 document; there is no v1 reader, and the refusal is `ErrSchema` rather than `ErrInvalid` |

## Authority fixtures

| fixture | outcome | the rule it pins |
| --- | --- | --- |
| `valid` | accepted | inside `FixtureEnvelope`, approved for the build the `valid` presence fixture runs |
| `tombstone` | accepted | withdrawal of authority is a generation; a sound document that activates nothing |
| `outside-envelope` | rejected | it grants a binding the envelope does not hold — and a *plausible widening* of one it does, which is how a subsumption rule would let it through |
| `other-build` | rejected | approved for a build the presence fixture does not name, so the tuple does not activate |

## Signed fixtures

These are **JSON**, not YAML, for the reason
[`docs/solution-host-binding.md`](../../docs/solution-host-binding.md) gives: the
signature covers the payload verbatim and a YAML emitter folds long scalars.

| fixture | outcome | the rule it pins |
| --- | --- | --- |
| `presence` | accepted | the `valid` presence document, signed by the active key |
| `authority` | accepted | the `valid` authority document, signed by the active key |
| `nominates-key` | rejected | the carrier ships a public key beside its signature; strict decoding refuses it, so a document can never nominate the key it is checked with |
| `key-id-is-a-url` | rejected | the key id says *where to fetch* a key rather than identifying one already trusted |
| `untrusted-key` | rejected | a genuine signature by a key the anchor does not hold |
| `revoked-key` | rejected | a genuine signature by a key the anchor holds and has revoked — revocation is checked before the key lookup |
| `non-canonical` | rejected | a genuine signature over bytes that are not the canonical encoding of the document they decode to |

`non-canonical` is `json.Marshal` of the struct: keys in Go declaration order,
collections in delivery order. That is exactly what a signer that reached for
`encoding/json` instead of `CanonicalBytes` produces, and it parses to the same
document — which is what makes the round-trip check worth having.

## Regenerating the signed fixtures

The signatures cover the canonical bytes of the `valid` presence and authority
documents, so **editing either one invalidates its signature** and
`TestSignedFixturesReachTheirOutcome` fails until you rerun:

```sh
go run scripts/gen_solutionhost_fixtures.go
```

That program carries the fixture signing keys and is excluded from every build
by a `//go:build ignore` tag. This package ships the public keys and the
already-signed bytes and **no signer at all** — a library that could sign an
authority document would put the authority to grant authority in every binary
that imports core. Signing belongs to the reviewed delivery pipeline, and that
program is the fixtures' stand-in for it.

The keys are derived from fixed seeds written in that file so the fixtures
regenerate byte-for-byte, which makes a change show up as a diff rather than as
churn. **Every private key it derives is therefore public** — in this
repository, in its git history. Nothing outside these fixtures may trust them.
