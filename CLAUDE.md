# CLAUDE.md — codefly/core

## Module & Repository

- **Module:** `github.com/codefly-dev/core`
- **Repository:** `https://github.com/codefly-dev/core`
- **Language:** Go 1.25

Core is the shared library for the entire codefly ecosystem. Every agent, the CLI, and user services depend on it. It defines the resource hierarchy, agent interfaces, network model, configuration system, proto-generated types, and test infrastructure.

## What Codefly Is

Running systems have formal APIs (REST, gRPC). But development operations — "run tests", "start dependencies", "build", "deploy", "scaffold a service", "generate protos" — have always been **implicit**, buried in scripts and READMEs.

Codefly formalizes development operations as **typed gRPC APIs**: `RuntimeService.Test()`, `BuilderService.Build()`, `BuilderService.Deploy()`, `CodeService.ListSymbols()`. Same API surface regardless of whether the service is Go, Python, or Rust — the agent plugin handles language-specific implementation. This makes development operations composable, discoverable, and automatable by both humans and AI agents.

## Architecture Overview

```
Workspace → Module → Service → Endpoint
     │          │         │         │
     │          │         │         └── API type (gRPC, REST, HTTP, TCP)
     │          │         └── Managed by an Agent (go-grpc, postgres, etc.)
     │          └── Logical grouping of services
     └── Root container (one per project)
```

Every resource is defined by a YAML file:
- `workspace.codefly.yaml` — root of the project
- `module.codefly.yaml` — inside each module directory
- `service.codefly.yaml` — inside each service directory, declares agent, endpoints, dependencies

## Package Hierarchy

### Resource Model
- **resources/** — All core types: Workspace, Module, Service, Endpoint, Agent, Organization, Environment. YAML loading, validation, DNS, network instances. This is the most important package.
- **architecture/** — Dependency graph computation across services and modules.
- **graph/** — Graph data structures used by architecture.

### Agent System
- **agents/** — Agent registration (`agents.Serve()`), manager (process lifecycle), communicate (interactive Q&A), helpers (Docker, code analysis), logger.
- **agents/services/** — Base server types that agents embed: `RuntimeServer`, `BuilderServer`, `CodeServer`. These implement the gRPC service interfaces.
- **agents/manager/** — Agent process management: spawn, connect, health check, shutdown.
- **agents/communicate/** — Interactive communication protocol between CLI and agents.

### Network
- **network/** — Port allocation, DNS management, network instance creation. Three access modes:
  - `Native()` — localhost access for local development
  - `Container()` — host.docker.internal for Docker-to-host communication
  - `PublicDefault()` — public-facing endpoints
- Port allocation uses deterministic hashing from workspace+module+service+endpoint+API names. The `RuntimeManager` tracks allocated ports to prevent collisions.

### Configuration
- **configurations/** — Configuration flow between services. `Manager` handles reading/writing configs. Services declare what configs they provide and consume. Configs are injected as environment variables at runtime.

### SDK & CLI Integration
- **sdk/** — Universal dependency pattern via CLI:
  - `sdk.WithDependencies(ctx)` — **the** way to start dependencies. Delegates to the CLI binary.
  - This is intentionally CLI-based, not Go-specific: the same `codefly` binary can be called from Python, Rust, TypeScript, or any language. This creates a **universal integration testing pattern** — any language SDK just shells out to the same CLI.
- **cli/** — `cli.WithDependencies()` — resolves service.codefly.yaml and starts agents in dependency order.

### Runners
- **runners/base/** — `Proc` interface for running external processes.
- **runners/golang/** — Go-specific runner: `go run`, `go build`, `go test` with environment setup.

### Companions
- **companions/** — Sidecar containers for language tooling:
  - **proto/** — Buf-based proto compilation companion.
  - **lsp/** — Language Server Protocol client for symbol extraction.
  - **golang/, python-poetry/, node/** — Language-specific build companions.
  - **scripts/** — `build_companions.sh` to build companion Docker images.

### Code Generation
- **generated/** — Proto-generated Go code. Structure: `generated/go/codefly/{base,services,actions,cli,mcp,observability}/v0/`
- **generation/** — Code generation utilities.
- **templates/** — Template engine for agent scaffolding.
- **openapi/** — OpenAPI spec generation from proto definitions.

### Standards & Languages
- **standards/** — API type constants: `TCP`, `HTTP`, `REST`, `GRPC`.
- **languages/** — Language detection and constants: `GO`, `PYTHON`, `TYPESCRIPT`, etc.

### Infrastructure
- **builders/** — Dependency tracking for agent builds (file watching, selective rebuild).
- **services/** — Service instance management, agent clearing.
- **shared/** — Utilities: embed helpers, file operations, `Must()`, `Select` patterns.
- **tui/** — Terminal UI components (bubbletea-based).
- **version/** — Version tracking.

### Test Support
- **testdata/** — Shared test fixtures with workspace/module/service YAML hierarchies.

## Build & Test

```bash
go test ./...                    # Run all tests
go test ./resources/ -v          # Test a specific package
go test ./companions/golang/ -run TestLSP  # LSP companion tests

# Coverage
make check-coverage

# Companion Docker images
./companions/scripts/build_companions.sh

# Proto regeneration (from core/)
# NEVER call buf/protoc directly — use codefly CLI or companion scripts
```

## Key Patterns

### Agent Lifecycle (gRPC)
Every agent implements four interfaces via gRPC:
1. **Agent** — `Load()` identity and settings
2. **Builder** — `Load() → Init() → Create() → Update() → Sync() → Build() → Deploy()`
3. **Runtime** — `Load() → Init() → Start() → Stop() → Destroy()` + `Information()`, `Test()`
4. **Code** — `ListSymbols()`, `GetSymbol()` — optional, for code analysis

### Network Mapping Flow
1. Agent declares endpoints in `service.codefly.yaml`
2. CLI loads endpoints → `resources.LoadEndpoints()`
3. Port allocated via `network.ToNamedPort()` (deterministic hash)
4. Network instances created: `network.Native()`, `network.Container()`, or `network.PublicDefault()`
5. Mappings distributed to dependent services as `basev0.NetworkMapping`

### Configuration Flow
1. Service A declares configs it provides
2. Service B declares dependency on A in `service.codefly.yaml`
3. CLI resolves the graph and passes configs as environment variables
4. Pattern: `CODEFLY__SERVICE_{SVC}__{ENDPOINT}__CONNECTION`

### Resource Loading
```go
ws, _ := resources.LoadWorkspaceFromDir(ctx, dir)
mod, _ := resources.LoadModuleFromDir(ctx, dir)
svc, _ := resources.LoadServiceFromDir(ctx, dir)
endpoints, _ := svc.LoadEndpoints(ctx)
grpcEP, _ := resources.FindGRPCEndpoint(ctx, endpoints)
```

## Key Environment Variables

- `CODEFLY__SERVICE_{NAME}__{ENDPOINT}__CONNECTION` — Injected connection strings
- `CODEFLY_DEBUG` — Enable debug logging
- `CODEFLY_SDK_EXTERNAL` — Use external (non-embedded) agent binaries

## Important Rules

- **NEVER mock.** Always test against real infrastructure. Use `testdata/` directories with real YAML fixtures.
- **NEVER call buf/protoc directly.** Proto generation goes through codefly CLI or the proto companion.
- **Port allocation must use `network.ToNamedPort()` or `RuntimeManager`.** Never hardcode ports. Always track allocated ports to prevent collisions (the temporal agent had a bug where duplicate ports were assigned because dedup tracking was missing).
- **Readiness checks must use gRPC health checks**, not raw TCP connects. A port being open does not mean the service is ready.
- **`resources/` is the source of truth** for all type definitions. When in doubt about how something is modeled, look there first.
- **Companion containers** are built separately and used at runtime. If a companion is broken, fix it — we own all of this.
