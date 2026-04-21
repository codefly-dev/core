# Testing Philosophy & Patterns

## Core Principle: NO MOCKS, EVER

Codefly tests run against real infrastructure. No in-memory fakes, no mock databases, no simulated services. If your service uses postgres, your test starts a real postgres. If it calls an LLM, the first test run makes a real API call and records it.

Why: mocks drift from reality. A mock postgres does not enforce constraints, does not have connection pooling bugs, does not have the same query planner. Testing against mocks gives false confidence.

## WithDependencies: The Universal Test Pattern

The SDK provides `WithDependencies()` which reads `service.codefly.yaml`, resolves the dependency graph, starts all required infrastructure via codefly agents, and injects connection strings as environment variables.

```go
func TestAPIServer(t *testing.T) {
    ctx := context.Background()

    // Start all dependencies declared in service.codefly.yaml
    env, err := sdk.WithDependencies(ctx)
    require.NoError(t, err)
    defer env.Stop(ctx)

    // Connection strings are now available
    pgURL := env.Connection("postgres", "connection")
    // or: pgURL := os.Getenv("CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL")

    // Test against real infrastructure
    db, err := sql.Open("postgres", pgURL)
    require.NoError(t, err)
    // ...
}
```

### Why It Uses the CLI Binary

`WithDependencies` delegates to the `codefly` CLI binary via `exec.Command("codefly", "run", "service", ...)`. This is deliberate:

1. **Language-agnostic.** The same `codefly` binary works from Go, Python, Rust, TypeScript. Any language SDK just shells out to the same CLI.
2. **Full graph resolution.** The CLI resolves arbitrarily deep dependency trees, not just direct dependencies.
3. **Port management.** The CLI handles port allocation, collision detection, and network mapping.
4. **Real lifecycle.** Agent processes go through the full Load → Init → Start lifecycle, exactly like production.

### The Standalone SDK Pattern

For simpler cases or when you want more control, use `sdk.New()` directly:

```go
func TestWithPostgres(t *testing.T) {
    ctx := context.Background()

    env := sdk.New()
    env.Add("postgres")
    require.NoError(t, env.Start(ctx))
    defer env.Stop(ctx)

    pgURL := env.Connection("postgres", "connection")
    // Test against real postgres
}
```

This starts individual agents without the full CLI dependency resolution. Useful for agent development and isolated infrastructure tests.

## Agent Lifecycle Testing

When testing an agent itself, exercise the full lifecycle:

```go
func TestGoGRPCAgent(t *testing.T) {
    ctx := context.Background()

    // 1. Load the agent binary
    agent, _ := resources.ParseAgent(ctx, resources.ServiceAgent, "go-grpc:latest")
    manager.FindLocalLatest(ctx, agent)
    conn, _ := manager.Load(ctx, agent)
    defer conn.Close()

    runtime := runtimev0.NewRuntimeClient(conn.GRPCConn())

    // 2. Load
    loadResp, err := runtime.Load(ctx, &runtimev0.LoadRequest{
        Identity: &basev0.ServiceIdentity{
            Name:      "test-svc",
            Module:    "mod",
            Workspace: "ws",
        },
    })
    require.NoError(t, err)
    require.NotEmpty(t, loadResp.Endpoints)

    // 3. Generate network mappings with temporary ports
    mgr, _ := network.NewRuntimeManager(ctx, nil)
    mgr.WithTemporaryPorts()
    mappings, _ := mgr.GenerateNetworkMappings(ctx, env, workspace, svcIdentity, loadResp.Endpoints)

    // 4. Init
    _, err = runtime.Init(ctx, &runtimev0.InitRequest{
        ProposedNetworkMappings: mappings,
    })
    require.NoError(t, err)

    // 5. Start
    _, err = runtime.Start(ctx, &runtimev0.StartRequest{})
    require.NoError(t, err)

    // 6. Test the running service
    // ...

    // 7. Stop + Destroy
    runtime.Stop(ctx, &runtimev0.StopRequest{})
    runtime.Destroy(ctx, &runtimev0.DestroyRequest{})
}
```

## Port Allocation in Tests

Always use `RuntimeManager` with temporary ports for tests:

```go
mgr, _ := network.NewRuntimeManager(ctx, nil)
mgr.WithTemporaryPorts()  // random ports, dedup tracked
```

This avoids:
- Collisions with services running on the developer's machine
- Collisions between parallel test runs
- Conflicts with deterministic ports used by `codefly run`

The manager picks a random starting port between 20000-40000 and increments, checking both its internal map and actual TCP availability.

## Readiness in Tests

Agents handle their own readiness. When `Start()` returns, the service is ready. Do not add `time.Sleep()` calls after starting services -- if the service is not ready when `Start()` returns, that is a bug in the agent.

## Cassette Pattern (from mind-server)

For external API calls (LLMs, embeddings, web APIs), use the cassette/recorder pattern:

```bash
# First run: makes real API calls, records responses
MIND_RECORD=1 go test ./pkg/chat/ -v

# All subsequent runs: replay from disk, zero API calls
go test ./pkg/chat/ -v
```

Cassette files are:
- **Git-tracked** -- committed to the repo
- **Human-readable** -- JSON with prompt preview, provider, model, sequence number
- **Deterministic** -- same hash = same response, forever
- **Replay-only by default** -- missing recording = test error, not silent API call

This pattern applies to any external API: LLM providers, embedding services, web search, webhook endpoints.

## Test Data

Use `testdata/` directories with real YAML fixtures:

```
pkg/mypackage/
├── mypackage.go
├── mypackage_test.go
└── testdata/
    ├── workspace.codefly.yaml
    ├── module.codefly.yaml
    └── service.codefly.yaml
```

Load fixtures with the standard resource loaders:

```go
ws, _ := resources.LoadWorkspaceFromDir(ctx, "testdata")
svc, _ := resources.LoadServiceFromDir(ctx, "testdata")
endpoints, _ := svc.LoadEndpoints(ctx)
```

## What Good Tests Look Like

1. **Start real infrastructure.** `sdk.WithDependencies(ctx)` or `sdk.New().Add("postgres").Start(ctx)`.
2. **Exercise real code paths.** No mocks, no fakes, no stubs.
3. **Assert real outcomes.** Query the real database, check real gRPC responses, verify real state.
4. **Clean up.** `defer env.Stop(ctx)` or `defer env.Destroy(ctx)`.
5. **Deterministic.** Same test, same result. Use cassettes for non-deterministic external APIs.
6. **Fast when replaying.** Cassette tests run in milliseconds on subsequent runs.
