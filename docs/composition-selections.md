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

The owner manifest declares named `release-artifacts` with purpose, location and
digest. Selected services name their required runtime artifacts. Lifecycle-agent
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
statement binds the complete selection identity, exact owner source and output
digest. The trusted builder must verify the acquired source/tooling and attest
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
time, deploy immutable digests, and record observed running identities rather
than reporting approval as proof that anything is running.

`UpstreamAdoptions` exports the owner, inherited/replacement selections,
requirements, rationale and an optional matching approval identity. Core does
not open requests. `ProposeOverrideRemoval` resolves the entire candidate both
with and without a replacement and compares effective inputs. Moved targets,
changed requirements and different artifacts prevent removal. Its result is a
proposal, not a mutation or deployment approval; the new full selection needs
its own compatibility checks and qualification.

## Consumer contract evidence

`Engine.ConsumerPins` accepts `SignedConsumerUsage`, not unauthenticated pins.
`ConsumerAuthorities` binds each instance to its expected consumer and usage
signers. The statement binds the instance, actual composition digest, complete
usage and expiry. Updating a module verifies both the usage statement and the
actual signed baseline/candidate before executing candidate generators. Changing
consumer inputs during tests prevents projection/lock publication.

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

Owners must publish the signed metadata and declared artifacts. CLI must wire
resolution, acquisition, trusted configuration identity, authenticated consumer
usage, build attestations, deployment policy and observed-running receipts into
its commands. The Core boundary tests are not evidence of those owner/CLI
publications or production integrations.
