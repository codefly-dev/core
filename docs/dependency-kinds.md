# Dependency kinds

A `service-dependencies` entry says which service another one needs. Until now
it did not say **why**, so every edge constrained every operation: a generated
client that only matters at build time forced the producer to be started, and a
consumed endpoint that only matters at run time forced the producer to be built
first.

Declaring a `kind` says which execution stage the edge constrains.

## Kinds

| Kind | Meaning | Stages it constrains | What the consumer waits for |
| --- | --- | --- | --- |
| *(absent)* | Legacy edge | build, run | consumed endpoint health |
| `build` | Build/codegen input — a generated client, a shared artifact | build | nothing |
| `schema` | The producer's declared API contract is read at build time | build | nothing |
| `runtime` | A consumed endpoint | run | consumed endpoint health |
| `completion` | One-shot work (a migration, a seed job) must finish | run | completed run |
| `external` | A capability provided outside the workspace | none | nothing |

## Stages and phases are different things

A **stage** is an elementary graph that can be topologically sorted: `build` or
`run`. A **phase** is an operation a caller asks for, and it decomposes into
ordered stages:

| Phase | Stages, in order |
| --- | --- |
| `build` | build |
| `run` | run |
| `test` | build, then run |
| `deploy` | build, then run |

So a `build` dependency **does** constrain `test` and `deploy` — you cannot test
or deploy a service without building it first. What it never does is order a
run.

The stages must stay separate graphs. Merging them would union a build-time edge
with a runtime edge pointing the other way and report a cycle — exactly the
misclassification kinds exist to remove.

`completion` and `runtime` are not interchangeable. Polling a migration job for
endpoint health never succeeds; treating a long-running service as "done" starts
its consumers early. A `completion` dependency therefore cannot declare
`endpoints` — that combination is rejected at load.

An `external` dependency is declared so configuration and documentation can name
the capability. Codefly never builds, starts or orders it.

## Cycles are a per-stage property

The two-stage relationship below is legitimate: the API talks to the database at
run time, and the database bootstrap reads the API contract to generate its
migrations at build time. The union of both edges is a cycle; neither stage's
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

A cycle **within** one stage — two services each declaring a `runtime`
dependency on the other — still fails, and the error names the stage and the
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
  existing workspace keeps its order and its validation errors. In particular an
  endpointless producer under an undeclared kind stays valid: that is how
  workspaces expressed one-shot work before `completion` existed.

## Migration

| Existing declaration | Keep as is when | Declare instead |
| --- | --- | --- |
| Dependency whose endpoints the consumer calls at run time | always safe | `kind: runtime` |
| Dependency used only to generate a client or read a schema | it also needs to be running | `kind: build` / `kind: schema` |
| Dependency on a job that must finish first | never — legacy waits for endpoint health | `kind: completion` |
| `kind: runtime` onto a producer with no endpoints | never — it waits forever; this is rejected | `kind: completion` |
| Dependency on something codefly does not manage | it is a real workspace service | `kind: external` |

Migration is per-edge and additive: an undeclared kind stays legacy, so a
workspace can be migrated one dependency at a time.

## API

```go
deps, _ := architecture.NewServiceDependencies(ctx, workspace)

// Fails when any stage's graph deadlocks, naming the stage and the cycle.
err := deps.VerifyAcyclic(ctx)

// What a caller wants: the ordered stages of an operation. `test` and `deploy`
// return a build order followed by a run order.
stages, _ := deps.OrderFor(ctx, resources.PhaseTest, unique)
for _, stage := range stages {
    // stage.Stage is resources.StageBuild, then resources.StageRun
    // stage.Services is that stage's order
}

// A single stage's graph, when that is what you need.
build, _ := deps.ForStage(resources.StageBuild)
buildOrder, _ := build.OrderTo(ctx, unique)
```

An unknown stage or phase name is an error, never an empty result: a graph with
no edges is indistinguishable from "nothing depends on anything", so a typo
would silently remove all ordering.

Restricting a graph — by stage or to a single service — keeps the edge kinds and
drops the service lookup entries for nodes it removed, so a service that is not
in the restricted graph cannot be resolved through it.
