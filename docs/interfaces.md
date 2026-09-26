# Interfaces

An **interface** is a named, versioned contract: *what* is offered, independent
of *who* offers it. Modules implement interfaces; services require them. A
consumer can then say *"I need a cache at `^0.3`"* instead of *"I need the
service `redis` in module `platform`"*, and an update can be judged by whether
the interfaces a module implements still satisfy what its consumers require,
not by the module's own version number.

Core owns the mechanism only. It defines no interface: `codefly.dev/cache` is
published next to the cache driver that reads it, not here. Core names,
versions, binds and checks interfaces it has never heard of.

## The concepts

| Term | What it is | Example |
| --- | --- | --- |
| **Interface** | A published contract, identified as `<publisher>/<name>@<version>`. Owned by whoever publishes it. | `example.dev/widgets@1.2.0` |
| **Endpoint** | One concrete address one service exposes: a name, an API, a visibility, a port at run time. | `platform/api/grpc` |
| **Capability** | A configuration group a service emits for consumers that reach it through a library driver, not a call. | redis's `connection` group |
| **Implementation** | A module's statement that one of its endpoints, or one of its services' capabilities, fulfils an interface version. | `implements: [example.dev/widgets@1.2.0]` |
| **Requirement** | A consumer's dependency on an interface at a version range. | `interface: example.dev/widgets@^1.1` |
| **Binding** | Choosing, for one requirement, the one implementation in scope that satisfies it. | `^1.1` → `platform/api/grpc` |

### Interface versus endpoint

They are different kinds of thing, and the relationship between them is
many-to-many.

| | Interface | Endpoint |
| --- | --- | --- |
| Answers | *what* is offered | *where and how* to reach it |
| Owned by | its publisher | the service that exposes it |
| Versioned | yes, semver, and diffed for compatibility | no |
| Exists without the other | yes: it is a definition, implemented by any number of endpoints, or by none | yes: an endpoint need implement no interface |

- **One endpoint can implement several interfaces.** A gRPC port carries
  several protobuf services, and often two major versions of one side by side
  (`widgets.v1` and `widgets.v2`).
- **One interface can be implemented by many endpoints**, in unrelated modules.
  That is what lets a consumer depend on the interface instead of on either.
- **Some interfaces are implemented by no endpoint at all.** A `capability`
  interface is implemented by the configuration a service emits.

In Go terms: the interface is the interface type, an endpoint is one value's
address, and the module's `implements` list is the statement that the value
satisfies the type.

### Where it sits in the resource family

| Resource | What it is |
| --- | --- |
| Workspace | The root. Composes modules, other versioned workspaces and solutions. |
| Module | The deployment and release unit. Its `interface` declares its boundary and what it implements. |
| Service, Job, Runnable, Application | What a module contains and codefly runs, schedules or builds. |
| Library | Shared code services build against. |
| **Interface** | A contract published by its owner, independent of any implementation. |

A *solution* is not a separate kind: it is a module a product workspace adds on
top of the workspaces it composes (`solutions:` holds module references).

## 1. Publishing an interface (the owner)

The owner publishes an `interface.codefly.yaml`:

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

- The **identity** is `<publisher>/<name>@<version>`. The publisher is a
  lowercase domain-like name; the name is lowercase letters, digits and dashes;
  the version is a strict semantic version with no `v` prefix.
- The version belongs to the interface. An implementation can release as often
  as it likes without changing it.
- The **type** selects how core checks the interface, and which one section
  carries its surface:

| Type | Surface section | Implemented by |
| --- | --- | --- |
| `grpc`, `connect` | `protobuf`: package, services, methods | an endpoint of the same API |
| `rest`, `http` | `openapi`: routes (method, path) | an endpoint of the same API |
| `mcp` | `mcp`: tools, resources | an `mcp` endpoint |
| `capability` | `capability`: configuration group, keys | a service's configuration |

`tcp` is a transport and carries no interface.

Core never fetches a definition. The host attaches an `InterfaceResolver` with
`WithInterfaceResolver`, the way it attaches a workspace resolver for
composition. `ResolveInterface` refuses a definition whose identity is not
exactly the one asked for.

## 2. Implementing an interface (the module)

A module declares what it implements in its `interface`. `implements` is always
a list:

```yaml
interface:
    endpoints:
        - service: api
          endpoint: grpc
          visibility: public
          implements:
              - example.dev/widgets@1.4.0
              - example.dev/widgets@2.0.0
              - example.dev/admin@1.0.0
        - service: redis
          endpoint: tcp
          visibility: public
    capabilities:
        - service: redis
          implements:
              - codefly.dev/cache@0.3.0
```

Rules:

- A module implements each interface version once.
- One entry lists at most one version per compatible line: `1.4.0` next to
  `2.0.0` is two lines side by side; `1.3.0` next to `1.4.0` is refused, since
  `1.4.0` already covers `1.3.0`.
- A service's capabilities go in one `capabilities` entry, which implements at
  least one interface.

**The export boundary.** Endpoint entries make the interface the module's
export boundary: once there is one, an endpoint the interface does not list is
private outside the module. A capability consumed over the network therefore
needs its endpoint exported too, as `redis/tcp` is above. Capability entries on
their own state what a service provides, not which endpoints cross module
lines, and leave the boundary as it was.

**Conformance.** `ValidateInterfaceConformance` checks each implementation
against the published definition: an endpoint implements interfaces of its own
API, and a capability entry implements capability interfaces. When a resolver
is attached, loading the module runs it, so a misdeclared module fails to load
instead of misleading its consumers. `ValidateProvidedConfiguration` checks
what a service emits against every capability it implements: the group is
present, every required key is in it, each key is secret exactly when the
interface says so, and no undeclared key appears.

A flat-layout workspace has no module boundary and so implements no
interfaces.

## 3. Requiring an interface (the consumer)

A service dependency can name an interface and a range, instead of or as well
as a service:

```yaml
service-dependencies:
    # Any provider in scope that satisfies the range.
    - interface: codefly.dev/cache@^0.3
    # This named service, which must implement a version in range.
    - name: api
      module: platform
      interface: example.dev/widgets@^1.1
```

The range is required: a requirement admitting every version would bind to a
provider whatever breaking change it later shipped.

### How binding chooses

The workspace binds every requirement when it loads the service:

1. With a named service, that service must implement a version in range. It is
   checked, never rewritten.
2. Otherwise, exactly one implementation in scope must satisfy the range, or
   the workspace chooses one:

   ```yaml
   interface-bindings:
       - interface: codefly.dev/cache
         module: edge
         service: memcache
   ```

No provider, or several with no binding, is an error; codefly never picks one
for you. A binding chooses among providers and never admits one outside the
consumer's range. Every declared binding must name a service that implements
the interface, whether or not a dependency uses it, so a misspelled binding
fails instead of matching nothing.

### What binding produces

A bound requirement is an ordinary named edge: ordering, visibility, readiness
and network mappings treat it like any other. An endpoint interface binds to
the implementing endpoint, except for a `completion` edge, which consumes none.

Several requirements may bind to the same provider, for example two interfaces
one gRPC endpoint serves. They stay separate dependencies: the dependency graph
merges their edges and unions their kinds.

A bound requirement is still the author's requirement of an **interface**, not
of the provider it was bound to:

- a save writes the declaration back, never the provider;
- removing the provider's dependencies leaves the requirement in place, and the
  next load binds another provider or reports that none is in scope;
- adding a named edge onto the bound provider adds a separate dependency.

### Where binding happens

| Loaded through | Interface dependencies |
| --- | --- |
| A workspace (`LoadService`, a module the workspace composed) | bound |
| A directory inside a workspace, as the SDK and agents find their service | bound against that workspace (`ApplyInterfaceBindings`) |
| A module loaded on its own | left unbound: reading its endpoints and exports needs no provider |

Any path that *uses* an unbound interface dependency refuses it instead of
resolving nothing. Jobs, runnables and applications do not bind: one that
requires an interface is refused at load.

## 4. Evolving an interface (the owner)

`EvolveInterface(before, after)` diffs two published versions of one interface
and rejects a version that under-states what changed. No author's claim is
consulted.

| Change | Kind |
| --- | --- |
| removed method, route, tool, resource or key | breaking |
| changed key (secret, optional), renamed configuration group, changed type | breaking |
| added required key | breaking |
| anything else added | additive |

| Kind | Version must |
| --- | --- |
| breaking | leave `^before`: a new major; below 1.0.0 a new minor; below 0.1.0 a new patch |
| additive | not be a patch inside `^before` |

The diff is structural: it sees the surface a definition declares, not a
message field changed behind an unchanged method. Wire-level breaking-change
detection for protobuf remains the schema plugin's job.

## 5. Judging a module update

`SemanticReport.AddInterfaceEvolutions` feeds the implemented interfaces into
the module update report. It validates the definitions it is given and returns
an error for an invalid one. Definitions are paired per compatible line, so a
module implementing `1.x` and `2.x` side by side is judged on each. The update
is blocked by:

- a version that under-states its change;
- a version published again with a different surface;
- a line the module stops implementing, since its consumers lose their provider.

## How the existing contract notions relate

These are not migrated; this is how they line up.

| Notion | Relation |
| --- | --- |
| `ModuleInterface` (`module.interface`) | The module's side: what it exports, at what visibility, and which interface versions it implements. |
| `APIContractCatalog` | The descriptor set or OpenAPI document one exported endpoint serves, pinned by digest in a package: the concrete artifact behind a `grpc`/`rest` implementation. |
| `RunnableContract` | The typed input and output of a runnable. A possible future interface type; not one today. |
| `standards.APIS()` | The endpoint APIs. Endpoint interface types are spelled the same. |
| Configuration groups | What a `capability` interface schematises. |

## Who does what

Core defines the model and every check. The host (the CLI):

- attaches the `InterfaceResolver`;
- calls `AddInterfaceEvolutions` when it builds a module update report;
- calls `ValidateProvidedConfiguration` on what `CreateConnectionConfiguration`
  returns;
- owns environment-specific providers, including an environment's managed
  replacement of a bound service, which keeps applying to that service by
  `module/service`.

Not in this version:

- The protos do not carry implementations or definitions; nothing sends them to
  an agent yet. The definition file is a YAML declaration like its siblings,
  and the evolution is part of the Go `SemanticReport`.
- Bindings are workspace-wide; there are no environment-specific bindings.
