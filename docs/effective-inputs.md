# Effective inputs, schema v1

`Agent.GetEffectiveInputs` is an additive, provider-neutral discovery RPC.
Agents advertise `effective_inputs_versions: [1]` through
`services.Advertisement`. Core's `ciinputs` package validates declarations,
computes deterministic task identities and compares two snapshots. CLI planning,
scheduling, evidence persistence and result storage are separate consumers.

## Inventory and discovery

1. Obtain `AgentInformation.validation` and call `ciinputs.Required`. For legacy
   validation advertisements, retain the existing RPC capability probes and
   supply their resulting phase/suite inventory explicitly.
2. Bind a snapshot token to source state, the dependency graph, requested task
   invocation and resolved execution context. Call `ciinputs.Discover` for both
   reference and candidate snapshots. The agent inspects `revision`, or the
   worktree when empty, without starting application services. It must decline
   completeness if it cannot inspect that exact snapshot, including historical
   configuration or historical discovery semantics.
3. Pass the full independent inventory to discovery. A task key is phase plus
   suite; suites come from `TestSuiteCapability`, never from discovered files.
   Preserve the union of reference and candidate required tasks. A removed suite
   is a coverage/policy decision for the consumer, not permission for discovery
   to silently remove required validation.
4. Use `Changed` on the evaluated snapshots. A conservative task requires the
   existing whole-service selection and dependency closure. Never use its empty
   identity as a cache key. A missing task in either snapshot is changed.

No advertised v1 support, `Unimplemented`, an unsupported response version or
unknown protobuf fields produce conservative selection without cache reuse.
Missing/incomplete tasks and unresolved inputs do the same per task. Other RPC
failures propagate. Malformed declarations, duplicate identities, unrequested
tasks and snapshot mismatches are errors. `Evaluate` takes the discovery request,
and both evaluation and discovery reject returned inputs that contradict matching
caller-resolved context keys, including identity, sensitivity and file metadata.
Context not consumed by a task does not enter its identity. An agent must not advertise support
until it implements discovery. Older clients ignore the new capability field;
existing Agent methods and validation advertisements retain their wire numbers.

## Completeness is an agent assertion

A complete task lists every effective input, including the selected tests and
fixtures, invocation selectors/options, runtime configuration, effective
environment, shared configuration, lockfiles, generation tools and their inputs,
resolved libraries, generated contracts, artifacts and required validation
results. Resolved agent/plugin and toolchain identities are mandatory for reuse.
The toolchain identity must cover the actual executable, relevant platform and
backend, not just a version range. Mutable tags, unpinned remote resources,
ambient environment, time, randomness and live external state need immutable
identities or must remain unresolved. Cache eligibility here is necessary, not
sufficient: the caller must also enforce deterministic execution, successful
results, evidence provenance and its reuse policy.

Core cannot prove completeness by reading application fixtures. The agent owns
native import/build discovery, suite semantics, dynamic fixture selection and
configuration resolution. A complete declaration must include configuration and
other inputs that govern discovery itself. Filename suffixes never establish a
production exclusion. If native discovery is incomplete, set `complete: false`.
Do not promote a generic source manifest or an import trace alone to a complete
build declaration: Docker context/recipe, framework config, plugins, generators,
assets and other effective inputs still matter.

Each input key is `(kind, owner, name)`. Owners identify workspace resources,
including shared resources outside the target service directory. Files use
canonical slash-separated paths relative to that owner's workspace namespace,
content identities and Git-style modes. A symlink includes its link-text identity
and mode plus separate entries for all consumed target content. External targets
must be explicitly identified or unresolved. Submodules require both a pinned
commit and any effective dirty content; otherwise discovery is incomplete.

Discover both snapshots independently. Hashing the full declaration includes
removed/moved paths, owner changes, modes, link targets and added/removed
consumption edges. New files also require discovery in the candidate: consumers
must not filter candidate changes through only the candidate's current path list
or reuse an old trace without verifying the discovery inputs.

## Dependencies and test semantics

`runtime_services` specifies only which services must be available for execution.
It does not automatically hash all runtime dependency content into every phase.
Consumption is explicit:

| Task | Runtime services | Content dependencies |
| --- | --- | --- |
| Isolated unit/pure suite | None | Selected sources, tests, fixtures and test configuration |
| Consumer integration suite | API and its required runtime closure | API `SERVICE_IMPLEMENTATION`, consumed libraries/contracts, fixtures and effective configuration |
| Image build | Usually none | Artifact prerequisites, image recipe/context, build toolchain and effective production inputs |

An agent resolving a consumer integration suite includes implementation identities
for every service whose behavior it exercises, including relevant transitive
services. A changed API implementation then changes the consumer's integration
identity without changing unrelated unit or image identities. A contract/library
identity appears in every declared consumer. `ARTIFACT` and `VALIDATION` entries
name their producer/output and identify the actual prerequisite. These entries
express dependency edges; CLI scheduling must resolve them before execution.

Suite names alone have no semantics. The existing Next.js agent advertises
`unit` with `START_DEPENDENCIES`, `pure` with `NONE`, `integration`/`e2e` with
`START_DEPENDENCIES` and `smoke` with `START_STACK`. Adoption must preserve those
contracts. The Go agent's existing validation advertisement remains its suite
inventory authority.

## Identity and sensitive evidence

Canonical identity is `sha256:` plus lowercase SHA-256 of
`codefly.effective-inputs/v1` followed by a NUL byte and the deterministic protobuf
encoding of `TaskInputs`, with inputs sorted by kind/owner/name and runtime service
names sorted lexicographically. Input order has no execution semantics. Snapshot
tokens are excluded so unrelated snapshot changes do not invalidate a task.
Changing this canonicalization requires a new schema/domain version.

Only identities cross this boundary. `sensitive: true` accepts an unresolved
identity or `HMAC_SHA256`; a plain hash is prohibited because low-entropy secrets
can be guessed offline. `ciinputs.Protect` uses a private key of at least 256 bits,
a public namespace identifying the trust scope/key version, and domain-separated
HMAC-SHA256. Rotate the namespace with the key. Key material and secret values
must never appear in names, owner IDs, namespaces, diagnostics or evidence.
An unkeyed digest of a secret is not protection. Without a trusted protection key,
leave the input unresolved and disable reuse. External `VERSIONED` identities
must be immutable, non-sensitive identifiers in a versioned namespace.

## Conformance and adoption status

`go test ./ciinputs` exercises gRPC compatibility, conservative fallback, required
suite retention, input kinds, distinct task dependencies, snapshot/edge changes,
symlinks and protected identities. Native consumption checks run with:

```sh
go test ./ciinputs -tags ciinputs_conformance -count=1 -timeout 300s
```

Python wire and cross-language identity conformance runs with the SDK dependencies:

```sh
PYTHONPATH=generated/python/codefly-cli/codefly_cli python3 generated/python/codefly-cli/codefly_cli/tests/test_effective_inputs_contract.py
```

The native checks require Go, Node/npm and npm registry access. They compile/test a
real Go project using `go list` inputs and embed data, and build a pinned Next.js
project using webpack's observed file dependencies. They prove that a real
production import with a test-like name is retained and an unrelated test can be
excluded. They are representative conformance fixtures, not complete discovery
implementations for arbitrary Go/Next.js projects.

Released-agent adoption remains in `codefly-dev/service-go` (the generic Go layer
used by `service-go-grpc`) and `codefly-dev/service-nextjs`. Those repositories are
not part of this Core checkout. They must implement the RPC through their existing
Agent/Advertisement interfaces and add full framework-specific conformance before
advertising v1. Until then Core explicitly retains conservative legacy behavior.
