# Dependency kinds

A `service-dependencies` entry says which service another one needs. Until now
it did not say **why**, so every edge constrained every operation: a generated
client that only matters at build time forced the producer to be started, and a
consumed endpoint that only matters at run time forced the producer to be built
first.

Declaring a `kind` says which execution phase the edge constrains.

## Kinds

| Kind | Meaning | Phases it constrains | What the consumer waits for |
| --- | --- | --- | --- |
| *(absent)* | Legacy edge | build, run, test, deploy | consumed endpoint health |
| `build` | Build/codegen input — a generated client, a shared artifact | build | nothing |
| `schema` | The producer's declared API contract is read at build time | build | nothing |
| `runtime` | A consumed endpoint | run, test, deploy | consumed endpoint health |
| `completion` | One-shot work (a migration, a seed job) must finish | run, test, deploy | completed run |
| `external` | A capability provided outside the workspace | none | nothing |

`completion` and `runtime` are not interchangeable. Polling a migration job for
endpoint health never succeeds; treating a long-running service as "done" starts
its consumers early. A `completion` dependency therefore cannot declare
`endpoints` — that combination is rejected at load.

An `external` dependency is declared so configuration and documentation can name
the capability. Codefly never builds, starts or orders it.

## Cycles are a per-phase property

The two-phase relationship below is legitimate: the API talks to the database at
run time, and the database bootstrap reads the API contract to generate its
migrations at build time. The union of both edges is a cycle; neither phase's
graph is.

```yaml
# services/api/service.codefly.yaml
name: api
service-dependencies:
    - name: database
      kind: runtime
      endpoints:
        - name: postgres
endpoints:
    - name: grpc
      api: grpc
      visibility: public
```

```yaml
# services/database/service.codefly.yaml
name: database
service-dependencies:
    - name: api
      kind: schema
      endpoints:
        - name: grpc
endpoints:
    - name: postgres
      api: tcp
      visibility: public
```

A cycle **within** one phase — two services each declaring a `runtime`
dependency on the other — still fails, and the error names the phase and the
path (`run dependencies have a cycle: a -> b -> a`).

## One-shot prerequisites

```yaml
# services/worker/service.codefly.yaml
name: worker
service-dependencies:
    - name: migration      # must have COMPLETED before the worker starts
      kind: completion
    - name: api            # only needed to generate a client
      kind: build
      endpoints:
        - name: grpc
    - name: stripe         # provided outside the workspace
      module: vendor
      kind: external
```

## What does not change

- Visibility is enforced on every consumed interface regardless of kind: a
  `build` edge crosses the same export boundary as a `runtime` one.
- An endpoint reference that names an endpoint the producer does not export is
  rejected for every kind.
- An entry with no `kind` behaves exactly as it did before kinds existed, so an
  existing workspace keeps its order and its validation errors.

## Migration

| Existing declaration | Keep as is when | Declare instead |
| --- | --- | --- |
| Dependency whose endpoints the consumer calls at run time | always safe | `kind: runtime` |
| Dependency used only to generate a client or read a schema | it also needs to be running | `kind: build` / `kind: schema` |
| Dependency on a job that must finish first | never — legacy waits for endpoint health | `kind: completion` |
| Dependency on something codefly does not manage | it is a real workspace service | `kind: external` |

Migration is per-edge and additive: an undeclared kind stays legacy, so a
workspace can be migrated one dependency at a time.

## API

```go
deps, _ := architecture.NewServiceDependencies(ctx, workspace)

// Fails when any phase's graph deadlocks, naming the phase and the cycle.
err := deps.VerifyAcyclic(ctx)

// Phase-selected closures — callers pick the phase they are executing rather
// than passing flags down the call chain.
buildOrder, _ := deps.ForPhase(resources.PhaseBuild).OrderTo(ctx, unique)
runOrder, _ := deps.ForPhase(resources.PhaseRun).OrderTo(ctx, unique)
```

Restricting a graph — by phase or to a single service — keeps the edge kinds and
drops the service lookup entries for nodes it removed, so a service that is not
in the restricted graph cannot be resolved through it.
