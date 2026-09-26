# Interfaces

An **Interface** is a named, versioned contract that modules implement and
services require. It lets a consumer say *"I need a cache at ^0.3"* instead of
*"I need the service called redis in module platform"*, and it lets an update be
judged by whether the interfaces a module implements still satisfy the ranges
its consumers require, rather than by the module's own version number.

Core owns the mechanism only. It defines no interface: `codefly.dev/cache` is
published next to the cache driver that reads it, not here. Core names,
versions, binds and checks interfaces it has never heard of.

## Where it sits in the resource family

| Resource | What it is |
| --- | --- |
| Workspace | The root. Composes modules, other versioned workspaces and solutions. |
| Module | The deployment and release unit. Its `interface` is its boundary. |
| Service, Job, Runnable, Application | What a module contains and codefly runs, schedules or builds. |
| Library | Shared code services build against. |
| **Interface** | A contract published by its owner, independent of any implementation. |

A *solution* is not a separate kind: it is a module a product workspace adds on
top of the workspaces it composes (`solutions:` holds module references).

The definition and the implementation are separate on purpose. The owner of an
interface publishes its definition; a module declares in its `interface` that
it implements a version of it. Two unrelated modules can implement the same
interface, which is what lets a consumer depend on the interface and not on
either of them.

## The definition

`interface.codefly.yaml`, published by the owner:

```yaml
kind: interface
publisher: codefly.dev
name: cache
version: 0.3.0
type: capability
capability:
    configuration: connection
    keys:
        - name: url
        - name: password
          secret: true
        - name: tls
          optional: true
```

Its identity is `<publisher>/<name>@<version>`, e.g. `codefly.dev/cache@0.3.0`.
The version is a strict semantic version and belongs to the interface: an
implementation can release as often as it likes without changing it.

The `type` selects how core checks it, and which one section carries its
surface:

| Type | Section | Implemented by |
| --- | --- | --- |
| `grpc`, `connect` | `protobuf`: package, services, methods | an endpoint of that API |
| `rest`, `http` | `openapi`: routes | an endpoint of that API |
| `mcp` | `mcp`: tools, resources | an `mcp` endpoint |
| `capability` | `capability`: configuration group, keys | a service's configuration |

`tcp` is a transport and carries no interface. A capability is for things a
consumer reaches through a library driver rather than an endpoint it calls; it
turns the configuration key names consumers read, which were an informal
agreement, into a checked one.

Core never fetches a definition. A host attaches an `InterfaceResolver` with
`WithInterfaceResolver`, the way it attaches a workspace resolver for
composition, and `ResolveInterface` refuses a definition whose identity is not
exactly the one asked for.

## Implementing an interface

A module declares what it implements in its `interface`:

```yaml
interface:
    endpoints:
        - service: api
          endpoint: grpc
          visibility: public
          implements: example.dev/widgets@1.2.0
        - service: redis
          endpoint: tcp
          visibility: public
    capabilities:
        - service: redis
          implements: codefly.dev/cache@0.3.0
```

A module implements each interface version once. `ValidateInterfaceConformance`
checks each declaration against the published definition: an endpoint
implements an interface of its own API, and a capability entry implements a
capability interface. `ValidateProvidedConfiguration` checks what a service
emits for its consumers against every capability it provides: the group is
present, required keys are in it, each key is secret exactly when the
interface says so, and no undeclared key appears.

A declared interface is the module's export boundary, capability entries
included: an endpoint the interface does not list is private outside the
module. A capability consumed over the network therefore needs its endpoint
exported too, as `redis/tcp` is above.

A flat-layout workspace has no module boundary and so implements no
interfaces; consumption inside one module stays by service name.

## Requiring an interface

A service dependency can name an interface and a range instead of, or as well
as, a service:

```yaml
service-dependencies:
    - interface: codefly.dev/cache@^0.3
    - interface: example.dev/widgets@^1.1
      kind: runtime
```

The workspace binds it when a module it composes loads the service:

- with a named service, that service must provide a version in range;
- otherwise exactly one provider in scope must satisfy the range, or the
  workspace chooses one:

  ```yaml
  interface-bindings:
      - interface: codefly.dev/cache
        module: edge
        service: memcache
  ```

No provider, or several with no binding, is an error. A binding chooses among
providers and never admits one outside the consumer's range. Once bound, the
dependency is an ordinary named edge: ordering, visibility, readiness and
network mappings treat it like any other, and an endpoint interface binds to
the implementing endpoint. A save writes the author's declaration back, never
the provider this workspace bound.

A module loaded on its own cannot bind, and says so. An agent, which loads the
service it serves by directory, binds through `ApplyInterfaceBindings` against
the workspace it was given. Any runtime path that meets an unbound interface
dependency refuses it instead of resolving nothing.

## Compatibility is computed

`EvolveInterface(before, after)` diffs two published versions of one interface:

- a removed method, route, tool, resource or key, a changed key, a renamed
  configuration group, or a newly required key is **breaking**;
- anything else added is **additive**.

A breaking change must leave the range a consumer of the earlier version
requires (`^before`: a new major, or a new minor below 1.0.0). An addition
needs at least a new minor. A version that under-states what the surface shows
is rejected; no author's claim is consulted.

`SemanticReport.AddInterfaceEvolutions` feeds this into the module update
report. Definitions are paired per compatible line, so a module that implements
`1.x` and `2.x` side by side is judged on each. An under-stated version, or a
line the module stops implementing, blocks the update.

The diff is structural. It sees the surface a definition declares, not a
message field changed behind an unchanged method; wire-level breaking-change
detection for protobuf remains the schema plugin's job.

## How the existing contract notions relate

These are not migrated; this is how they line up.

| Notion | Relation |
| --- | --- |
| `ModuleInterface` (`module.interface`) | The module's side of an Interface: what it exports, at what visibility, and now which interface versions it implements. |
| `APIContractCatalog` | The descriptor set and OpenAPI document of each exported endpoint, pinned by digest in a package. The concrete artifact behind a `grpc`/`rest` implementation. |
| `RunnableContract` | The typed input/output of a runnable. A future interface type; not one today. |
| `standards.APIS()` | The endpoint APIs. Endpoint interface types are spelled the same. |
| Configuration groups | What a `capability` interface schematises. |

## Not in this version

- The protos do not carry `implements`, capabilities or definitions: nothing
  sends them to an agent yet. The definition file is a YAML declaration like its
  siblings, and the evolution is part of the Go `SemanticReport`.
- Only services bind interface dependencies. A job, runnable or application
  that requires an interface is refused at load rather than carried unbound.
- Bindings are workspace-wide. Environment-specific providers, including an
  environment's managed replacement of a bound service, belong to the CLI.
- The host supplies the resolver, calls `AddInterfaceEvolutions` when it builds
  an update report, and calls `ValidateProvidedConfiguration` on what
  `CreateConnectionConfiguration` returns. Core defines those checks; it does
  not run the flows that call them.
