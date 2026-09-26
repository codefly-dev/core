# Architecture Overview

## The Core Insight

Running systems have formal APIs: REST endpoints, gRPC services, GraphQL schemas. But development operations -- "run tests", "start dependencies", "build", "deploy", "scaffold a service" -- have always been implicit. They live in Makefiles, shell scripts, READMEs, and tribal knowledge.

Codefly formalizes development operations as **typed gRPC APIs**.

```
RuntimeService.Load()     →  Load the service agent
RuntimeService.Init()     →  Compile, configure, allocate ports
RuntimeService.Start()    →  Run the service process
RuntimeService.Test()     →  Execute tests
RuntimeService.Stop()     →  Graceful shutdown
RuntimeService.Destroy()  →  Full cleanup

BuilderService.Load()     →  Load the build agent
BuilderService.Create()   →  Scaffold a new service
BuilderService.Build()    →  Compile/package
BuilderService.Deploy()   →  Ship to target environment
```

Same API surface regardless of whether the service is Go, Python, Rust, or a managed database. The agent plugin handles language-specific implementation. This makes development operations composable, discoverable, and automatable by both humans and AI agents.

Development operations are local-first plugin contracts. Runtime `Lint`,
`Build` (native compile/typecheck), and `Test`, plus Builder `Sync`, `Audit`,
`SBOM`, and deployable `Build`, are invoked unchanged by local CLI commands,
Mind/editor integrations, and CI. CI may select affected resources, expand
dependencies, schedule, cache, enforce policy, and persist a report; it must
not introduce a parallel execution service or own language/schema commands.

## Contract source of truth

Core owns shared runtime and agent contracts. The CLI-owned coordinate document
and deployment YAML are an explicit exception: their existing wire format and
strict parser live with the deployment consumer, not under Core's proto tree.
See [runtime configuration and deployment ownership](core-cli-boundary.md).

Protobuf is the default source of truth for Codefly contracts. This applies not
only to gRPC methods, but also to public reports, manifests exchanged between
components, persisted evidence, events, policy envelopes, and any state that
crosses a package, process, language, or repository boundary.

- define the schema under `core/proto` before writing consumer code;
- generate bindings with Codefly itself:

  ```text
  codefly generate proto --proto ./proto --output ./generated --local
  ```

- import the generated binding from `core/generated`; do not mirror it with a
  handwritten Go/TypeScript/Python DTO;
- evolve contracts additively with stable field numbers and an explicit schema
  or protocol version where the artifact has an independent lifecycle;
- dispatch schema lint, compatibility, generation, and drift through the
  owning Codefly schema plugin, then run the affected generated consumers.
  Provider workflows and language plugins must not invoke `buf`, `protoc`, or
  generator binaries directly.

Handwritten structs are reserved for private, in-process implementation state
that has no serialization or compatibility meaning. An exception at a public
boundary must be explicit and documented; convenience alone is not an
exception.

Per-consumer module update evidence and verdicts use `codefly.update.v0`.
See [module updates](module-updates.md) for the offline comparison, authenticated
release artifacts, usage completeness requirements, and delivery ownership.

A module package carries the descriptor set and OpenAPI document of every
interface endpoint under `contracts/api/`, catalogued in
`contracts/api/catalog.codefly.json` (`codefly/module-api-contracts/v1`).
Consumers generate clients from the package, never from a checkout of the
producing repository. The module `interface` block is the authoritative export
boundary: only its endpoints are exported and graphed across modules.

A module that declares one therefore states each export once. An entry's
`visibility` is the visibility that endpoint carries across module lines, and an
endpoint no entry names does not cross them at all, however the service declares
it — `visibility:` on the endpoint governs the module's own inside. The boundary
is applied where a module hands out its services, so every reader observes the
same value: the static passes, the module graph, the run and deploy resolution,
and the protos the module publishes. A service loaded by directory rather than
through its module — an agent loading the service it serves — applies it with
`resources.ApplyModuleInterface`.

The one endpoint the boundary leaves alone is the one whose visibility is the
deprecated `external`, because there that word records a location rather than a
permission, and it is the only record of it. Rewriting it would move an endpoint
resolved from DNS inside the system and give it an allocated port. Declare
`location: external` alongside a real visibility and the endpoint exports like
any other.

### Client facades

A published client pairs the generated bindings with a small facade that binds
them to a gateway seam and hides the transport envelope. Those facades are
themselves generated, not hand-written: the proto companion ships
`protoc-gen-codefly-facade-{python,go,ts}`, and `proto.GenerateClient` drives
them from either a descriptor set (what a module package carries) or proto
sources. Per proto package they emit one client class per service and a
`<module>` entry point (`accounts(gw)` / `accounts.New(gw)`), each RPC taking
the request message and returning the response message. The hand-written
facades still living in consumer repos (`saas-sdk-go`, `saas-sdk-python`,
`@codefly-dev/saas-sdk`) are to be regenerated through these plugins, not
maintained by hand.

### Universal failures

Plugin boundaries use `codefly.base.v0.Failure` as the universal structured
error detail. gRPC failures carry it inside standard `google.rpc.Status`;
domain responses may embed the same message when partial output or failure
evidence must still be returned. Callers must branch on `FailureCode`, not parse
human-readable messages.

The contract separates transport behavior (`google.rpc.Code`) from the stable
Codefly reason, and includes retryability, operation/resource identity, field
violations, normalized diagnostics, bounded process evidence, typed `Any`
details, retry delay, and causal failures. Operation-specific enums remain only
for meaningful domain outcomes such as audit `CLEAN/FINDINGS` or SBOM
`COMPLETE`; repeated string error channels are removed and their protobuf
names/numbers reserved rather than maintained as a second failure model.

All Builder and Runtime lifecycle statuses expose an optional `Failure` field.
Core's shared response wrappers populate it automatically, and
`failures.Wrap` lets plugin implementations classify native errors once while
preserving the original Go cause. When Core turns an error status into a host
error, it retains the typed detail; CLI, Mind, editor, and CI consumers can
therefore apply the same retry, policy, and remediation behavior.

The Code service has one RPC, `Execute`, and one failure location,
`CodeResponse.failure`; operation-specific result messages never carry their
own error channel. Every Tooling RPC response carries `Failure` directly
because each response is its own RPC envelope. Unsuccessful build, test, and
lint outcomes must include a failure even when they also retain structured
diagnostics, counts, or process output.

## Resource Hierarchy

```
Workspace → Module → Service → Endpoint
     │          │         │         │
     │          │         │         └── API type (gRPC, REST, HTTP, TCP)
     │          │         └── Managed by an Agent (go-grpc, postgres, etc.)
     │          └── Logical grouping of services
     └── Root container (one per project)
```

Every resource is a YAML file:

```yaml
# workspace.codefly.yaml — root of the project
name: my-platform
layout: modules     # or "flat"

# module.codefly.yaml — inside each module directory
name: backend

# service.codefly.yaml — inside each service directory
name: api-server
agent: go-grpc
version: 0.1.0
endpoints:
  - name: grpc
    api: grpc
    visibility: module
service-dependencies:
  - name: store/postgres
```

## Agent Model

The [CLI-agent contract](agent-contract.md) versions protocol compatibility and
required capabilities independently of the Core library release.

Core and the CLI must not carry concrete-agent compatibility tables or infer
compatibility from release pins. They check the running process's advertised
protocol and operation requirements over the generic boundary before work.
An unchanged protocol requires no fleet rebuild or repinning when Core or the
CLI releases. Agent names and artifact versions are opaque selection metadata,
not compatibility policy. Agent-specific selection, configuration, framework
paths and cleanup stay with the owning plugin; see the ownership and review
rules in [the compatibility contract](agent-contract.md#no-static-agent-compatibility-knowledge).

An agent is a **plugin binary** that implements the development API for a specific service type. When the CLI needs to start a Go gRPC service, it spawns the `go-grpc` agent process, connects over gRPC, and calls `Runtime.Load() → Init() → Start()`.

```
CLI ──gRPC──→ Agent Process ──→ Service Process
                  │
                  ├── Load(): read service.codefly.yaml, parse settings
                  ├── Init(): compile, resolve deps, allocate ports
                  ├── Start(): exec the service binary
                  ├── Test(): run `go test ./...`
                  └── Stop()/Destroy(): kill process, cleanup
```

Agents are downloaded binaries managed by the agent manager (`agents/manager/`). Each agent declares its identity in `agent.codefly.yaml`.

**Key agents:**
- `go-grpc` — Go services with gRPC endpoints
- `postgres` — Managed Postgres (Docker for local, cloud for prod)
- `external-temporal` — Temporal workflow engine
- `nextjs` — Next.js frontend services

## Network Model

Connection strings change across environments. `localhost:5432` locally, `host.docker.internal:5432` from Docker, `db.prod.internal:5432` in k8s.

Codefly solves this with **NetworkMapping**: each endpoint gets multiple **NetworkInstances**, one per access type.

| Access Type | Hostname | Use Case |
|---|---|---|
| `native` | `localhost` | Local dev, tools like pgAdmin |
| `container` | `host.docker.internal` | Docker-to-host communication |
| `public` | configurable | Production/public endpoints |

Port allocation is **deterministic**: `SHA256(workspace + module + service + endpoint + api) → port`. The same service always gets the same port. This means pgAdmin configs, DataGrip connections, and browser bookmarks survive restarts.

```go
port := network.ToNamedPort(ctx, "myworkspace", "backend", "api", "grpc", "grpc")
// Always returns the same port for these inputs
```

For tests and ephemeral environments, `RuntimeManager.WithTemporaryPorts()` uses random free ports instead.

### Endpoint carriers

Endpoints reach a process as environment variables, one per endpoint:

| Carrier | Value | Use |
|---|---|---|
| `CODEFLY__ENDPOINT__<MODULE>__<SERVICE>__<NAME>__<API>` | a dependency's address; for the service's **own** endpoint, the address it **listens** on (a Kubernetes render localizes it to `localhost:<port>`) | dial a dependency; bind a listener |
| `CODEFLY__SELF_ENDPOINT__<MODULE>__<SERVICE>__<NAME>__<API>` | the service's own endpoint as its **peers** reach it | advertise yourself — e.g. register an upstream with a gateway |

Both keys share one normalization (upper case, `-` → `_`), and the self
carrier deliberately does not start with `CODEFLY__ENDPOINT__`, so a reader
collecting endpoint carriers never mistakes one for the other. Values have the
`NetworkInstance.Address` shape: `host:port`, or a scheme-qualified URL for
HTTP-based APIs (`http://backend.<namespace>.svc.cluster.local:8080`).

Where the self value comes from:

- **Kubernetes render** (`DeployKustomize` with `OwnEndpoints`): the
  container-access instance of the service's own mapping — the in-cluster
  Service DNS name. Never the public instance, never localhost.
- **Local run**: the agent calls `RuntimeWrapper.AddSelfEndpoints(ctx)` in
  Init, which selects the instance under the runtime's own `NetworkAccess()`
  (host-native for native/nix, `host.docker.internal` for a container).

Read it with `resources.FindSelfNetworkInstanceInEnvironmentVariables`. A
missing self carrier is an error, never a fallback to the listen address:
advertising `localhost` to a peer is the defect this carrier exists to prevent.

## Configuration Flow

1. Service A produces configuration (e.g., postgres connection string)
2. Service B declares dependency on A in `service.codefly.yaml`
3. CLI resolves the dependency graph and injects configs as environment variables
4. Pattern: `CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__NAME__KEY=value`
5. Secrets get a separate prefix: `CODEFLY__SERVICE_SECRET_CONFIGURATION__...`

Configuration values carry a `Secret` flag. The system never logs secret values and uses the separate env var prefix for them.

`resources.IsSensitiveKey` is the name-based safety net for a value whose
`Secret` flag was forgotten: diagnostics redact it, restricted Kubernetes
renders refuse it inline, and the CLI promotes it to a secret reference. Its
markers (`PASSWORD`, `SECRET`, `TOKEN`, `CREDENTIAL`, `API_KEY`, …) match as
substrings. `AUTH` is matched **per word** instead: any word containing `AUTH`
is sensitive (`AUTH`, `BASIC_AUTH`, `OAUTH`, `AUTHKEY`, `AUTHORIZATION`, …)
except a closed list of public words — `AUTHOR(S)`, `AUTHORITY`/`AUTHORITIES`,
`AUTHORIZE` — so `AUTHORITY_ISSUER`, `AUTHORITY_AUDIENCE` and
`IDENTITY_AUTHORIZE_URL` stay public, while a credential noun beside a public
word (`CERTIFICATE_AUTHORITY_KEY`) keeps the key sensitive. A new public word
is added with a test, never inferred.

## Runtime Contexts

The same code runs in four contexts:

| Context | Network Access | Description |
|---|---|---|
| `native` | `native` | Process runs directly on host |
| `nix` | `native` | Process runs in Nix shell on host |
| `container` | `container` | Process runs inside Docker |
| `free` | varies | No assumptions (testing) |
| `kubernetes` | `container` | Workload rendered for Kubernetes (deployed only) |

`CODEFLY__RUNTIME_CONTEXT` environment variable controls which context is active. `NetworkAccessFromRuntimeContext()` maps context to the correct network access type.

`kubernetes` is written by the builder into every Kubernetes render's
ConfigMap (all output profiles), so a deployed service can tell it is deployed
without comparing environment names. It is not a run context: it is absent from
`RuntimeContexts()`, so `ValidateRuntimeContext` refuses it for a local run.
For configuration matching it folds onto `container`, as `nix` folds onto
`native`. The values a process can observe are therefore `native`, `nix`,
`container`, `free` (local runs, unchanged) and `kubernetes` (renders).

## Readiness

Each agent defines what "ready" means for its service. The orchestrator does not guess -- it calls the agent and the agent performs real health checks.

For a postgres agent, ready means: a real SQL query succeeds.
For a gRPC service, ready means: the gRPC health check endpoint responds.
For Temporal, ready means: the Temporal health RPC returns OK.

**Never use raw TCP connects for readiness.** A port being open does not mean the service is ready to accept requests.

## Dependency Graph

Services declare dependencies in `service.codefly.yaml`. The `architecture/` package computes a DAG across services and modules. The CLI starts services in dependency order: infrastructure first (postgres, temporal), then application services.

```
workspace.codefly.yaml
├── module: backend/
│   ├── api-server (depends on: store/postgres)
│   └── worker (depends on: store/postgres, temporal)
└── module: store/
    └── postgres
```

Start order: `postgres → api-server, worker` (parallel where possible).

A dependency can declare a `kind` saying which stage it constrains — a build
input, a consumed runtime endpoint, a one-shot completion prerequisite, a schema
contribution or an external capability. See [dependency-kinds.md](dependency-kinds.md).

A dependency can also require an **interface** — a named, versioned contract a
module implements — instead of naming a service; the workspace binds it to the
one provider in scope. See [interfaces.md](interfaces.md).

### The module list is a pin set, not a graph

Composition namespaces expose generic cache, build and runtime roots. Generators
receive the build root as `CODEFLY_COMPOSITION_BUILD` and `namespace.buildDir` in
the composition input. Each agent or generator owns its subdirectories; Core
does not choose framework build paths or create framework-specific directories.
The namespace, module and locked composition identity isolate these roots.

Projection activation retains immutable revisions for existing readers. On
Darwin it also retains superseded `.projection-link-*` symlinks: unlinking a
symlink during pathname resolution can fail a concurrent read with `EINVAL`.
These links and revisions have the same lifetime as the composition namespace;
do not sweep them while its consumers are running.

`workspace.codefly.yaml`'s `modules:` answers where a module named X comes from —
source, version, checkout location. It does not say which modules take part in a
given run: that is derived by `Workspace.ResolveModuleClosure`, which starts from
the modules being run and follows the dependencies services already declare.

The closure is per-`Stage`, because a dependency only pulls a module in for the
stages its kind constrains: building a service needs its codegen inputs and not
the endpoints it will later consume, and running it needs the reverse. A
`kind: external` dependency constrains no stage and so pulls in nothing.

A pinned module nothing reaches is simply not in the closure, so the pin set is a
superset of any one stage. A module a declaration reaches but the pin set does not
cover is an error naming the declaring service, rather than a run that comes up
with no endpoints. `ModuleClosure.ValidateServiceDependencies` applies the
workspace-wide visibility rules to exactly that set — and to exactly the
dependencies that put them there, so an edge is judged by the stage that
traverses it rather than by whichever modules happen to be loaded beside it. It
shares its implementation with `Workspace.ValidateServiceDependencies`, which
has no stage to scope to and so judges every declared edge.

The same scoping rule governs the graph in `architecture`. A view restricted
with `ServiceDependencies.ForStage` judges the edges that constrain that stage
and an unrestricted one judges them all, and `Closure.Verify` takes the phase
the plan is for — the closure walk follows every declared edge, so judging them
all would make a build input's verdict depend on whether the walk happened to
reach its consumer.
