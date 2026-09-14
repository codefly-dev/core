# Runnable (experimental)

A **Runnable** is a packaged implementation of a typed finite operation: typed
input in, typed output out, declared dependencies, requirements and effect
semantics. It is declared in `runnable.codefly.yaml` and owned by a module next
to the module's services and jobs, so one module release can ship both a
service surface and runnable work.

This document covers what core owns. Language agents (`runnable-python`,
`runnable-go`), CLI commands, the local launcher and Orchestration's execution
adapters live in their own repositories and consume the contracts described
here.

## Runnable vs. Job

| | Job (`job.codefly.yaml`) | Runnable (`runnable.codefly.yaml`) |
| --- | --- | --- |
| What it is | Deployment/dependency work codefly orders: a migration, a seed | A typed operation an orchestrator invokes as durable work |
| Who starts it | codefly, as part of a phase (`run`, `deploy`) or on a schedule | An orchestrator, under its own task identity and attempt policy |
| Consumers wait on | its `completion` (a dependency-kind prerequisite) | nothing; an invocation completing with valid output is a fact about that invocation, not a readiness signal |
| Retries | `execution.retries` on the job | none in core; `execution.recovery` says what an uncertain outcome may be resolved with |
| Identity | module/name | module/name **@ version**: a release is immutable, versions coexist |

Core never schedules an invocation and never reinterprets a Job's retry settings
as a Runnable's effect policy. A service being ready to accept requests
(declared readiness, `docs/readiness.md`) and a finite invocation completing
with valid output are separate facts that never stand in for one another.

## Declaration

```yaml
# runnables/word-count/runnable.codefly.yaml
kind: runnable
name: word-count
version: 0.1.0                 # immutable release, strict semver
agent:                         # exact language agent: kind is uniform, name is the language
  kind: codefly:runnable
  name: python
  version: 0.0.1
  publisher: codefly.dev
contract:
  protocol: codefly.runnable/v1
  input:
    fields:
      - name: text
        type: string
      - name: options
        type: object
        optional: true         # key may be absent
        fields:
          - name: stop_words
            type: array
            nullable: true     # value may be null
            items:
              type: string
  output:
    fields:
      - name: count
        type: integer
entrypoint:                    # launched facilities only; see below
  handler: handler.py          # the author entrypoint
  inputs: [pyproject.toml, uv.lock]   # everything else whose content changes the package
execution:
  facilities: [native, kubernetes]   # native | kubernetes | service | function
  timeout: 2m
  cancellation: signal         # none | signal
  recovery: recompute          # recompute | receipt
  payload:
    max-input-bytes: 65536     # default 1 MiB for both bounds
  logs:
    max-bytes: 1048576         # default 4 MiB per stream; over it, logs truncate
service-dependencies:
  - name: store            # module defaults to the runnable's own module
    kind: runtime
    endpoints: [{ name: tcp }]
workspace-configuration-dependencies: [openai]
```

Referenced from the owning module (`module.codefly.yaml`, or the flat
`workspace.codefly.yaml`) with the same reference shape as services and jobs:

```yaml
services:
  - name: store
runnables:
  - name: word-count
  - name: counter
    path: work/counter         # relative to the module, or absolute
```

Loading is strict and rejects, with a message naming the rule: an unknown key
anywhere outside `spec` (so a misspelled `optionnal:` cannot load as its
opposite), an escaping name or path, a non-strict version, a `latest` or
unpinned agent, an agent of another kind, an unsupported `protocol`, a schema
outside the bounded profile, a missing handler, an unknown facility,
cancellation or recovery, and a missing or non-positive timeout. `Save` refuses
to write a declaration that would not load, and `Module.NewRunnable` writes the
declaration before it references it, rolling both back on failure, so a module
never points at a runnable that is not on disk.

Paths are confined lexically (`handler`, `entrypoint.inputs`, and the paths a
descriptor pins). Whoever reads them — the agent generating the harness, the
CLI digesting build inputs — must resolve symlinks and refuse a target outside
the runnable directory before trusting the content; core never opens the files.

A flat workspace whose legacy `module.codefly.yaml` declares `runnables:` is
migrated by this version. An older `codefly` loading the same workspace
migrates only services and jobs and deletes the module file, losing the key:
upgrade every checkout of a workspace before adding runnables to it.

### Bounded schema profile

The contract's schema is a small typed profile, not JSON Schema: `object`,
`array`, `string`, `integer`, `boolean`, with `optional` (key may be absent) and
`nullable` (value may be null) declared separately. Field names are
identifiers so every language agent can generate typed bindings. Enumerations,
arbitrary JSON Schema, expressions and coercion are not promised because a YAML
key can be written; `codefly.runnable/v1` is the only protocol accepted.

## Immutable installation facts

`proto/codefly/base/v0/runnable.proto` carries the wire contracts, generated
into `generated/go/codefly/base/v0`; `runnable-python` generates its own Python
bindings from the same sources. The `runnable` Go package validates,
canonicalizes and digests two of them.

**`RunnablePackage`** (`codefly.runnable-package/v1`) is the descriptor of one
built release: identity, exact agent, contract, execution bounds, pinned build
inputs (handler, declared inputs, generated harness, toolchain, effective
configuration), typed service dependencies with the endpoints they consume,
the workspace configurations the invocation needs, and the implementations of
the operation: built artifacts (a `NATIVE` package with its launch command
and/or a digest-pinned `IMAGE`, each with its platform), owner service methods
and remote functions. Its `digest` is the sha256 of a canonical form core owns
(proto3 JSON with sorted keys, prefixed by a digest-format identifier) — not
of the wire encoding, which protobuf only keeps stable within one binary. A
message carrying fields outside the schema it claims is rejected rather than
hashed with them ignored. Every unordered set (facilities, inputs, artifacts,
dependencies, endpoints, configurations) is sorted first, so equal content in
another order is the same release. A resolved execution-plan fingerprint
(`docs/design/execution-plan.md`) identifies a plan, not bytes; the package
digest is what proves two builds are the same executable.

`runnable.CompareRelease(existing, incoming)` is the registration rule: same
identity and same digest is an idempotent registration (`nil`); same identity
and a different digest is `ErrConflict`. A new version is a different release
and coexists.

**`RunnableBinding`** (`codefly.runnable-binding/v1`) is the installation of
one verified package on one execution facility. It pins the package digest,
the facility, the implementation selected from that package, the coordinates of
the executor it dispatches to, the reachable addresses of the declared
service dependencies (only the endpoints the dependency consumes), the *names*
of exactly the workspace configurations the package declares, and the *names*
of credentials the facility resolves at launch. Coordinates, configuration and
credential references live here, under deployment trust; a value never does,
and an invocation payload carries none of them. Mappings are sets: an installer
emitting them in another order has installed the same binding.
`VerifyBinding` fails against a rebuilt package with the same identity, so an
existing binding can never be routed to newer bytes.

### Operation, implementation, binding, availability

Four things the contract keeps apart:

| | What it is | Where it lives |
| --- | --- | --- |
| Operation | the immutable callable identity, its typed contract, dependencies, bounds and effect semantics | `runnable.codefly.yaml`; a package's identity, contract and execution |
| Implementation | one way that operation is actually carried out | a package's artifacts, service operations and functions |
| Binding | a trusted installation selecting one implementation, one target and the references it resolves | `RunnableBinding` |
| Availability | whether an installed target can run it right now | nowhere in core; an orchestrator observes it |

Availability is not registration: a binding stays installed while its executor
is down, scaled to zero or being replaced, and core records no readiness for
it. A binding is not a readiness signal either (`docs/readiness.md`).

`execution.facilities` names *dispatch forms*, not locations, and calling an
operation does not inherently start a process:

| Facility | The implementation it dispatches | Invocation protocol | Its target coordinates |
| --- | --- | --- | --- |
| `native` | a `NATIVE` artifact: an installed package the launcher starts | `codefly.runnable/v1` | `host`: the launcher, and the directory the package was unpacked into, absolute as the artifact's own platform spells it |
| `kubernetes` | an `IMAGE` artifact, run as a finite invocation Job | `codefly.runnable/v1` | `cluster`: kubeconfig context, namespace, optional service account |
| `service` | a method an owner service already publishes | `codefly.runnable.service/v1` | `service`: the resolved mapping of the endpoint it is published on |
| `function` | a provider-managed remote function | `codefly.runnable.function/v1` | `function`: provider, region and the deployed resource |

One owner may publish the same contract over shared code both as a service
method and as a runnable operation. That is what the `service` form is for: it
installs *without an executable package*, and such a release carries no
`build`, because no harness, toolchain or configuration digest was produced for
it — the owner's own service agent built the service. Inventing them to fill a
required field would assert a package nobody built, so a package with no
artifact must omit `build` and a package with one must pin it. Core never loads
owner code and never decides which process the owner runs the method in.

A service-only operation may name its owner's pinned `codefly:service` builder
as `agent`: that agent built the running implementation. It needs no separate
language Runnable builder. Native/Kubernetes artifacts still require a
`codefly:runnable` agent, and service builders are not accepted for functions.

A `service` implementation preserves what the owner already publishes — the
service, endpoint, method name and the identities of the request and response
messages — alongside the `adaptation` that maps a bounded payload onto them.
Bounded JSON is the only accepted adaptation. The bounded schema profile does
not cover arbitrary protobuf, streaming or dynamic values, and a method whose
messages fall outside it is rejected rather than coerced: reusing an owner's
schema is a claim that has to be stated, not inferred from shapes that happen
to line up.

The service, module and endpoint names it points at carry `Endpoint`'s own
naming rules, and so do a `service-dependencies` entry's `name`, `module` and
`endpoints`. Both are matched against a real `Endpoint` before anything can be
bound, so a coordinate that no endpoint could spell — two characters long, an
underscore, a leading hyphen — would be a release that validates and can never
be installed. The rules are enforced from one place and pinned by a test that
requires both sides to accept exactly the same names.

A `function` implementation names only what a build knows, the provider and the
handler entrypoint. Provisioning the function, calling the provider SDK and
carrying its completion back belong to the adapter that owns the provider.

**A form decides what the rest of the declaration may say**, and every rule
below is enforced when the declaration loads and again on the package, so a
descriptor assembled by a CLI cannot assert what an author could not write.

`contract.protocol` names how the operation is reached, so it follows from the
facilities. `codefly.runnable/v1` is the launcher/harness seam — three
environment variables, an invocation document and a result file — and it is
the wrong answer for a method reached over the owner's endpoint or a function
reached over the provider's transport, which is why each of those has its own
protocol. Facilities whose protocols differ cannot share one release: a
contract cannot name two transports, and a consumer choosing a transport from
the protocol would have nothing to choose. `native` and `kubernetes` share
`codefly.runnable/v1`, so one release still covers both.

`entrypoint` is a launched-facility fact. A method an owner already publishes
is built by that owner's service agent, so there is no author entrypoint in
the runnable directory and none may be declared; requiring one would make the
author name a file that nothing reads and no build ever digests, which is the
same fabrication the missing `build` avoids on the package.

`logs.max-bytes` bounds what a launcher captures from a process's streams.
Where no facility is launched nothing captures anything, so declaring a bound
there is rejected and the wire form carries none, rather than stating a number
that describes nobody's behavior.

`cancellation: signal` promises a harness that reports `INTERRUPTED` when it is
signalled, which needs a launcher that owns the execution. It is refused for
`service` and `function`. The refusal is deliberately whole-declaration rather
than per-binding: cancellation is a promise the *operation* makes to its
callers, so a release may not honor it on one facility and quietly not on
another.

### The execution target

Every binding carries the coordinates of the executor it dispatches to, as a
versioned `RunnableTarget` (`codefly.runnable-target/v1`): the Codefly
environment the installation is scoped to, the immutable `revision` of the
installed target, and one coordinates variant that must match the facility.
Stating them once, here, is what keeps them out of an invocation payload and
out of `dependency_network_mappings` — those address what an invocation
reaches, this addresses what executes it. The variant is also what holds the
dispatch forms apart structurally: a method running inside a process its owner
operates has neither a launcher nor an install root, so it cannot be written as
a `native` binding.

The cluster spelling follows the existing Kubernetes deployment inputs
(`codefly.services.builder.v0.KubernetesDeployment`), which the target does not
import: that message carries manifest-generation inputs, not dispatch
coordinates. A service target reuses `NetworkMapping`, the resolved address
contract dependencies already use, and must address exactly the endpoint the
selected method is published on.

Implementation and target are both part of the binding digest, and
`runnable.CompareBinding(existing, incoming, pkg)` is the installation rule
beside `CompareRelease`: an installation is identified by its release, its
facility, and its target's environment and revision. Same identity and same
digest is idempotent (`nil`); same identity and a different digest is
`ErrConflict`. A second environment, or a re-provisioned target carrying a new
revision, is a separate installation that coexists — so replacing an executor
never reroutes an existing binding onto it, and a task keeps the release and
implementation it selected.

### Wire compatibility

The changes above are additive on the wire. `RunnableBinding.artifact` keeps
field 5 and moved into an `implementation` oneof, so an executable-package
binding encodes exactly as it did before the other forms existed; Go callers
set `Implementation` rather than `Artifact`. `RunnablePackage.build` and
`artifacts` dropped their required and non-empty constraints, because a release
with no artifact has neither, and the Go validation states the pairing instead.
`Runnable.handler` and `RunnableExecution.max_log_bytes` dropped theirs for the
same reason: both are launched-facility facts, and the Go validation now
requires each exactly where its form provides it. A binding also no longer
carries an endpoint's `api_details`: that field holds raw `.proto` source or a
serialized OpenAPI document and changes whenever the owner regenerates, which
would make an installation whose coordinates never moved digest differently and
read as a conflict.
The `SERVICE` and `FUNCTION` facilities, the implementation messages and
`RunnableTarget` are new. The target carries its own `schema`, so a later shape
is a new schema rather than a reinterpretation of these coordinates.

Binding validation requires every explicitly consumed runtime endpoint and at
least one mapped endpoint for a runtime dependency with an empty endpoint
selection. The installer must resolve that empty selection to all the service's
endpoints: the package alone has no service catalog with which to prove that
none were omitted. Legacy and external dependencies require mappings for their
explicitly selected endpoints; an endpointless legacy prerequisite or a
configuration-only external capability does not require an address. Build,
schema and completion prerequisites do not require network mappings at launch.
Each supplied mapping must have an endpoint and nonempty instance addresses.
Use one mapping per module/service/endpoint/API key and one instance per
access-kind/address key; duplicate keys are rejected even when other metadata
differs, so mapping and instance order cannot change the binding digest.
These checks validate resolved coordinates; installers and launchers still
own facility compatibility, reachability and the declared readiness predicates.

## Agent protocol

A runnable agent is an ordinary codefly agent of a uniform kind
(`codefly:runnable`, `Agent_RUNNABLE`): the language is the agent *name*
(`runnable-python`, `runnable-go`), so discovery, download and install resolve
through the same registration every other agent kind uses and no consumer
switches on language.

It is started and driven through the existing `Builder` service, extended in
two places and nowhere else:

- **`LoadRequest.runnable`** (`RunnableLocation`) is set instead of
  `LoadRequest.identity`, which names a service a runnable does not have. Which
  of the two is populated follows from the kind of agent the caller started.
  `RunnableLocation` pairs the release identity with `workspace_path` and
  `relative_to_workspace`. Those host paths are deliberately *not* fields of
  `RunnableIdentity`: the identity is part of the canonical form the package
  digest is taken over, so a checkout-dependent path would give one release a
  different digest on every machine. `Workspace.RunnableLocationOf` builds the
  pair and stamps the workspace name, the one part of the identity a runnable
  cannot know about itself.
- **`Builder.RunnableBuildInputs`** generates the harness into a caller-owned
  directory and returns the `RunnableBuild` for the loaded runnable: the
  handler and declared-input digests, the generated harness digest, the exact
  toolchain and the effective configuration digest. Only the agent can pin the
  harness it just generated and the toolchain it resolved, so it returns the
  whole message; the CLI assembles the `RunnablePackage` from the declaration,
  this build and the artifacts.

Artifacts need no new RPC. `Builder.Build` with an `output_directory` already
returns a `DockerBuildPlan` for the CLI to build (core#461), and
`Builder.Package` emits native artifacts with their digests and, in
`PackageArtifact.command`, how each one is launched. That command is the last
build fact only the agent holds: deriving it from the toolchain or from a
template name is exactly the language-specific shortcut a uniform agent kind
exists to avoid. With the build inputs and the command, the CLI has every field
a `RunnablePackage` and its `NATIVE` `RunnableArtifact` require. The `Runtime`
service is not extended: a runnable has no agent-run process, because the CLI
owns the launcher.

## Invocation framing

`codefly.runnable/v1` is the seam between a launcher and a generated harness.
The CLI is the only launcher core ships, but a harness in `runnable-python`
has to agree with it byte for byte, so the framing is frozen here rather than
guessed twice. `proto/codefly/base/v0/runnable_invocation.proto` carries it and
the `runnable` Go package implements the launcher's half.

The whole seam is three environment variables and two documents:

| Variable | Meaning |
| --- | --- |
| `CODEFLY__RUNNABLE_PROTOCOL` | the protocol name, so a harness refuses a launcher it does not implement |
| `CODEFLY__RUNNABLE_INVOCATION` | absolute path of the `RunnableInvocation` the launcher wrote before starting the process |
| `CODEFLY__RUNNABLE_RESULT` | absolute path the harness writes its `RunnableResult` to |

Both documents are proto3 JSON spelled with the proto field names, so a
harness generating bindings from the same sources needs no hand-written
spelling of the framing. The harness writes its result to a temporary file in
the result path's directory and renames it onto the result path: a launcher
never reads a half-written document, and nothing at that path when the process
ends means no result at all. `runnable.InvocationEnvironment` builds the three
variables and `runnable.EncodeInvocation` the document, so a launcher does not
restate either.

One process runs one invocation: the paths name a single invocation and a
single result, and there is no way to hand a second invocation to a process
already running. `execution.concurrency` therefore bounds how many such
processes a facility runs at once, not a pool inside one of them.

**Identity and deadline.** An invocation carries the release it invokes, an
`invocation_id` for this one process, an `intent_id` stable across attempts of
the caller's logical operation, and the `effect_id` an uncertain outcome is
resolved by. A `recovery: receipt` package requires that effect identity; a
`recompute` one may still carry it, because a caller with a single identity
scheme for all of its work should not have to branch on the target package's
recovery policy before filling a field, and nothing looks it up there.
`issued_at` and `deadline` travel together so a harness budgeting its own work
measures the remaining time as `deadline - issued_at` from the moment it reads
the document, unaffected by an offset between the two clocks. That budget may
not exceed the package's declared `timeout`, which bounds one invocation's
duration: a launcher computing a deadline of its own may shorten it, never
overrule the author.

**Payload versus logs.** The input and output payloads are each one UTF-8 JSON
object, bounded by `max_input_bytes` and `max_output_bytes`. They are carried
as bytes rather than as a structured value because proto3 JSON maps every
number to a double while the bounded profile has a 64-bit integer, and because
the bound must apply to exactly the document that was bounded; a proto3 JSON
encoder base64-encodes them, and the bound is on the decoded document. Standard
output and standard error are logs and never carry completion data — a harness
that printed its output would be indistinguishable from a library that printed
a warning. `max_log_bytes` bounds each captured stream; exceeding it truncates
and never changes the outcome, while an output payload over its bound makes the
result invalid. Core frames and bounds the payloads and proves they are objects;
the harness type-checks them against the bindings generated from the contract.

**Outcomes.** A harness reports only what it observed of itself: `SUCCEEDED`
with output, `FAILED` with a typed handler error in the operation's own
vocabulary, or `INTERRUPTED` when it handled a signal and stopped. A package
declaring `cancellation: signal` promises exactly that third report, and
without a status for it such a harness would have to claim a failure it did
not have — which a caller reads as proof the effect did not happen. `FAILED`
carries that weight too: it says the handler completed without its effect, so
a handler abandoning a half-applied one owes an `INTERRUPTED` or a crash.

Every other way an invocation ends is the launcher's judgement about a process
that left no result, and `runnable.Complete` makes it, so a timeout, a crash
and a harness that never wrote its result mean the same thing everywhere:

| Outcome | What the launcher saw |
| --- | --- |
| `SUCCEEDED` | a valid result reporting output |
| `FAILED` | a valid result reporting a typed handler failure — the operation ran |
| `INVALID_OUTPUT` | a result document that is malformed, another invocation's, missing its payload or over the output bound, including from a process that exited zero |
| `MISSING_OUTPUT` | a process that exited successfully without writing a result |
| `CRASHED` | a non-zero exit or a signal, with no result |
| `TIMED_OUT` | the deadline passed and the launcher ended the process |
| `CANCELED` | the harness reported `INTERRUPTED`, or the launcher interrupted the process at the caller's request |

The precedence is: a valid result for this invocation first, so an outcome the
harness already proved is never discarded because the launcher also ended the
process; then the launcher ending it, which explains the process better than
the exit status its own kill produced; then an invalid document; then a
non-zero exit or signal; then nothing at all. Only a package declaring
`cancellation: signal` may be interrupted by a launcher; a launcher that
interrupts one declaring `none` has broken the contract, and the completion
records that in its `message` rather than being withheld — the invocation
ended, and discarding the record of how would throw away an effect's only
evidence. `SUCCEEDED` and `FAILED` prove what happened to the effect;
`runnable.OutcomeIsCertain` says so, and every other outcome — `CANCELED`
included, however the interruption was reported — leaves it unproven for the
package's `recovery` policy to resolve. Core neither retries nor recovers.

## Ownership of what is not here

- **Agents** (`Builder.Build` with an `output_directory` → `DockerBuildPlan`;
  `Builder.Package` → native artifact) emit recipes and packages. The CLI
  builds and publishes images (core#461) and assembles the descriptor; no
  agent builds an image.
- **CLI** owns the launcher: it executes the bound artifact's command with the
  resolved configuration and the framing above, captures the streams and reads
  the result document. The rules it applies to them — what a valid result is,
  and which outcome each way of ending maps to — are core's, so the CLI
  implements the process, not the contract.
- **Orchestration** owns registration, activation, tasks, attempts and
  receipts, translating the binding into its own compute contract.
