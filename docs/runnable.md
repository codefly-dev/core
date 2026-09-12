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
entrypoint:
  handler: handler.py          # the author entrypoint
  inputs: [pyproject.toml, uv.lock]   # everything else whose content changes the package
execution:
  facilities: [native, kubernetes]
  timeout: 2m
  cancellation: signal         # none | signal
  recovery: recompute          # recompute | receipt
  payload:
    max-input-bytes: 65536     # default 1 MiB for both bounds
service-dependencies:
  - name: store
    kind: runtime
    endpoints: [{ name: tcp }]
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

Loading rejects, with a message naming the rule: an escaping name or path, a
non-strict version, a `latest` or unpinned agent, an agent of another kind, an
unsupported `protocol`, a schema outside the bounded profile, a missing handler,
an unknown facility, cancellation or recovery, and a missing or non-positive
timeout. `Save` refuses to write a declaration that would not load.

### Bounded schema profile

The contract's schema is a small typed profile, not JSON Schema: `object`,
`array`, `string`, `integer`, `boolean`, with `optional` (key may be absent) and
`nullable` (value may be null) declared separately. Field names are
identifiers so every language agent can generate typed bindings. Enumerations,
arbitrary JSON Schema, expressions and coercion are not promised because a YAML
key can be written; `codefly.runnable/v1` is the only protocol accepted.

## Immutable installation facts

`proto/codefly/base/v0/runnable.proto` carries the wire contracts, generated
into `generated/go/codefly/base/v0`. The `runnable` Go package validates,
canonicalizes and digests two of them.

**`RunnablePackage`** (`codefly.runnable-package/v1`) is the descriptor of one
built release: identity, exact agent, contract, execution bounds, pinned build
inputs (handler, declared inputs, generated harness, toolchain, effective
configuration) and the built artifacts — a `NATIVE` package with its launch
command and/or a digest-pinned `IMAGE`, each with its platform. Its `digest` is
the sha256 of its deterministic bytes. A resolved execution-plan fingerprint
(`docs/design/execution-plan.md`) identifies a plan, not bytes; the package
digest is what proves two builds are the same executable.

`runnable.CompareRelease(existing, incoming)` is the registration rule: same
identity and same digest is an idempotent registration (`nil`); same identity
and a different digest is `ErrConflict`. A new version is a different release
and coexists.

**`RunnableBinding`** (`codefly.runnable-binding/v1`) is the installation of
one verified package on one execution facility. It pins the package digest,
the facility, the artifact selected from that package (a `NATIVE` artifact for
`native`, an `IMAGE` for `kubernetes`), the reachable addresses of the declared
service dependencies and the *names* of credentials the facility resolves at
launch. Coordinates and credential references live here, under deployment
trust; a value never does, and an invocation payload carries none of them.
`VerifyBinding` fails against a rebuilt package with the same identity, so an
existing binding can never be routed to newer bytes.

## Ownership of what is not here

- **Agents** (`Builder.Build` with an `output_directory` → `DockerBuildPlan`;
  `Builder.Package` → native artifact) emit recipes and packages. The CLI
  builds and publishes images (core#461) and assembles the descriptor; no
  agent builds an image.
- **CLI** owns the launcher: it executes the bound artifact's command with the
  resolved configuration, delivers bounded input, captures typed output apart
  from logs and reports malformed or missing output, non-zero exit and
  interruption as distinct outcomes, without retrying.
- **Orchestration** owns registration, activation, tasks, attempts and
  receipts, translating the binding into its own compute contract.
