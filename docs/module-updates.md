# Per-consumer module update contracts

`moduleupdate` computes SAFE, NEW CAPABILITY, or BREAKING from a release's
contract evidence and one consumer's exact pins. It performs no network access
or deployment changes. Its public messages live in
`proto/codefly/update/v0/update.proto`; Go consumers use the generated bindings.

## Evidence and publication

`composition.BuildPackageContractSnapshot` derives a `ContractSnapshot` from the complete packaged interface,
including non-SDK behavior such as registration, authorization, configuration,
and frontend integration. Each item has a stable identity (an RPC, type,
endpoint, or named behavioral requirement), a SHA-256 digest of its canonical
contract, and its dependencies. RPC items must include dependencies on request
and response types; dependencies may be cyclic. Identity and canonicalization
rules are shared by publication and archive verification and must remain stable across
releases. A changed canonicalization is unknown compatibility, not a safe bump.

`PrepareSnapshot` validates and sorts a copy, then hashes the canonical protobuf
JSON from `runnable.CanonicalJSON`, excluding the snapshot's own digest. The
hash domain is `codefly.update.snapshot/v1` followed by a zero byte. Digests use
`sha256:` plus lowercase hexadecimal. `BuildReleaseDiff` compares two exact
versions of the same module. It emits additions, removals, and changes to item
digests, dependencies, or universal applicability. Documentation changes alone
do not change a contract's compatibility.

`MarshalReleaseDiff` produces deterministic JSON for
`contracts/update.codefly.json`, containing both snapshots and their entire
delta. The publisher includes that file in the module archive before signing
its provenance. `composition.VerifiedRelease.ContractDiff` reads only from the
authenticated archive and checks that the candidate module/version matches its
manifest. It independently derives the candidate snapshot from the packaged
sources and rejects stale evidence even when the archive signature is valid.
A loose sidecar digest does not authenticate a release.

The shared derivation validates catalog/manifest/file agreement and reads actual
protobuf methods, reachable types and OpenAPI operations/references. IDs are
`api/<service>/<endpoint>#<symbol>`; endpoint context and referenced types are
dependencies. Remote OpenAPI references and unresolved sources block derivation.
Non-API source declarations are required in `contracts/behavior.codefly.json`,
using `BehavioralContracts` with explicit completeness and canonical content.
Their IDs are `behavior/<id>`. An empty declaration must explicitly be complete;
missing source files never imply no behavioral requirements. Behavioral
implementations still need conformance tests against their declared contracts.

These functions do not invoke `codefly publish`. Publication integration is tracked in
[CLI #753](https://github.com/codefly-dev/cli/issues/753). Existing releases
without evidence produce BREAKING when checked; ordinary release verification
does not newly require this artifact.

## Consumer usage and verdicts

A `ConsumerPin` binds the consumer identity, module version, and baseline
snapshot digest. Versions must be full semantic versions (an optional `v` is
accepted) or immutable 40/64-character hexadecimal revisions. Exact SDK/client
versions carry expected contract digests and the dependency-closed used surface.
Direct non-SDK usage is declared separately. Multiple versions of a client may
coexist. A generated client with no more precise usage evidence must declare
its entire exposed surface.

`usage_complete` explicitly asserts coverage of every client and direct use.
`complete` on each snapshot asserts coverage of every public contract, including
behavioral requirements. Neither assertion can be inferred from empty lists.
The engine verifies the submitted evidence's internal consistency; it cannot
prove that a producer or consumer declared everything its code does. Source
generators and their conformance tests own that assertion.

`Evaluate` and `EvaluateJSON` apply these rules:

- SAFE: no used item changed and no optional item was added. Removing or changing
  a contract this consumer does not use can be safe.
- NEW CAPABILITY: optional items were added and no used contract changed. The
  result names the additions and preserves their documentation pointers.
- BREAKING: a used item's digest, dependencies, or applicability changed, or it
  was removed. The result names the item and affected client/version. New
  universal requirements are BREAKING even without SDK usage. Existing universal
  requirements and their transitive dependencies apply to every consumer.

Malformed input, unknown schema/fields, incomplete coverage, mismatched module
or baseline, stale SDK digests, missing usage dependencies, and a fabricated or
truncated diff all yield BREAKING with `could not determine` and the reason.
BREAKING takes precedence over new capabilities, but both lists are retained.
A valid explicit declaration of no usage is different from missing evidence.

Every changed used item is treated conservatively: this version does not prove
that a changed protobuf field or OpenAPI schema remains source/wire compatible.
It may therefore require review for an actually compatible used-type change.
A publisher cannot label a change safe to override the comparison.

For authenticated module artifacts, call `VerifiedRelease.EvaluateUpdate(pin)`;
it also turns missing or invalid archive evidence into a named BREAKING result.
Acquisition/signature failures happen before a `VerifiedRelease` exists and must
likewise block the caller's update. Raw `Evaluate`/`EvaluateJSON` are for already
authenticated evidence or local analysis; self-consistent hashes are not trust.

The baseline must be the consumer's exact pinned version. For skipped releases,
the publisher/checker must construct a diff from that baseline's authenticated
snapshot to the target snapshot. An adjacent-release diff cannot stand in for
this comparison. The engine does not resolve tags or chain diffs implicitly.

## Distribution decision and outstanding acceptance

Use A first: shared offline core/CLI computation, with the existing GitHub App
path delivering exact-pin PRs for SAFE and notices for NEW CAPABILITY or BREAKING.
Keep B (a fleet service running as an Obin solution) as a future delivery adapter
to the same contracts. A service must not define a second verdict algorithm.
Delivery owns authentication, recipient authorization, retries, deduplication,
and rereading the baseline before proposing an update. SAFE describes contract
compatibility under declared evidence; it is not authority to deploy or a claim
about health, migrations, credentials, or undeclared behavior.

This core implementation does **not** complete the cross-repository acceptance
criteria of #578. There is no new running fan-out service or production CLI
command in this repository. The remaining owner work is:

- [CLI #753](https://github.com/codefly-dev/cli/issues/753): contract/usage
  generation, release publication, offline checks and structured output.
- [saas-starter #852](https://github.com/codefly-dev/module-saas-starter/issues/852):
  real SDK/behavioral contracts and GitHub App delivery, including the
  publisher-bound registration change.
- [platform-obin #25](https://github.com/obin-ai/platform-obin/issues/25): qualify
  `module-saas-starter → lodestar → platform-obin → lastlogin-go, lastlogin-python,
  wiki` using actual release artifacts, pins, delivery, and declared boot checks.

The checked-in YAML is a synthetic contract scenario, not a recording of that
chain. Tests exercise per-consumer differences, transitive type changes,
universal registration requirements, conservative unknowns, deterministic JSON,
and real signed-archive verification. No deployed-cell or real-chain update is
claimed from those tests. The core PR remains draft pending these integration
and acceptance gates; a merge must not close #578 prematurely.
