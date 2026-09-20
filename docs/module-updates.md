# Per-consumer module update contracts

`moduleupdate` computes SAFE, NEW CAPABILITY, BREAKING, or UNDETERMINED from a release's
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
without evidence produce UNDETERMINED when checked; ordinary release verification
does not newly require this artifact.

## Consumer usage and verdicts

A `ConsumerPin` binds the consumer identity, module version, and baseline
snapshot digest. Versions must be full semantic versions (an optional `v` is
accepted) or immutable 40/64-character hexadecimal revisions. Exact SDK/client
versions carry expected contract digests, expected dependency sets, and the
dependency-closed used surface. These expectations are compared with the
candidate, not the old module: a release that restores the pinned SDK's exact
contract can be SAFE even when the baseline was incompatible.
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

- SAFE: the candidate satisfies every pinned expectation and no unused optional item was added. Removing or changing
  a contract this consumer does not use can be safe.
- NEW CAPABILITY: optional items were added and no used contract changed. The
  result names the additions and preserves their documentation pointers.
- BREAKING: a used contract was removed. The result names the item and affected
  client/version. Changed digests alone are not proof of incompatibility.
- UNDETERMINED: required evidence is missing, stale or unsupported, including
  candidate content or dependencies that differ from pinned expectations.
  Universal requirements and their transitive dependencies
  apply even without SDK usage; the module baseline supplies expectations where
  the consumer has no explicit pin. A newly universal requirement must have an
  explicit satisfied expectation before the update can be safe.

Malformed input, unknown schema/fields, incomplete coverage, mismatched module
or baseline, stale SDK digests, missing usage dependencies, and a fabricated or
truncated diff all yield UNDETERMINED with `could not determine` and the reason.
BREAKING takes precedence over UNDETERMINED, which takes precedence over new
capabilities. All three detail lists are retained separately.
A valid explicit declaration of no usage is different from missing evidence.

Every candidate differing from a used item's pinned expectation is treated conservatively: this version does not prove
that a changed protobuf field or OpenAPI schema remains source/wire compatible.
It may therefore require review for an actually compatible used-type change.
A publisher cannot label a change safe to override the comparison.

For authenticated module artifacts, call `VerifiedRelease.EvaluateUpdate(pin)`;
it also turns missing or invalid archive evidence into a named UNDETERMINED result.
Successful archive/source preparation is cached on the immutable verified
release; transient preparation errors remain retryable. `ContractDiff` returns
a copy so callers cannot modify the cached evidence. For fleet evaluation of
already authenticated snapshots, `PrepareReleaseDiff` returns an immutable
`PreparedRelease`; its `Evaluate` method shares validation and indexes across
consumers and concurrent calls. Caller mutations of the input or a returned
result cannot change that prepared evidence.
Acquisition/signature failures happen before a `VerifiedRelease` exists and must
likewise block the caller's update. Raw `Evaluate`/`EvaluateJSON` are for already
authenticated evidence or local analysis; self-consistent hashes are not trust.

The baseline must be the consumer's exact pinned version. For skipped releases,
the publisher/checker must construct a diff from that baseline's authenticated
snapshot to the target snapshot. An adjacent-release diff cannot stand in for
this comparison. The engine does not resolve tags or chain diffs implicitly.

## Composition updates and owner authority

`Engine.Update` evaluates consumer usage before applying a release transition.
The CLI supplies `Engine.ConsumerPins`, keyed by the composition descriptor's
module instance name. Missing usage blocks adoption. The engine fetches and
verifies the exact locked baseline, derives both snapshots from authenticated
archives, and computes their complete diff rather than trusting an adjacent
release's asserted baseline. The semantic report includes the structured consumer
verdict and does not apply an unknown or breaking result. Initial selection and
same-release projection work explicitly report that no release transition was
evaluated. Projection readiness is not deployment approval or behavioral proof.

`TrustPolicy.Signers` is keyed first by package identity, then signing identity.
The package's configured repository and its authorized signer must both match;
a signer trusted for another package cannot authorize a release by claiming the
expected repository. CLI trust configuration must migrate to the package-scoped
map. This API is an intentional source change, not a global-signer fallback.
Trust configuration belongs to the component authority, never to a product
replacement declaration. This verification alone is not deployment admission:
local checkouts and derived runtime outputs need their own input-bound gate.

## Publisher-wide classification

`moduleupdate.ClassifyContractChange` is the shared structural classifier for
authenticated snapshots. Unchanged supported contracts classify as patch-level;
optional additions as minor; removals, including non-schema contracts, as major.
A changed digest, dependency set or universal requirement whose semantics cannot
be established returns `undetermined` with reasons. Incomplete coverage also
returns uncertainty. A patch-level contract result does not prove a functional
fix: implementations and stateful upgrades still require qualification.

Classification is publisher-wide, independent of a particular consumer's usage.
An unused removed RPC can require a publisher major while that consumer remains
compatible. No parent release is classified merely from a child's version.
The result explicitly identifies stable, development (0.x), prerelease,
development-prerelease and immutable-revision stages; none implies safety.
CLI release CI must consume this result instead of adding another classifier.

## Independent-upgrade requirement evidence

This is a reconciliation, not a completion claim for Core #584. The expanded
product-selection requirements remain in the existing Core PR #589 and CLI
PR #752; no additional tracker or release is implied.

1. **Defaults, requirements and selections:** `Engine.ResolveComposition` extends
   `Descriptor`/`PackageManifest` with exact nested module and service-agent
   replacements. Signed defaults, additional requirements, selected components,
   output admission and deployment approvals remain distinct. The Team A/Team B
   signed-HTTP fixture proves separate identities for independent replacements
   inside the same upstream release and unchanged sibling/default selections.
2. **Declarations-only acquisition:** `GitHubResolver.ResolveMetadata` downloads
   only independently authenticated manifest/provenance/signature assets.
   `ResolveComposition` computes participating instances, runtime/tooling assets
   and explicit source builds. Recorded HTTP tests reject unused dependencies
   and implementation archive acquisition. Missing metadata/artifacts are errors.
   CLI still must route nested compositions to this API and remove implicit
   dependency-source fallback in its orchestration; the old single-package
   projection path now rejects nested selections rather than ignoring them.
3. **Independent local checkouts:** `SetDevelopOverride`, `ClearDevelopOverride`
   and `Engine.Materialize` preserve the release lock while identifying local
   content. `TestIndependentLocalCheckoutsBindDirtyContentAndRestoreWithoutDeletingFiles`
   exercises two independently initialized Git repositories, uncommitted edits,
   same-path invalidation, sibling isolation and restoration without mutation.
   It also covers a linked worktree. VCS `.git` files/directories are neither
   projected nor part of the local content digest: background Git maintenance
   is not a source edit, and a projection must not inherit the worktree's Git
   administrative pointer. Every other local source path remains in the digest.
   `TestLocalMaterializationRejectsSourceMutationDuringGeneration` runs a real
   generator and refuses publication after it changes the source. These are
   supplemented by nested local-content/build-plan/restoration tests in
   `resolution_test.go`, including rejection by deployment admission.
4. **Deployment authority:** `VerifyRelease` now requires the selected package's
   authorized signer. Real signed fixtures reject tampering, another component's
   signer and missing authority. `CheckDeploymentInputs` verifies actual output
   bytes and current component authorities; `AdmitDeployment` additionally
   checks signed qualification for exact inputs and target bindings. Signed
   owner-authorized derived-build fixtures reject private source, changed
   selections, unauthorized builders and tampered output. CLI must integrate
   these gates and owners must authorize their actual release/build workflows.
5. **Effective identity:** service connection, in-flight and instance caches
   check the executable digest, not just agent name/version.
   `TestCachedAgentBindsExecutableContent` covers replaced bytes, startup races,
   preservation of the running process and explicit replacement. Projection
   identities already include package and contribution content. Resolution now
   produces a combined product identity/difference record with protected HMAC
   configuration identity. Deployment records bind actual/derived outputs and
   separate targets to approval evidence. Downstream build caches and observed
   running/rollback records still need to consume these identities.
6. **Compatibility:** `TestEngineUpdateUsesAuthenticatedConsumerBaseline` proves
   real signed baseline/candidate evaluation for skipped releases, a compatible
   optional REST capability, a removed used route, changed authorization
   requirements, missing/stale usage and a tampered baseline. Protobuf tests
   derive transitive type identities and reject stale catalogs. Changed digests
   remain UNDETERMINED rather than fabricated semantic breaks. Full structural
   gRPC/REST compatibility analysis and functional/stateful upgrade qualification
   still require implementation and representative owner/consumer runs.
7. **Versioning:** `ClassifyContractChange` and its tests share the supported
   structural rules, explicit uncertainty and 0.x/prerelease stages. CLI and
   release CI do not yet consume it. Semantic qualification of changed contract
   contents remains unsupported and visibly undetermined.
8. **Upstream adoption:** `UpstreamAdoptions` exports owner/default/replacement
   facts and optional matching approval identity. `ProposeOverrideRemoval`
   re-resolves the full candidate with/without the replacement; signed fixtures
   reject changed requirements and moved targets. It proposes, never mutates
   declarations or opens requests. New selections require new qualification.

See [composition selections](composition-selections.md) for API contracts,
signed metadata publication, derived-build authority and CLI integration duties.

CLI migration specifically includes `pkg/composition/pinned.go`'s trust loader
(flat signing keys must become package-bound), supplying actual consumer pins
to `Engine.Update`, and displaying/blocking `VERDICT_UNDETERMINED`. The owner
must supply authoritative keys and release metadata; a product declaration
cannot grant itself that authority. Published-agent lifecycle qualification is
separate from independently built protocol fixtures. The last official-service
admission run rejected all 17 installed releases for missing declarations; this
work does not forge declarations or republish those agents.

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
criteria originally recorded in #578. There is no new running fan-out service or production CLI
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
claimed from those tests. Core #584 and CLI #753 retain the outstanding
qualification; the Core PR remains draft pending these integration and acceptance gates.
