# Resolved execution plan

## Why

Every Codefly entry point that needs a dependency graph recomputes one. `codefly
run`, `codefly test`, the SDK's `WithDependencies`, and the deployment path each
walk the workspace, resolve modules, pick agents and derive configuration wiring
independently. Nothing names the *result* of that resolution, so nothing can be
compared, cached, explained or replayed: two invocations that resolve the same
way are indistinguishable from two that do not.

The execution plan is that named result. It is the immutable set of resolved
facts one invocation will act on, separate from the declarations it was derived
from.

## Requested declarations vs. resolved facts

The distinction the model turns on:

| Declaration (input, mutable)                  | Resolved fact (plan, immutable)                          |
| --------------------------------------------- | -------------------------------------------------------- |
| `service.codefly.yaml` `service-dependencies` | `Edge` between two selected nodes, with consumed endpoints |
| `module.codefly.yaml` reference + overlay      | `Artifact` with the winning source, version and verification |
| `agent:` block                                 | `Backend` with the exact publisher/name/version           |
| `workspace-configuration-dependencies`         | `ConfigurationOrigin` naming the producer, never a value  |
| jobs declared on a module                      | ordered `SchemaStep` list                                 |
| "run this service"                             | `Target` plus the transitive closure actually selected    |

A declaration can be ambiguous, unresolvable or overridden. A plan cannot: it
either resolves or fails validation.

## Reused types

Nothing in `executionplan` re-implements resolution. The model is the shared
vocabulary; `architecture` populates it from existing core types:

- `resources.Workspace.ResolveModule` already computes overlay/pin/layout
  precedence and returns a `ModuleResolution` — the plan records which rule won
  and what it beat.
- `resources.Service.Agent` is the backend choice.
- `resources.Service.ServiceDependencies` and `ConsumedEndpoints` give edges and
  endpoint requirements.
- `resources.EndpointAsEnvironmentVariableKey` and
  `ServiceConfigurationEnvironmentKeyPrefixFromUnique` give the configuration
  keys, so a plan's origins are the keys services actually receive.
- `architecture.DAG` gives cycle detection and topological order.
- `executionreceipt` is the precedent for the shape: a versioned schema constant,
  pure validation, and a deterministic digest over canonical bytes.

## Semantic identity

`Plan.SemanticFingerprint` hashes everything that changes what an invocation
does, and nothing else. Excluded by construction:

- `Plan.Invocation` — invocation id, start time, actor, and the workspace
  directory. Two checkouts of the same workspace at different paths plan
  identically, so a warm session started from one is reusable from the other.

Included: the requested target and environment, every selected node, backend,
artifact version/digest/verification, every typed edge, every configuration
origin, the ordered schema steps and the state policy.

The fingerprint is a bare lowercase sha256 hex so it can be carried directly as
the `Fingerprint` field of a session ledger record (core#424). The hash *format*
is versioned inside the hashed bytes (`SemanticHashFormatV1`), so changing how
the digest is computed changes every fingerprint rather than silently colliding
with the old format.

## Determinism

`Canonical` sorts every set the plan does not order semantically — nodes, edges,
artifacts, endpoint requirements, configuration origins — and preserves the two
that are ordered: `SchemaStep` sequence (a migration order is not a set) and
`Selection.Via` (a dependency path). The model contains no Go maps, so struct
field order fixes the JSON encoding.

## Lazy closure

`architecture.SelectClosure` walks out from the requested target and loads only
the modules that closure reaches. `NewServiceDependencies` eagerly loads every
module in the workspace, so one uncomposed or broken module fails planning for
targets that never touch it. A module the closure *does* reach and cannot load
becomes an unresolved node carrying an actionable reason; `Verify` then rejects
it, rather than the error being omitted along with the node.

## Secrets

`ConfigurationOrigin` has no value field. A secret is represented by
`Secret`, `SecretRef` and `SecretVersion` — a coordinate the consumer resolves at
runtime. There is no code path that can serialize a configuration value, a token
or a control nonce into a plan.

## Schema versioning and rollout

`SchemaV1` (`codefly.execution-plan/v1`) is rejected on mismatch rather than
best-effort parsed: a plan is a contract between a producer and a warm-reuse
consumer, and a partially-understood plan is worse than a refused one. A future
`v2` gets its own constant and its own hash format constant; a producer emits one
version at a time.

Rollout:

1. **Model + closure (this stage).** The plan exists and is validated; nothing
   consumes it in production paths. `NewServiceDependencies` is untouched.
2. **CLI planning.** `codefly run` / `codefly test` build the plan and drive
   ordering from it instead of recomputing the graph, and gain a `--explain`
   rendering of `Selection` (companion change in `codefly-dev/cli`).
3. **Warm reuse.** The session ledger compares `SemanticFingerprint` to decide
   reattach vs. rebuild.
4. **Deployment.** The deploy path consumes the same plan rather than its own
   traversal.

## Edge kinds

`EdgeKind` uses the same vocabulary as the typed dependency kinds introduced for
core#423 (`build`, `runtime`, `completion`, `schema`, `external`), plus
`declared` for an untyped legacy edge. Until a declaration carries a kind, every
edge a workspace produces is `declared`; the builder maps the typed kind through
once it is available, with no change to the plan schema.
