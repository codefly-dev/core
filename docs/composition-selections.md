# Independent composition selections

`Engine.ResolveComposition` extends `Descriptor` and `PackageManifest` with
instance-scoped selections. It is a resolver and acquisition planner, not a
checkout manager, builder, live-agent admission check or deployment command.

## Declarations and participation

The descriptor's `modules.include` selects direct module instances declared in
the base's signed package manifest. Each `ProvidedModule` records an exact
default release (ID, version and archive digest), interface/configuration
requirements, and its own included modules and services. An empty include list
selects nothing; unused dependencies are not resolved. The root release is an
explicit `ReleaseSelection`, checked against the descriptor's base constraint.

Replacements use exact paths, for example
`modules/saas/services/store/agent` or `modules/saas/modules/accounts`.
Short names, wildcards, duplicate replacements and absent targets are errors.
A replacement retains the component ID and its externally configured release
authority. Its additional requirements cannot weaken inherited requirements.
The resolver never edits an upstream manifest or changes an ancestor's version.

`Provides` declares exact interface/configuration contract versions. These are
checked against both sets of requirements. This is declaration compatibility,
not proof of protobuf/OpenAPI semantic compatibility or functional correctness.
It does not replace authenticated live agent protocol/capability admission.

## Metadata and acquisitions

`GitHubPackage.MetadataAsset` names the separately downloadable package manifest.
The existing owner-signed provenance binds its exact bytes using
`manifestDigest`, as well as the immutable package archive digest. The manifest
inside a full archive must have the same digest. Metadata-only resolution never
downloads that archive. Old releases without signed metadata fail explicitly;
there is no source-checkout or whole-archive fallback.

The owner manifest declares named `release-artifacts` with purpose, location,
digest and an explicit `media-type` when used in execution. Selected services
name their required runtime artifacts. Lifecycle-agent
artifacts are separate from deployed runtime outputs. Clients and additional
contracts are acquired only when explicitly requested. A missing artifact is an
error, not permission to recursively clone or build source.

An agent's declared usage is `build` or `lifecycle`. Changing a build agent
invalidates reuse of the inherited runtime output. Core requires an explicit
source-build target, owner permission (`allow-derived-builds`), an immutable
owner source artifact and released build tooling. It returns build requirements
instead of treating the old runtime output as newly built. Local module
substitutions also return build requirements, without acquiring released source.

CLI must execute these acquisition/build requirements with bounded transports,
digest validation and its install/cache locks. Existing single-package
`Update`/`Materialize` reject nested descriptors before side effects; they cannot
silently ignore replacements. CLI must route nested declarations to the resolver
and its orchestration path, not remove this guard.

For single-package execution, `Engine.ToolVersion` must identify the executing
host. Core never substitutes its own release version. A package's explicit
`minimum-codefly-version` tooling requirement remains enforced, separately from
composition contract negotiation and live agent admission. Upgrading the linked
Core library cannot satisfy or invalidate that requirement. Metadata-only
resolution does not execute package tools and does not require a host version.

## Local development and identity

`ResolutionOptions.LocalCheckouts` maps exact module targets to absolute paths.
Resolution hashes actual files, including uncommitted changes, before and after
reading the manifest. `.git` administration is excluded, not source files.
Symlinks/nonregular inputs fail rather than silently escaping the input set.
`CheckLocalInputs` rejects changes after resolution. Omitting a substitution
restores the released selection without writing or deleting the checkout.

The immutable resolved value exports a defensive-copy `ResolutionRecord` and
one deterministic identity. It includes original and selected releases, owner
provenance, inherited/additional requirements, selected services, local content,
acquisitions, build inputs, differences and a protected configuration identity.
It also binds product contributions, test commands and service bindings through
`ProductInputIdentity`. Products with contributions must supply an absolute
`ResolutionOptions.ProductRoot`; contribution bytes, names and executable modes
are hashed, including test inputs. Resolution and deployment admission reject
input drift. No product checkout is needed when no contributions are declared.
`ConfigurationIdentity` uses HMAC-SHA-256 with a caller-owned 256-bit minimum key;
the key is neither stored nor returned. Keep that key stable and private across
resolutions. Deployment target bindings are identified separately.

## Deployment admission

`CheckDeploymentInputs` rechecks owner signatures against current trust and
hashes supplied runtime content. Local substitutions are rejected regardless
of clean/dirty Git state. Self-published or privately patched output does not
match the owner's signed runtime identity and is rejected. A resolver result is
not deployment approval.

An unchanged-source rebuild requires `SignedDerivedOutput` from a separately
configured, component-bound `TrustPolicy.BuildSigners` identity. The signed
statement binds the complete selection identity, exact owner source, prepared
build execution identity, output digest and acquisition URI. The trusted builder
must verify the acquired source/tooling and attest
what it actually built; a product cannot authorize its own builder. Derived
provenance is retained separately even if a reproducible rebuild has identical
output bytes. The upstream artifact is never overwritten.

`AdmitDeployment` additionally requires authenticated, unexpired qualification
for the exact selection, actual output bytes and target bindings, under the
caller's deployment policy and every owner's signed `required-qualifications`.
Missing owner declarations are errors; an intentional empty list must be
explicit. Replacements always require `component-compatibility` and `functional`
qualification. The approval record preserves signed evidence and
its validity window. Functional/stateful tests and rollout approval belong to
the authorities configured in that policy. Callers must re-admit at deployment
time, apply only the verified render outputs, deploy immutable digests, and record observed running identities rather
than reporting approval as proof that anything is running.

## Artifact execution

Each selected service declares `artifact-operations` in its owner's signed
`PackageManifest`. These extend the existing manifest, not a separate language:

```yaml
artifact-operations:
  - operation: render
    protocol: codefly.builder.deploy/v1
    executor:
      target: services/api/agent
      name: renderer
    inputs:
      application:
        name: runtime
    outputs:
      manifests: application/yaml
```

Artifact references name a declared artifact in the same module (omitted
`target`) or a descendant instance relative to that module. The resolver binds
them to the actual selected instance releases, including scoped replacements.
It never derives a renderer or a media type from an artifact's name or URI.
An undeclared artifact or nonparticipating target fails; render cannot acquire
implementation source. Source inputs require an explicitly selected build.
Builds consuming another pending derived output are rejected, not silently
given the inherited runtime output. Build output media types must match the
runtime outputs they replace. Every service runtime artifact must feed render.

`ResolutionRecord.Operations` exposes resolved executor/input acquisitions and
named output media types. `Engine.PrepareArtifactExecution` rechecks authority
and local content, and for render authenticates actual runtime bytes through
`CheckDeploymentInputs`. It returns an immutable prepared value whose `Request()`
is a defensive-copy `base.v0.ArtifactExecution`. The deterministic identity binds
selection, instance/service, operation protocol, executor bytes, input artifact
identities, protected configuration, target bindings and output declarations.
Derived inputs use the signed derived URI and digest, never upstream locations.
For multi-instance orchestration use `Engine.PrepareArtifactExecutions` to
authenticate runtime streams once and return a bound request per selected service.

CLI passes that request to Builder `BuildRequest.execution`, Builder
`DeploymentRequest.execution`, or Solution `RenderRequest.execution`, according
to the declared protocol. It must supply the corresponding effective
configuration and target values used to produce the protected identities. A
digest does not carry those values or authorize inventing them. Solution's
legacy `artifact_reference` must be empty on bound calls. Builder's verified
process artifact digest and Solution's verified package identity respectively
must match the declared executor; these representations are not interchangeable.

Bound calls require absolute caller-owned staging directories: Build and Deploy
use `output_directory`; Solution uses `destination`. These calls stage outputs
only, never apply workloads or publish releases. The live capability gate and
receipt check are in `services.BuilderAgent` and `solution.Client`. Existing
unbound calls retain their separate behavior but cannot manufacture admission
for a resolved composition. Missing/future capabilities refuse before dispatch.

The response's execution receipt maps each declared output name to its media
type, SHA-256 digest and relative regular-file path. Directory outputs must be
packaged explicitly. `VerifyArtifactExecutionDirectory` opens these paths within
the staging root, rejects undeclared/nonregular files and hashes actual bytes.
`VerifyArtifactExecution` also supports
caller-owned readers, which must supply the corresponding actual output bytes.
Both reject missing, duplicate, mismatched or additional outputs. The returned
sealed `VerifiedArtifactExecution` values go into `DeploymentInputs.Executions`.

`CheckDeploymentInputs` without executions validates runtime inputs only. With
executions it also computes `DeploymentRecord.ExecutionIdentity`. Qualifications
must sign that identity. `AdmitDeployment` requires a verified render operation
for every selected service and binds the resulting output identities into the
approval. Metadata without explicit render mappings cannot be deployed through
this path. A valid receipt is not approval; changed output bytes require new
qualification even when the selected runtime bytes are unchanged.

CLI owns acquiring, invoking, keeping staged files isolated from writers,
checking their bytes again at the apply boundary and recording actual rollout
state. Owner executors must implement and advertise `artifact-execution/v1`;
merely linking Core cannot satisfy it. No framework/provider-specific policy or
new environment wrapper is introduced by this contract.

`UpstreamAdoptions` exports the owner, inherited/replacement selections,
requirements, rationale and an optional matching approval identity. Core does
not open requests. `ProposeOverrideRemoval` resolves the entire candidate both
with and without a replacement and compares effective inputs. Moved targets,
changed requirements and different artifacts prevent removal. Its result is a
proposal, not a mutation or deployment approval; the new full selection needs
its own compatibility checks and qualification.
Repeated inherited requirements do not prevent removal: equivalence compares
deduplicated effective constraints, while the record retains their provenance.

## Consumer contract evidence

`Engine.ConsumerPins` accepts `SignedConsumerUsage`, not unauthenticated pins.
`ConsumerAuthorities` binds each instance to its expected consumer and usage
signers. The statement binds the instance, actual composition digest, complete
usage and expiry. Updating a module verifies both the usage statement and the
actual signed baseline/candidate before executing candidate generators. Changing
consumer inputs during tests prevents projection/lock publication.
Update and rollback publication serialize on the module lock and compare the
current selection with the baseline observed before qualification. A competing
commit invalidates a stale operation instead of allowing it to overwrite the
new selection. The operation must resolve and qualify again.

`BuildPackageContractEvidence` derives canonical source bytes alongside the
existing snapshots. `PrepareReleaseDiffWithSources` and
`ClassifyContractChangeWithSources` verify those bytes against item digests before
applying supported rules. Snapshot identities are unchanged by adding this
analysis. Supported rules cover simple optional protobuf field additions,
removed/changed fields, optional/required OpenAPI parameters and incompatible
transitive object types. Changed validation, authorization, dependency edges,
oneofs, enums and unsupported schema semantics remain explicitly undetermined.
This does not infer implementation correctness or stateful upgrade safety.

## Boundary tests and delivery

`composition/resolution_test.go` serves real signed release files over HTTP.
It checks instance isolation, unchanged upstream metadata, deterministic
identities, recorded metadata-only acquisitions, contract conflicts, dirty
content, restoration, exact derived builds, runtime tampering, revoked keys,
stale evidence, target changes and full upstream catch-up resolution.
`composition/artifact_execution_test.go` checks signed declaration resolution,
output-byte verification, directory containment and approval binding.
`agents/services/artifact_execution_test.go` builds a separate protocol peer and
exercises Build, Deploy and Solution Render over TCP, including missing/future
capabilities and bad acknowledgements. That fixture qualifies the host boundary,
not the implementation or publication of any production agent.

Owners must publish the signed metadata and declared artifacts. CLI must wire
resolution, acquisition, trusted configuration identity, authenticated consumer
usage, build attestations, deployment policy and observed-running receipts into
its commands. The Core boundary tests are not evidence of those owner/CLI
publications or production integrations.
