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
package mints and every way one is refused, with the signed token and the
outcome — including the sentinel a refusal must match. Minted fresh per call,
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
consumer that runs these fixtures against its own entrypoint proves it uses this
package's path. A consumer that has grown a second implementation cannot pass
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

`Run` presents the single-use fixture twice, so a verifier without a working
replay store passes everything else and fails there. Single-use is a property of
the verifier, not of the token.

`Verifier` requires all four sources and refuses everything if any is missing.
That makes it issuer-shaped, and a consumer verifying incoming capabilities
needs all four. That is a real cost of there being one implementation, and it is
the correct cost: the alternative is a second verifier answering a weaker
question.
