# Runnable (experimental)

A **Runnable** is a packaged implementation of a typed finite operation: typed
input in, typed output out, declared dependencies, requirements and effect
semantics. It is declared in `runnable.codefly.yaml` and owned by a module next
to the module's services and jobs, so one module release can ship both a
service surface and runnable work.

This document covers what core owns. Language agents (`runnable-python`,
`runnable-go`), CLI commands and Orchestration's execution adapters live in
their own repositories and consume the contracts described here.

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
  protocol: codefly.runnable.served/v1
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
entrypoint:                    # only where a build produces an artifact; see below
  handler: handler.py          # the author entrypoint
  inputs: [pyproject.toml, uv.lock]   # everything else whose content changes the package
execution:
  facilities: [generated-service, kubernetes]   # generated-service | kubernetes | service | function
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
key can be written. `codefly.runnable.served/v1`, `codefly.runnable.service/v1`
and `codefly.runnable.function/v1` are the protocols accepted, and which one a
release may name follows from its facilities.

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
the operation: built artifacts (an `ARCHIVE` with the command that starts its
harness and/or a digest-pinned `IMAGE`, each with its platform), owner service
methods and remote functions. Its `digest` is the sha256 of a canonical form core owns
(proto3 JSON with sorted keys, prefixed by a digest-format identifier) — not
of the wire encoding, which protobuf only keeps stable within one binary. A
message carrying fields outside the schema it claims is rejected rather than
hashed with them ignored. Every unordered set (facilities, inputs, artifacts,
dependencies, endpoints, configurations) is sorted first, so equal content in
another order is the same release. A resolved execution-plan fingerprint
(`docs/design/execution-plan.md`) identifies a plan, not bytes; the package
digest is what proves two builds are the same executable.

`runnable.CanonicalJSON(message)` returns that form — `protojson` with
`UseProtoNames`, object keys sorted, whitespace removed — and `digestOf` is its
only other caller, so a consumer that must reproduce these bytes never
marshals a second time on its own. `codefly generate runnables` writes them as
`runnable-package.json` and its `--check` mode diffs the committed file against
a freshly derived one, which `protojson` alone cannot do: it deliberately
varies its whitespace. That makes the **byte form**, not only the digest taken
over it, a contract across every consuming repo. `runnable/testdata/canonical`
pins it: changing the normalization turns every committed file into spurious
`--check` drift and every unchanged re-registration into `ErrConflict`, so it
is a versioned decision reviewed here, never an incidental one.

Three properties of that form are load-bearing, because the repos reproducing
it are not all Go. `<`, `>` and `&` are emitted as themselves: Go's
`encoding/json` escapes them to `\uXXXX` by default and nothing else does, so
the escaping is off and `executionplan`'s canonical encoding makes the same
choice. The call normalizes the *encoding*, never the message — the unordered
sets are sorted by `PreparePackage` and `PrepareBinding`, and canonicalizing a
descriptor that skipped those yields stable bytes of an uncanonical descriptor,
which is reproducible and still not what core digests. And the form inherits
`protojson`'s value rendering, where a `Duration` is `"120s"`, an `int64` is a
quoted string and an enum is its name; a `google.golang.org/protobuf` upgrade
that changed any of those would move every digest, which is why the golden
fixture pins bytes rather than only the digest taken over them.

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
| `generated-service` | an `ARCHIVE` artifact: the generated harness, unpacked and run as a service | `codefly.runnable.served/v1` | `generated`: the directory the package was unpacked into, absolute as the artifact's own platform spells it, and the resolved address the harness serves on |
| `kubernetes` | an `IMAGE` artifact, run as a finite invocation Job | `codefly.runnable.served/v1` | `cluster`: kubeconfig context, namespace, optional service account |
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
language Runnable builder. A built artifact still requires a `codefly:runnable`
agent, and service builders are not accepted for functions.

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
facilities. `codefly.runnable.served/v1` reaches the harness a runnable agent
generated, and it is the wrong answer for a method reached over the owner's
endpoint or a function reached over the provider's transport, which is why each
of those has its own protocol. Facilities whose protocols differ cannot share
one release: a contract cannot name two transports, and a consumer choosing a
transport from the protocol would have nothing to choose.

`generated-service` and `kubernetes` share `codefly.runnable.served/v1`, so one
release covers both — and that sharing is the point rather than a convenience.
They are the same generated harness, reached over the same transport, carrying
the same identity. A runnable someone runs on their own machine is a service
they run locally, called exactly the way the invocation Job is called, so local
development exercises the production path instead of a second one. That is what
replaced the native process placement, which had a framing of its own that
nothing else used.

`entrypoint` is a built-artifact fact. A method an owner already publishes
is built by that owner's service agent, so there is no author entrypoint in
the runnable directory and none may be declared; requiring one would make the
author name a file that nothing reads and no build ever digests, which is the
same fabrication the missing `build` avoids on the package.

`logs.max-bytes` bounds what is captured from the streams of an artifact this
build produced. Where no declared facility runs one nothing captures anything,
so declaring a bound there is rejected and the wire form carries none, rather
than stating a number that describes nobody's behavior.

`cancellation: signal` promises a harness that reports `INTERRUPTED` when it is
signalled, which needs something that owns the execution and can signal it —
the process a `generated-service` runs, or the container a `kubernetes`
invocation Job runs. It is refused for `service` and `function`. The refusal is deliberately whole-declaration rather
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
operates has no install root of its own, so it cannot be written as a
`generated-service` binding.

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
same reason: both are facts of a facility that runs an artifact this build
produced, and the Go validation now requires each exactly where its form
provides it. A binding also no longer
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
These checks validate resolved coordinates; installers still own facility
compatibility, reachability and the declared readiness predicates.

## Operations derived from a service method

How a prepared binding of a derived operation reaches the caller that installs
it — what the prepared value carries, and how any large configuration value is
delivered by file — is in [Runnable binding delivery](runnable-binding-delivery.md).
An owner is called with JSON: a gRPC owner on its Connect endpoint, a REST owner
on its real route, so no protobuf descriptor is delivered to a caller.

A unary, idempotent operation an owner service already publishes becomes a
Runnable by derivation, not by authoring. The owner marks it and the contract
follows from the payloads the owner already describes. Nothing is written
twice, so the published message and the bounded contract cannot drift apart.

There are two ways an owner describes an operation, and one derivation:

| | gRPC | REST |
| --- | --- | --- |
| Marking | the `codefly.runnable.v0.operation` method option (`proto/codefly/runnable/v0/options.proto`, a `google.protobuf.MethodOptions` extension) | the `x-codefly-operation` vendor extension on the operation object, the proto3 JSON form of that same `Operation` message |
| Source of the contract | the method's request and response descriptors | the operation's `application/json` request body and its one `2xx` response |
| Builder | `runnable.PackageFromMethod(files, location, owner, "/pkg.Service/Method")` | `runnable.PackageFromOpenAPIOperation(document, location, owner, "POST", "/path")` |
| Projection | `runnable.ProjectMessage(md)` | `runnable.ProjectJSONSchema(doc, schema)` |
| `operation` recorded | `/pkg.Service/Method` | `POST /path` |
| `input_message` / `output_message` | the messages' full names | the payload components' names |
| `retryable_codes` | `google.rpc.Code` names, such as `UNAVAILABLE` | HTTP statuses as decimal strings, such as `"503"` |

Both land in the same place, which is the point: one profile, one digest and
one drift rule, so a FastAPI service and a gRPC one publish the same kind of
operation rather than two kinds that resemble each other.

Either builder returns the `RunnablePackage` and, beside it, the
`OperationSpec`. The package is the contract: facility `SERVICE`, protocol
`codefly.runnable.service/v1`, `recovery: receipt`, `cancellation: none`, the
declared `total_timeout` as its execution timeout, the default inline payload
bounds, one `RunnableServiceOperation` naming the owner's module, service and
endpoint with `ADAPTATION_BOUNDED_JSON_V1`, and the owner's own
`codefly:service` agent — the exception a `SERVICE`-only release already makes.
It is finished by `PreparePackage`, so its digest is the canonical one. The
endpoint and the operation spelling differ between the two, so one operation
published over both transports is two releases of one contract.

The option's execution policy and its Work Context authority are *not* in the
package. Policy is installed with the binding, so two installations of one
contract may differ in both, and a digest that moved when an attempt budget did
would make re-registering an unchanged contract read as a conflict. Core
validates them and hands them back: the method must be unary (a streaming
method is rejected by name); `attempt_timeout` is 1s–1m, `total_timeout` at
least one attempt, `max_attempts` 1–5, `backoff` 100ms–1m and at most 16
`retryable_codes`, each named once and a code of the transport's own
vocabulary; `audience` is required, and both concrete scope sets are required at
installation. An owner declaration may defer scopes to its
[required scope slots](#required-scope-slots); supplied scopes carry the runtime's
own value bounds. An operation with no
audience and no scopes is not a lenient one: there is nothing to mint a child
capability from, and the runtime refuses the installation rather than calling
without authority. `lookup_scopes` must be a subset of `invoke_scopes` under
the Work Context attenuation rule and read-only (exactly `read`): recovering an
outcome never carries more authority than producing it did.

`lookup_method` is optional — empty leaves the answer to the SDK's generic
receipt lookup — but when set it must name a unary method the *same* service
publishes, and it may not name the operation itself. A package declaring
`recovery: receipt` says an uncertain outcome is resolved by reading the
receipt and never by re-running, so an operation that is its own lookup would
make recovery repeat the effect it exists to avoid repeating. Naming a method
is the whole of what the field can say, which is a gRPC spelling: an
`x-codefly-operation` declaring one is rejected rather than accepted as a route
nothing validates, and a REST operation's receipts are read through the
receipts route the SDK publishes.

The marking's presence is the marking; its policy fields are required, and an
empty option or marker is rejected rather than filled in with an attempt budget
nobody chose.

### The projection

`runnable.ProjectMessage(md)` and `runnable.ProjectJSONSchema(doc, schema)` are
the two readers of one profile. They are written as one table so they cannot
drift, and held to it by a fixture pair: a method's messages and the OpenAPI
document transcoded from them project to **equal** `RunnableSchema`s.

| Protobuf | JSON Schema | Bounded profile | Note |
|---|---|---|---|
| `string` | `type: string`, `format` absent or outside the rejected list below | `STRING` | `uuid`, `email`, `uri` annotate a string |
| `int32`, `sint32`, `sfixed32`, `int64`, `sint64`, `sfixed64`, `uint32`, `fixed32` | `type: integer`, `format` absent, `int32` or `int64` | `INTEGER` | int64 range |
| `uint64`, `fixed64` | `type: integer` of any other `format` | **rejected** | exceeds int64 |
| `bool` | `type: boolean` | `BOOLEAN` | |
| message | `type: object` with `properties` and `additionalProperties: false` | `OBJECT` with `fields` | depth ≤ 32 and ≤ 10 000 fields in total; recursion (a message or a `$ref` reaching itself) rejected |
| `repeated T` | `type: array` with a single `items` schema | `ARRAY` with `items` | |
| proto3 `optional` / message-typed field | a name absent from `required` | `optional: true` | absent key |
| — | `nullable: true`, or `type: [T, "null"]` | `nullable: true` | protobuf has no null value |
| `enum`, `map<>`, `oneof`, `bytes`, `float`, `double`, `google.protobuf.*` well-known types, `Any` | `enum`, `oneOf` / `anyOf` / `allOf`, `type: number`, `additionalProperties` other than `false` (maps), free-form objects, `type: object` without `properties`, tuple `items`, `format: byte` / `binary` / `date-time` / `date` | **rejected** | named by full field path or by JSON pointer in the error; never coerced |

Rejection is a generation failure, not a runtime one: an out-of-profile payload
is fixed once in the `.proto` or the document and stays fixed, whereas coercing
one would put a representation on the wire that neither the owner nor the
runtime agreed to. An array's items carry neither the field's name nor its
optionality — an element is present or the list is shorter.

The well-known-type rule covers the payload itself and not only its fields: a
method taking or returning `google.protobuf.Timestamp` would otherwise derive a
contract of `{seconds, nanos}`, and one returning `google.protobuf.Empty` a
contract with no keys at all. `optional` reads the field's presence, except that
a *required* field (proto2, or editions `LEGACY_REQUIRED`) keeps its key: it
tracks presence, but it is never absent. The rejected string formats are what
the rejected protobuf types transcode to — `bytes` becomes `byte` or `binary`,
`Timestamp` becomes `date-time` — which is why the two columns reject the same
payloads.

Depth is not the only bound, because it is not the one that binds. A payload
whose members are themselves objects expands *multiplicatively*: at twelve
levels of fanout three, under three kilobytes of source describes forty
megabytes of schema, and sixteen levels exhausts memory before it finishes —
while `MaxProjectionDepth` would allow twice that nesting. `MaxProjectionFields`
bounds the projection at 10 000 fields in total, in both readers, because there
is one profile. A `required` name the object does not declare is rejected for
the same reason a type outside the profile is: the object closes
`additionalProperties`, so the declaration is unsatisfiable, and projecting it
anyway would hand back a contract silent about a key the owner marked mandatory.

Reading the columns together says what a transcription has to spell, and the
fixture pair is what enforces it. A proto3 field with implicit presence has a
key that is always there, so it is `required` in the document; a message-typed
or `optional` field has one that may be absent, so it is not. An `int64` is an
`integer` of format `int64` and never the string proto3 JSON would carry it as:
the bounded profile has a 64-bit integer of its own and does not borrow that
encoding. An object's members are projected in the order the source spells
them, because the profile's `fields` are an ordered list the package digest is
taken over.

### The REST reader

`runnable.PackageFromOpenAPIOperation(document, location, owner, method, path)`
reads the OpenAPI JSON an owner already publishes (`codefly generate
contracts` writes one per HTTP endpoint) and derives the same package. An
operation is eligible only if it is a `POST` or a `PUT`, carries no parameter
outside `header` and `cookie`, has one `application/json` request body and
exactly one `2xx` response with an `application/json` schema. Anything else is a
refusal that names why, never a coercion: v1 keeps an operation's input as one
JSON object, and folding a parameter into it is a later profile decision.

The parameter locations are an **allow list**, not a pair of denied ones. The
transport carries a header and a cookie, so they are no part of the payload;
every other location names an input the contract would have to describe.
Denying only `path` and `query` would let `body` and `formData` — which is how
Swagger 2.0 spells a request body — pass in silence, and the derived contract
would simply omit the operation's input.

The document's version is checked rather than assumed, for the same reason.
Swagger 2.0 puts a request body in an `in: body` parameter and a response schema
on the response itself, so reading one as OpenAPI 3 finds no `requestBody` and
reports exactly that — pointing at the wrong part of a document that plainly
declares one. Core's own `OpenAPICombinator` still writes 2.0, so this is a
document that turns up; it is refused as Swagger, by name.

Both payload schemas must be `#/components/schemas/…` references. That is where
`input_message` and `output_message` come from, and their job is to record the
owner's published message identity so the reuse is auditable rather than
implied by a matching shape — which an inline schema has nothing to record. A
`$ref` is followed there and nowhere else: core reads the operation object as
the document writes it, so a `$ref`'d path item, request body, response or
parameter is refused rather than resolved.

`x-codefly-operation` is the proto3 JSON form of `codefly.runnable.v0.Operation`
— the same field names in either spelling, durations as strings such as `"30s"`,
scopes as `WorkScopeV1` — decoded into that message rather than into a second
schema, and held to the same bounds. A field the message does not declare is an
error, so a misspelled policy field is never a policy silently left at zero. The
one difference is the vocabulary a retry names: a REST operation names its
retryable outcomes by HTTP status, spelled as the decimal number in a string.
Each vocabulary is as closed as its own protocol makes it — `google.rpc.Code` is
an enumeration, so a name outside it names nothing, while HTTP defines a status
as any three-digit code in a class it defines, and a proxy in front of the owner
does answer with unregistered ones.

Core owns the markings, the projections and the builders. The generator that
walks a service and writes the results out is the CLI's (`codefly generate
runnables`), reading descriptors and OpenAPI as its two sources; the generic
`SERVICE` invoker, the HTTP transport and the receipt lookup belong to the
orchestration runtime and the SDKs.

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
`Builder.Package` emits archives with their digests and, in
`PackageArtifact.command`, the command that starts each one's harness. That
command is the last build fact only the agent holds: deriving it from the
toolchain or from a template name is exactly the language-specific shortcut a
uniform agent kind exists to avoid, and it is why the command stayed on the
artifact when the launcher that used to run it went away. With the build inputs
and the command, the CLI has every field a `RunnablePackage` and its `ARCHIVE`
`RunnableArtifact` require.

## How one call is made

Every facility is reached by **calling** something that serves the contract, so
there is one shape for all of them: this endpoint, this method, this identity,
this input, this policy. The four facts fixed when the operation is installed
live in the binding; identity, input, the effect identity and the deadline
belong to one call. `proto/codefly/base/v0/runnable_invocation.proto` carries
the per-call facts and the answer, and the `runnable` Go package implements the
caller's half.

`codefly.runnable.served/v1` is the seam between a caller and the harness a
runnable agent generated. A harness in `runnable-python` and one in
`runnable-go` have to agree with the caller byte for byte, so it is frozen here
rather than guessed three times.

**The call.** `POST <address><route>` with `content-type: application/json`.
The body is the bounded input object itself, with no Codefly envelope around it
— a field Codefly added to an owner's body would be a field the owner never
described. The per-call facts that are not the body travel as headers, pinned
in `runnable/served.go` so a caller and an implementation cannot disagree
invisibly:

| Header | Meaning |
| --- | --- |
| `Codefly-Work-Context` (`X-Codefly-Work-Context`) | the Work Context minted for this call, carried verbatim |
| `Codefly-Runnable-Effect-Id` | the effect identity an owner keys its receipt by |
| `Codefly-Runnable-Deadline` | when the caller stops waiting, RFC 3339 with nanoseconds, in UTC |
| `Codefly-Runnable-Failure-Code` | on the answer: the owner's own typed failure code |

There is deliberately **no invocation-id and no intent-id header**. Both are the
caller's own correlation identity and nothing on the receiving side does
anything with them, so a header for either would be one more spelling two sides
could disagree on for no gain.

One call carries one invocation, and there is no way to hand a second to a
call already in flight. `execution.concurrency` therefore bounds how many calls
a facility serves at once, not a pool inside one of them.

**Identity and deadline.** An invocation carries the release it invokes, an
`invocation_id` for this one call, an `intent_id` stable across attempts of the
caller's logical operation, and the `effect_id` an uncertain outcome is resolved
by. A `recovery: receipt` package requires that effect identity; a `recompute`
one may still carry it, because a caller with a single identity scheme for all
of its work should not have to branch on the target package's recovery policy
before filling a field, and nothing looks it up there. `issued_at` and
`deadline` travel together so an implementation budgeting its own work measures
the remaining time as `deadline - issued_at`, unaffected by an offset between
the two clocks. That budget may not exceed the package's declared `timeout`,
which bounds one invocation's duration: a caller computing a deadline of its own
may shorten it, never overrule the author.

The identity itself is required, in every facility, and it is a oneof: a
`work_context` for a call happening now, or a `grant` reference for work that
starts later. Which arm is set follows from *when* the invocation runs, not from
which facility runs it — a child capability projected at submit time is already
expired by the time a job container starts or a provider retries it.

**Payload versus logs.** The input and output payloads are each one UTF-8 JSON
object, bounded by `max_input_bytes` and `max_output_bytes`. They are carried
as bytes rather than as a structured value because proto3 JSON maps every
number to a double while the bounded profile has a 64-bit integer, and because
the bound must apply to exactly the document that was sent; a proto3 JSON
encoder base64-encodes them, and the bound is on the decoded document. Standard
output and standard error are logs and never carry completion data — an
implementation that printed its output would be indistinguishable from a library
that printed a warning. `max_log_bytes` bounds each captured stream; exceeding
it truncates and never changes the outcome, while an output payload over its
bound makes the result invalid. Core frames and bounds the payloads and proves
they are objects; the implementation type-checks them against the bindings
generated from the contract.

**What an implementation may report.** Only what it observed of itself:
`SUCCEEDED` with output, `FAILED` with a typed handler error in the operation's
own vocabulary, or `INTERRUPTED` when it handled a signal and stopped. A package
declaring `cancellation: signal` promises exactly that third report, and without
a status for it such an implementation would have to claim a failure it did not
have — which a caller reads as proof the effect did not happen. `FAILED` carries
that weight too: it says the handler completed without its effect, so a handler
abandoning a half-applied one owes an `INTERRUPTED` or a crash.

Every other way a call ends is the caller's judgement, and the next section is
where it is made.

> **The launcher framing is gone.** `codefly.runnable/v1` framed a process a
> launcher started, around three `CODEFLY__RUNNABLE_*` environment variables, an
> invocation document and a result file, with an outcome taxonomy built on exit
> statuses and signals. The native placement it existed for was removed, and a
> job container running the served harness is a finite process per attempt
> without any of it. A package naming that protocol is refused by name rather
> than half-read as a call. Its one load-bearing part — the line between a
> proven and an unproven effect — was written in served terms *before* the
> deletion, not after, and is below.

## What a served invocation's outcome proves

What is classified is a call, not a process. The taxonomy this replaced was
built around one — a non-zero exit, a signal, a harness that never wrote a
result — and none of that vocabulary means anything for a call, while the line
it drew is the one recovery depends on. `runnable.ClassifyServed` makes the
judgement and `runnable.ServedOutcomeIsCertain` draws the line.

| Outcome | What the caller saw | Effect |
| --- | --- | --- |
| `SERVED_SUCCEEDED` | a response that validates against the installed output schema | **proven committed** |
| `SERVED_OWNER_FAILED` | the owner's own assertion, in the protocol's own vocabulary, that it completed without its effect | **proven not committed** |
| `SERVED_AUTHORITY_UNAVAILABLE` | no authority was resolved, so nothing was sent | **proven not committed** |
| `SERVED_INVOCATION_INTEGRITY` | a response that is malformed, another invocation's, or fails the output schema | unproven |
| `SERVED_OWNER_UNAVAILABLE` | the call did not complete, or completed with a status carrying no typed code | unproven |
| `SERVED_EFFECT_OUTCOME_UNKNOWN` | the owner reported an interrupted invocation | unproven |
| a receipt read answering *absent* | nothing committed under this effect id **so far** | unproven |

Two of these carry the weight the process taxonomy carried, and they are the two
a caller must never confuse.

**Only the owner's own typed assertion proves a no-effect failure.** A bare error
status does not, however plausible it looks. A `4xx` with no typed code, a dropped
connection and a timeout are all unproven — the side on which the operation
retries under its attempt budget or ends unknown, never the side on which a
second attempt is issued against an effect that may already have committed. The
code must be a value in the protocol's own vocabulary, never inferred from a
status class, which is why `Call.FailureCode` is what the classifier reads and
not the status.

**Absent is not "no", it is "not yet known".** A receipt read that finds nothing
is the answer recovery exists to get, and it must be reachable without an error.
A caller reads it as inconclusive and never as permission to invoke again. An
owner serving no receipt route answers with neither, which is also inconclusive,
so an owner cannot make its effects look absent by declining to report them.

There is deliberately **no outcome for a container that exits without
answering**. Served, that is not a distinct case: it is a call that did not
complete, so it is `SERVED_OWNER_UNAVAILABLE` and the receipt is the only
evidence — exactly what it would have been had the container answered and the
reply been lost. Collapsing the two is the point rather than a gap: a caller
never had a way to tell them apart, and separate outcomes implied a certainty it
did not have.

## Submit mode: the four shapes a later answer arrives in

`COMPLETION_CALL` answers in the reply and everything above is the whole of it.
`COMPLETION_SUBMIT` is for work whose effect takes longer than a call: the reply
proves the work was **accepted** and nothing about the effect, and the answer
arrives later. That one fact is where the rest follows from, and it is why the
mode requires `recovery: receipt` — every terminal answer it could give is one
the caller may fail to receive, and recomputing cannot tell a lost answer from
work that never happened.

The call itself does not change. Same address, same route, same four headers,
the bounded input as the whole body. `proto/codefly/runnable/v0/submit.proto`
carries the four shapes; `runnable/submit.go` is the caller's half of them.

**The acceptance is the reply**, `RunnableAcceptance` as proto3 JSON:

| Field | Meaning |
| --- | --- |
| `schema` | `codefly.runnable-acceptance/v1` |
| `invocation_id` | the invocation this accepts, so a reply on a reused connection is matched rather than assumed |
| `handle` | what the owner minted to name the accepted work; everything the caller later says about it is keyed by this |
| `accepted_at` | when the owner took responsibility |
| `heartbeat_interval` | how often liveness is reported, or read |

The handle is the owner's and opaque to the caller. A caller that derived it
from the invocation could ask about work the owner never accepted and get an
absence it could not tell from a lost acceptance.

**Which shape a reply has follows from the declared completion mode**, fixed in
the binding's policy when the operation was installed — never sniffed from the
body, never read off a status. An owner whose contract happened to declare a
`handle` field would otherwise have its own output document read as an
acceptance. The acceptance answers `202`, which is for humans and proxies:
`runnable.ClassifySubmit` never reads it, for the same reason `ClassifyServed`
never reads a status class.

**The reporting address travels as two headers**, and is recorded on the
invocation as `RunnableCallbackTarget`:

| Header | Meaning |
| --- | --- |
| `Codefly-Runnable-Callback` | the absolute URL the owner POSTs its reports to |
| `Codefly-Runnable-Callback-Audience` | the trust boundary the owner mints its own capability for when it reports |

Both are optional and they are the **caller's** choice, not the owner's: with
them, the owner pushes; without them, the caller reads the status itself. An
owner never decides which. The audience is stated rather than derived from the
URL host, because an audience is a trust boundary and a host is a route to one
— deriving it would make a DNS name decide what a capability is minted for. A
call-mode invocation carrying either is refused at `PrepareInvocation`: it would
describe a report nothing ever sends. Plaintext is admitted only to a loopback
host; anything else would put a completion, and the Work Context authenticating
it, on the wire in clear.

**Heartbeats are pushed, and the direction is not negotiated.** It follows from
the one fact the invocation already carries. With a callback address the owner
POSTs `RunnableHeartbeat` there at `heartbeat_interval` and
`RunnableCompletionCallback` when the work ends; with none it reports nothing
and the caller reads `RunnableStatus` at that interval instead. Both documents
arrive on that one URL, so `schema` is the discriminator **there** — the single
place in this contract where a document's shape is read off the document rather
than off the declaration.

An owner serving submit mode serves the status route
(`/codefly.runnable.v0.Runnable/Status`, a `RunnableStatusRequest` naming the
handle) whether or not a callback address was presented. That is not a second
mechanism: a callback can always be lost, so the read is the recovery path the
owner owed anyway, and a caller that presented no address is using the recovery
path as its only one.

Silence is never a failure. A heartbeat that stopped, a callback that never
arrived and a status that says `STATE_LOST` are all **unproven** — a worker
whose heartbeats stopped may have committed its effect in the same instant — and
the effect receipt is the only evidence. `STATE_LOST` is reported rather than
withheld, because a caller told "lost" reads the receipt while a caller told
nothing cannot tell that owner from one that never accepted the work.

**The terminal answer is classified exactly as a reply is.** A
`RunnableCompletionCallback` and a `STATE_COMPLETED` status both carry the
harness's own `RunnableResult`, and `SUCCEEDED`, `FAILED` and `INTERRUPTED` map
to the same three served outcomes they map to in a reply. The route differs;
what the answer proves does not. The completion is dated by the owner's own
`completed_at`, because a caller's clock records when it heard.

**Identity is the one part that is not new.** There is still exactly one carrier
on the wire — the Work Context header — and `RunnableInvocationIdentity` says
where that capability comes from: `work_context` when whoever holds the
invocation is calling now, `grant` when the invocation is handed to something
that will call later. A container that starts an hour after the work was
admitted is started with the **grant** arm in its invocation document, and
exchanges it for a short child at the instant it calls. Nothing long-lived is
minted, and a child projected at submit time would already have expired. The
report is a call too: the owner authenticates it with its own workload identity,
minted for `callback.audience`. There is deliberately no signature, token or
shared-secret field in any of these messages — a shared key would have to reach
every owner that may report, which makes each able to forge every other's
completions.

**The deadline means the acceptance.** On a submit-mode invocation the deadline
header is when the caller stops waiting for the *acceptance*, not for the work:
the work's own bound is the package's declared `timeout`, measured from
`accepted_at`. An owner that cannot accept within the deadline declines rather
than accepting work whose acceptance nobody is still waiting for.

**What is not here yet.** The generated harnesses serve call mode
(runnable-go#20, runnable-python). Nothing in this section is implemented by a
harness; it is the contract an owner serving submit mode meets and the caller's
half core implements, which is what the runtime builds its own half against.

## Ownership of what is not here

- **Agents** (`Builder.Build` with an `output_directory` → `DockerBuildPlan`;
  `Builder.Package` → archive) emit recipes and packages, and generate the
  harness that serves the contract. The CLI builds and publishes images
  (core#461) and assembles the descriptor; no agent builds an image.
- **CLI** runs a `generated-service`: it starts the bound artifact's command
  with the resolved configuration, and calls it the way everything else does.
  The rules applied to the answer — what a valid result is, and which outcome
  each way of ending maps to — are core's, so the CLI implements the process,
  not the contract.
- **Orchestration** owns registration, activation, tasks, attempts and
  receipts, translating the binding into its own compute contract.

### Explicit tool exposure

A Runnable is not automatically a discoverable tool. An owner opts in with
`Operation.tool` in the method option or the same `tool` object in the
`x-codefly-operation` OpenAPI marker. It declares a stable `name`, bounded
`description`, and an explicit `effect`: `EFFECT_READ_ONLY` or
`EFFECT_MUTATION`. Absence keeps the operation hidden; an empty exposure or an
unspecified effect is invalid. HTTP verbs, scope names, receipt support and
idempotence never classify a mutation as read-only.

The input/output schema remains the operation's derived `RunnableContract`.
`OperationSpec.Tool` retains exposure during derivation, and a prepared binding
carries it in `policy.tool`. A delivery writer does not copy policy fields by
hand: `OperationSpec.Policy` is the one conversion from a derived spec to the
`Operation` a binding delivers, and `TestPolicyRoundTripsEveryField` holds it
and the read-back side to the schema's field list, so a field a hand-copying
writer would drop — exposure arrived as exactly such a field — cannot be added
to the schema without the conversion learning it. `ToolFromPrepared` validates
the complete binding (including its contract digest) and returns a detached
exposure with no route, credential or authority scopes. A valid unexposed
operation returns `ErrNotATool`.

Core validates the declaration, not the publisher's identity or the viewer's
permissions. The host must authenticate the declaration source and project only
currently permitted installations/operations. Admission must reject duplicate
tool names and freeze the exact binding, contract and exposure; the contract
digest alone covers schemas, **not** policy or tool metadata. Every invocation
and receipt lookup still needs current, narrowly delegated caller authority.
A read-only declaration is an owner assertion, never an authorization grant.

### Tools over a parameterised operation

One owner method can serve many installed declarations, each discoverable as a
tool with its own name, schema and effect. Discovery projects those tools over
**one prepared binding**. Core does not derive a package per declaration:
`PackageFromMethod` still reads the method descriptors and release identity at
generation time. Installation-selected declarations do not change that identity.

[`ToolProjection`](../proto/codefly/runnable/v0/projection.proto) is the shared
delivery shape. Its fields are:

```text
{
  binding_id, contract_digest,
  tools: [{name, description, effect, input_schema, output_schema,
           selector: {field, value}, digest}]
}
```

Complete examples are shipped in both
[snake_case](../runnable/testdata/tool-projection/snake_case.json) and
[lowerCamelCase](../runnable/testdata/tool-projection/lowerCamelCase.json).

`binding_id` is an opaque installation reference. The admitting consumer resolves
it to the exact prepared binding; `PreparedBinding` does not itself carry that
reference, so matching a contract digest cannot prove the reference was resolved
correctly. `contract_digest` must equal that binding's digest and covers only its
bounded method contract. It does not cover policy, route or projected tools.
Each tool's `digest` identifies the authenticated publisher's complete declaration,
including implementation details outside discovery. The publisher owns that
declaration format and its digest computation. Core can check the digest's
`sha256:<lowercase hex>` spelling, but cannot recompute content it does not have.
Admission freezes and compares **the entire projection and exact binding**,
including every declaration digest; equal contract digests, or equal asserted
declaration digests with changed metadata, are not sufficient.

`ValidateToolProjection` enforces `ToolExposure`'s name, description and explicit
effect rules, unique names within the projection, required digests and selectors,
and bounded self-contained JSON Schemas. There are 1..256 tools in at most 1 MiB
of canonical JSON; each schema is at most 64 KiB. Input declares root `type:
"object"`; output may describe any JSON type. Schema validation defaults to JSON
Schema draft 2020-12 and loads no external references, including local files.
These schemas describe the declared payload and unwrapped result, not the generic
method envelope. They neither replace nor widen the bounded Runnable profile.

Tool names are the uniqueness key. Distinct names may share a selector, a
declaration digest, or both: aliases are permitted. Admission decides which
views to expose and authenticates each complete declaration, including its
effect; equal selectors or asserted digests do not establish that two tools
are interchangeable.

`selector.field` names one top-level string field in the prepared input contract,
in that contract's spelling. `selector.value` fixes its value, independently of
the discovery name. The adapter validates model input against `input_schema`,
wraps it in the method envelope, applies the fixed selector and admitted
installation context, and validates the unwrapped output against `output_schema`.
It must refuse model attempts to supply or override binding identities, selectors,
destinations or authority. Core does not infer this adapter from field names and
does not dispatch a projection. For a method carrying a JSON payload in a string,
the adapter owns that explicit encoding; protobuf maps, bytes and well-known
`Struct`/`Value` remain outside `ProjectMessage`'s bounded profile.

`EncodeToolProjection` validates and writes canonical proto3 JSON;
`DecodeToolProjection` accepts snake_case or lowerCamelCase names and refuses
unknown fields. `ToolsFromProjection(prepared, projection)` first verifies the
complete binding, then the projection, contract equality and selector fields,
and refuses names colliding with that binding's direct `policy.tool` exposure.
It returns detached `ProjectedTool` messages. `TestToolProjectionRoundTripsEveryField`
holds both JSON fixtures, delivery and extraction to the schema's field list,
including every nested projection field, as `TestPolicyRoundTripsEveryField`
does for policy. A valid projection may accompany a binding with `policy.tool`
absent: this leaves the generic method hidden from direct discovery, and
`ToolFromPrepared` still returns `ErrNotATool`. An explicitly exposed generic
method is a separate tool; its effect does not classify the projected tools.

Discovery authenticates the declaration publisher and exposes only currently
permitted installations and declarations. Admission rejects name collisions
with other projections and other bindings' direct tools; `ToolsFromProjection`
cannot check tool sets it was not given. Each invocation and receipt lookup still
needs current delegated caller authority; neither a projection nor a read-only
effect grants it. `ToolsFromProjection` verifies data, not those live decisions.

`ScopeSlot` is the ownership precedent: a fixed operation can carry
installation-selected specifics. It is not a tool representation to reuse: scope
resolution fills authority policy, while this projection declares discovery and
one fixed argument. No scope resolution, new operation derivation, OpenAPI reader
extension or generic payload adapter is introduced here.

### Required scope slots

An owner cannot always spell its authority alone. A generic model operation
acts on a resource kind another module defines; a generic tool callback
forwards whatever resource kinds and actions the installed tool owners use;
and a fixed `invoke_scopes` entry can name those neither exactly (the owner
does not know them) nor by omission (an empty `resource_ids` is every resource
of the kind, which is the wildcard this seam exists to refuse).
`Operation.required_scope_slots` is the owner's explicit slot for that
authority. A `ScopeSlot` names the slot and states only what the owner needs
of whatever fills it: `required_actions` every selected invoke scope must
carry, and `lookup`, whether a read-only lookup scope over the same exact ids
must come with each invoke scope so a receipt can be read back. The resource
kinds, the actions and the exact ids are the composition's: it answers each
slot with a `ScopeSelection` carrying the `invoke` scopes it forwards and the
read-only `lookup` scopes that go with them.

Required slots may supply **all** of an operation's resource authority. An
unresolved declaration may leave both fixed `invoke_scopes` and `lookup_scopes`
empty; it need not invent an unrelated fixed permission or a wildcard of the
selected kind. Supplied fixed scopes still satisfy the usual bounds and lookup
subset rules. After every slot is resolved, both concrete scope lists must be
nonempty. `lookup: true` additionally requires read-only lookup over the same
exact ids for every invoke kind selected into that slot. A slot without that
flag may supply lookup too, but a completed policy with no lookup authority
is refused. The [slot-only fixture](../runnable/testdata/scope-slots/slot-only-operation.json)
is exercised through derivation, resolution, prepared delivery and the matching
resolved-policy receipt.

A declaration with neither concrete scopes nor required slots is refused as
missing owner authority. A declared slot with no selection is a different
refusal: `ResolveScopeSlots` names the required slot and says the composition
must supply its `ScopeSelection`. This holds even when fixed scopes already
exist. Readers must preserve `required_scope_slots` through resolution so a
missing selection remains distinguishable from a declaration without authority.

`OperationSpec.ResolveScopeSlots` is the one resolution to a concrete policy.
It validates the declaration it was handed before it resolves anything, then
refuses a selection naming no declared slot, a slot left unselected or
selected twice, a selection with no invoke scope, and any kind, action or id
that is empty, blank or a wildcard; a selected kind a fixed invoke scope or
another slot already binds; an invoke scope missing an action the owner
requires; a lookup scope that is not the one read-only action, names a kind no
invoke scope of the selection names, or names ids its invoke scope does not
cover; and, where the slot asks for lookup, an invoke scope without a lookup
scope over the same exact ids. It reads its inputs and never writes them. Its
result is the spec with the selected scopes appended to the owner's fixed
scopes, in declaration then selection order, no slots left, validated as any
policy is — so the owner's fixed scopes are never widened by a selection, and
the installed authority keeps the owner's and the composition's contributions
distinguishable by kind. The lookup rule is unchanged: a lookup scope is
exactly `read`, a subset of an invoke scope. A tool kind whose vocabulary has
no `read` therefore resolves invoke-only, and its receipts are read under the
owner's fixed read scopes; such a kind cannot fill a slot that asks for lookup.

A prepared binding carries concrete scopes only. `VerifyPrepared` refuses a
policy whose slots are unresolved (`runnable.ErrUnresolvedScopeSlots`, wrapping
`ErrInvalid`), so no writer delivers a hole. Readers fail closed on the schema
they do not know: `DecodePrepared` and `DecodeOperation` refuse a document
carrying a field they have no name for, and `OperationFromMethod` refuses a
method option carrying policy bytes this core does not know, because a reader
that dropped the field would emit a package and a binding with the requirement
erased and nothing downstream could tell them from complete ones. A derived
catalog's operation document is therefore decoded with `DecodeOperation`, not
a lenient JSON decoder, and a catalog that carries slots is written under an
index schema a reader without `DecodeOperation` rejects. The contract digest
is unchanged and covers input and output only — the runtime pins the installed
selection by digesting the prepared binding it admitted. A delivery writer
resolves once and hands that one concrete policy to both halves of the
installation: the prepared binding, and the resolved policy receipt
(`ResolvedPolicy`, `docs/runnable-binding-delivery.md`) the host binds its
audience and scopes from. `BindingMatchesResolvedPolicy` holds the two to each
other by operation, value and `PolicyDigest`; that gate, not the runtime's own
equality check, is what proves one resolution produced both.
