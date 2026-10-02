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

- **`workcontext.Inspect(token) error`** — is this token *structurally* a sealed
  capability? Shape, encoding, schema, attenuation, grant shape, a seal naming
  an installation, an epoch on every actor hop. **Error only, no claims**, and
  that is the safety argument: returning claims would let a caller act on an
  unverified token, and with nothing to read the only possible use is the one
  it is for — a sender refusing early rather than having the receiver refuse
  late. It checks no signature and no issuer state; a test asserts a forgery
  passes it. It exists because consumers were answering this question by hand
  and reaching different sentinels than core's fixtures declare.
- **`(*Verifier).Recheck(ctx, *Verified) error`** — re-read live state under a
  long-running call, **without consuming the nonce**. It takes a `*Verified`,
  so it cannot be a first verification: you must already hold one. It re-checks
  the window, the revision, the seal, every hop's epoch, the binding and the
  grant record, and never the signature or the replay store. `Verify` consumes
  a single-use capability, so a stream guard that re-verified before each
  emission killed the stream on its first check; liveness and consumption are
  different operations and only one belongs in a loop. How often to call it is
  the caller's explicit choice, not core's.

See `docs/work-context.md`, "Two entrypoints, one implementation, one strength".
