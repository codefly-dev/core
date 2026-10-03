# `workcontext` is the only implementation of the Work Context

**One rule: a wire contract has exactly one implementation, in the repository
that owns the type.** The Work Context is a core proto
(`codefly.base.v0.WorkContextV1`), so this package — the deterministic proto
marshal, Ed25519 over those bytes — is the only mint and the only verify.
Nothing else may sign, verify or re-encode one.

The model, the seal, and the reasoning behind every check are in
[`docs/work-context.md`](../docs/work-context.md). This file is the short
version of the rule and of the gate that keeps it true.

## Why the rule is written down

It was broken. A second implementation in another repository signed a
hand-written snake_case JSON payload instead of the proto encoding. Both forms
are `<base64url payload>.<base64url signature>` with an Ed25519 signature, so a
token from either looked structurally fine to the other and then failed
**signature** verification. `signature does not verify under key X` reads like a
key-rotation or trust-root problem, so that is what got investigated — while the
actual problem was that there were two encodings.

## The three things that make it unrepeatable

**A foreign encoding is refused before the signature, with its own error.**
`ErrNotACoreToken`, never a signature failure. The check is certain, not a
heuristic: `{` is 0x7b, which as a proto3 field tag is field 15 with the
start-group wire type that proto3 does not emit and `Unmarshal` refuses; `[` is
0x5b, the same. Neither byte can begin a `WorkContextV1`.

The discrimination is exported as `CheckEncoding(payload []byte) error`, taking
the base64url-**decoded** claims. It is exported so that every other place in
the fleet that decodes a payload names the same condition with the same error:
a second decoder answering "payload is not a WorkContextV1" for a foreign
encoding would hand an operator two messages for one condition, which is this
whole error's fragmentation in miniature. A **nil return means only that the
payload is not visibly in another format** — not that it is valid, and not that
anything was authenticated. `Verify` is still the only thing that turns a token
into claims.

**`Fixtures(now)` is the conformance kit, as code.** Every token form this
package mints and every way one is refused, with the signed token, the outcome,
the sentinel a refusal must match, and — where that sentinel is an umbrella — a
substring the message must contain. Count the kit with `len(Fixtures(now))`
rather than trusting a number written in prose; a number in a comment is a
number that goes stale, and this one did. Minted fresh per call,
because a capability carries a validity window and committed bytes would expire.
The fixture private key is derived from a seed in the source and is therefore
**public**; it signs conformance tokens and nothing else.

**`conformance.Run(t, verify)` is run by the consumer.** A consumer passes its
own verification entrypoint and the helper fails its build if any outcome
differs:

```go
func TestWorkContextConformance(t *testing.T) {
    verifier := conformance.Verifier()
    conformance.Run(t, func(ctx context.Context, token string) error {
        _, err := myPackage.VerifyWorkContext(ctx, token)
        return err
    })
    _ = verifier
}
```

**The gate is in the consumer because core cannot see who re-implements it.** A
consumer that runs these fixtures against its own entrypoint demonstrates that
it BEHAVES like this package's verifier.

Be precise about what that is and is not: these fixtures check accept/refuse
decisions and the named reason for each refusal, so an equivalent second
implementation could in principle pass them. Behavioural conformance is not
proof of implementation identity. The identity proof is the consumer's own
import gate — an alias of this package's `Verifier`, pinned by a compile-time
assertion, plus a CI check that no second signing or verifying implementation
exists in the module — and that lives in the consumer, not here. The two
together are the gate; the fixtures alone are the cheaper half. A consumer that has grown a second implementation cannot pass
the `foreign-encoding` fixture, because refusing a foreign encoding *before* the
signature check with a distinct error is the one behaviour a re-implementation
never thinks to copy — it is the behaviour that exists because the
re-implementation happened.

Two fixtures are the pair that makes this load-bearing rather than cosmetic:
`tampered-payload` is a sound capability with one byte changed and the signature
untouched, and it is the one fixture that *must* report a signature failure;
`foreign-encoding` is a genuinely signed JSON-payload token, whose signature is
real precisely so that a verifier checking the signature first would still
refuse it — with the wrong error.

## `SealSource` has four methods, and the fourth is per-principal

`Seal` (installation state), `PrincipalEpoch`, `ApprovedBuild`, and
`OperationBinding`.

`ApprovedBuild(ctx, principalID) (digest string, incarnation uint64, err error)`
is the newest and the reason the others changed shape: the approved execution
used to live on `Seal`, read for the task's OWNER, so a derived capability's
execution described the owner's workload no matter who held it — and a hop's
principal does not hold the owner's installation, so a derivation had nothing
to attest against. Minting was execution-bound at `Start` and nowhere else.

Two things to implement deliberately rather than by default:

- **Return `ErrNoApprovedBuild` for a principal that bears no execution.** It
  is the right answer for a human session, not a missing record. Do NOT return
  an empty digest: a verifier cannot tell that from an issuer that has lost the
  record, and those two must not read alike.
- **Key it on the principal and nothing else.** A lookup by (service account,
  image digest) answers "approved" for whatever a superseded pod presents,
  which is the hole the field exists to close.

`MemorySealSource.PutApprovedBuild` is monotone in the incarnation and free in
the digest — approving a different build is not a rewind, it is what approving
a build is, and the incarnation advancing with it separates the runs. A
principal that bears no execution is recorded by recording nothing.

## What a consumer owes

`conformance.New(now)` returns the `Settings` an entrypoint must be configured
with: issuer, audience, keys, revision source, replay store, seal source, grant
source, clock. `Settings.Verifier()` assembles core's `Verifier` from them,
which under the one rule is what every consumer's entrypoint resolves to.

**Passing `Settings.Verifier()` to `Run` drives core's verifier and says
nothing about yours.** To prove something about your own exported type, build
it field by field from the settings and run the kit against that — the alias is
what makes behaviour and identity coincide, and the kit cannot see the alias.
`Settings.PublicKeys()` is the keys in the type `Verifier.Keys` takes
(`Settings.Keys` is `[]byte`-valued so holding settings does not force a
`crypto/ed25519` import, and it will not assign across). Use `settings.Replay`
as handed to you: one store for the whole run, because the kit presents the
single-use fixture twice.

`Run` presents the single-use fixture twice, so a verifier without a working
replay store passes everything else and fails there. Single-use is a property of
the verifier, not of the token.

`Verifier` requires all four sources and refuses everything if any is missing.
That makes it issuer-shaped, which is the correct shape for the party that mints.

For a party that verifies **without minting** — the host's gateway — there is
`Authenticator.Authenticate`, and `conformance.RunAuthenticator` is the mode
that certifies it. It is a second entrypoint and deliberately not a second
strength: `Authenticate` assembles a `Verifier` and calls `Verify`, so there is
one check path; a capability carrying a grant hop is **refused** with
`ErrNeedsIssuer` rather than accepted unchecked; `Seals` and `Revisions` are
still required, because no entrypoint skips the seal comparison or assumes the
authorization revision — it is per tenant, and an earlier draft's stated number
silently stopped enforcing revocation for every tenant but one; and the result
is `*Authenticated`, which
`policy.PrincipalFromWorkContext`, `Authority.Child` and `Authority.Grant` will
not take. `RunAuthenticator` is stricter than `Run`, not laxer: identical
outcomes everywhere except the fixtures marked `NeedsIssuer`, which must be
refused — the one assertion a downgraded authenticator fails.

Two more entry points exist, and neither is a third strength:

- **`workcontext.Inspect(token) (*Inspected, error)`** — is this token
  *structurally* a sealed capability? Shape, encoding, schema, the lifetime
  bound, attenuation, grant shape, a seal naming an installation, an epoch on
  every actor hop. It checks no signature and no issuer state; a test asserts a
  forgery passes it.

  **It returns the claims, and this entry used to say "error only, no
  claims"** with an argument for why that was the safe choice. A consumer
  showed the argument was wrong in the direction that mattered: a holder
  reading its OWN credential needs the expiry and the seal, so with nothing
  returned it keeps the hand-written parser that this call exists to delete.
  The safety now rests on the TYPE — `Inspected` is not `Verified`, cannot
  become one, and `policy.PrincipalFromWorkContext` will not take it — rather
  than on withholding data any holder of the token could base64-decode anyway.
- **`(*Verifier).Recheck(ctx, *Verified) error`** — re-read live state under a
  long-running call, **without consuming the nonce**. It takes a `*Verified`,
  so it cannot be a first verification: you must already hold one. It re-checks
  the window, the revision, the seal, every hop's epoch, the binding and the
  grant record, and never the signature or the replay store. `Verify` consumes
  a single-use capability, so a stream guard that re-verified before each
  emission killed the stream on its first check; liveness and consumption are
  different operations and only one belongs in a loop. How often to call it is
  the caller's explicit choice, not core's.

## What the kit does NOT check

The kit proves BEHAVIOUR, not identity and not host sourcing. Three gaps are
worth naming rather than discovering — and the first is one instance of a
shape that has now appeared three times in this contract, tabulated in
[`docs/architecture.md`](../docs/architecture.md) under "A required input needs
an independent source":

- **Where a host gets `Execution.ImageDigest`.** This is the big one. The field
  is the running build, and `SealSource.ApprovedBuild` answers the APPROVED
  build; a host that fills the first from the record that produced the second
  compares approved against approved, so the mint passes for every caller —
  including the superseded pod the field exists to refuse. **The kit itself
  does this**, in `fixtureSessionOn`: it reads `ApprovedBuild` and attests the
  answer back.
  That is deliberate and unavoidable here — a fixture has no pod, and a
  negative fixture must mint against a divergent source or the capability it
  exists to present cannot be produced — but it means the reference
  implementation demonstrates the shortcut, so copying its shape is copying
  the tautology. The test that catches it needs a host and cannot live here:
  take a pod from a SUPERSEDED generation and confirm the mint refuses. "A
  sound execution mints" passes either way.
- **Who may hold the key.** The kit asserts nothing about key custody; its own
  key is refused by default for the reason in the next section.
- **That a consumer imports core rather than reimplementing it.** The kit runs
  against whatever `Settings` hands it, so passing a hand-written verifier
  makes the kit pass while proving the opposite of what it exists to prove.
  The identity proof is the consumer's own import gate, never this.

## The fixture key is NOT linked into every binary, measured

A review found that `FixtureKeyPair()` derives a private key from a seed in the
source and concluded it is "linked into every production binary importing
`workcontext`", with the remedy being to move the kit into a test-only package.

**The premise does not survive measurement.** Go's linker eliminates it. Built
with a positive control, because a negative result from an unvalidated method
is worth nothing:

| binary | seed string present | `FixtureKeyPair` symbol |
| --- | --- | --- |
| imports `workcontext`, only constructs a `Verifier` | no | no |
| calls `FixtureKeyPair()` (positive control) | yes | yes |

Method: build each, then `strings <bin> | grep -F "codefly work context
conformance fixtures"` and `go tool nm <bin> | grep FixtureKeyPair`. The
control is what makes the first row meaningful — an earlier run of the same
check used `grep` on the binary directly and reported "absent" for BOTH, which
would have been a false refutation.

So the exposure is narrower than stated: a binary that REFERENCES the kit links
the key. That is a real shape but a different one, and relocating the kit — which
would break every consumer conformance suite just written against
`Fixtures`/`conformance` — is not justified by a premise that measurement
contradicts.

**What remains true, and is the open owner item:** `conformance.Verifier()` is
an exported, ready-made verifier that trusts the fixture key, so the risk is
reachability by mistake rather than linkage. That is why
`TrustTheConformanceFixtureKey` must be set explicitly and why the key is
refused by default — see the next section. A `//go:build` tag or a separate
module would make it unreachable rather than merely visible, and that is the
decision still open.

## The fixture key is refused by default

The kit's private key is **derivable from this package's source by anyone**, and
every binary importing `workcontext` links it. `conformance.Verifier()` is an
exported, ready-made verifier that trusts it — so one mistaken call, or one
JWKS document that picked the fixture key up, would make a real verifier accept
tokens anybody can mint.

A verifier now refuses that key unless `TrustTheConformanceFixtureKey` says
otherwise. `conformance.Settings` carries the flag so the field-by-field recipe
above stays complete; copy it into a conformance verifier and **nowhere else**.
Its name is that long so copying it into a production verifier is visible in
review. `RunWith` detects the case where it was missed and names the field,
because a consumer read this very warning and still omitted the bool.

**Why the field-by-field build is worth the friction**, stated because that
consumer made the argument better than the original text did: building the
verifier by hand is what MADE the new flag a failing test. `Settings.Verifier()`
would have inherited it silently and the consumer would have learned nothing
about a new field in the contract. That is the second time the field-by-field
recipe has paid for itself — the first was `Seal.ImageDigest`, which cost that
consumer no code at all because its credential returns core's `WorkSealV1`
rather than a hand-filled struct.

What this does **not** fix: the key is still linked into any binary importing
this package. Only moving the kit into a package that production cannot import
fixes that, which moves `Fixtures`, `FixtureKeys` and every `Fixture*` constant
for every consumer. That relocation is an owner call, not a quiet one.

See `docs/work-context.md`, "Two entrypoints, one implementation, one strength".
