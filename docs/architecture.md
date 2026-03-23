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

## Resource Hierarchy

```
Workspace → Module → Service → Endpoint
     │          │         │         │
     │          │         │         └── API type (gRPC, REST, HTTP, TCP)
     │          │         └── Managed by an Agent (go-grpc, external-postgres, etc.)
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
- `external-postgres` — Managed Postgres (Docker for local, cloud for prod)
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

## Configuration Flow

1. Service A produces configuration (e.g., postgres connection string)
2. Service B declares dependency on A in `service.codefly.yaml`
3. CLI resolves the dependency graph and injects configs as environment variables
4. Pattern: `CODEFLY__SERVICE_CONFIGURATION__MODULE__SERVICE__NAME__KEY=value`
5. Secrets get a separate prefix: `CODEFLY__SERVICE_SECRET_CONFIGURATION__...`

Configuration values carry a `Secret` flag. The system never logs secret values and uses the separate env var prefix for them.

## Runtime Contexts

The same code runs in four contexts:

| Context | Network Access | Description |
|---|---|---|
| `native` | `native` | Process runs directly on host |
| `nix` | `native` | Process runs in Nix shell on host |
| `container` | `container` | Process runs inside Docker |
| `free` | varies | No assumptions (testing) |

`CODEFLY__RUNTIME_CONTEXT` environment variable controls which context is active. `NetworkAccessFromRuntimeContext()` maps context to the correct network access type.

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
