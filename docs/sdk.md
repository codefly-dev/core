# SDK Reference

The SDK provides language-agnostic dependency management for development and testing. It starts real infrastructure via codefly agents and injects connection strings as environment variables.

## Two Entry Points

### 1. `sdk.WithDependencies()` — CLI-based (recommended)

Reads `service.codefly.yaml` from the current directory, resolves the full dependency graph, and starts everything via the `codefly` CLI binary.

```go
import "github.com/codefly-dev/core/sdk"

func TestMyService(t *testing.T) {
    ctx := context.Background()

    env, err := sdk.WithDependencies(ctx)
    require.NoError(t, err)
    defer env.Stop(ctx)

    pgURL := env.Connection("postgres", "connection")
    // pgURL = "postgresql://localhost:23450/postgres"
}
```

**How it works internally:**

1. Runs `codefly run service --exclude-root --cli-server` as a subprocess
2. Connects to the CLI's gRPC server on port 10000
3. Waits for all services to be ready (`GetFlowStatus`)
4. Extracts network mappings and configurations from the CLI
5. Sets environment variables for the calling process

**Why it uses the CLI binary:** This creates a universal integration testing pattern. The same `codefly` binary can be called from Go, Python, Rust, TypeScript, or any language. No language-specific dependency management code needed.

### 2. `sdk.New()` — Direct agent management

For simpler cases, agent development, or when you do not have a `service.codefly.yaml`:

```go
import "github.com/codefly-dev/core/sdk"

func TestWithInfra(t *testing.T) {
    ctx := context.Background()

    env := sdk.New()
    env.Add("postgres")
    env.Add("external-temporal")
    require.NoError(t, env.Start(ctx))
    defer env.Stop(ctx)

    pgURL := env.Connection("postgres", "connection")
    temporalAddr := env.Connection("external-temporal", "connection")
}
```

**How it works internally:**

1. For each agent: parse agent identity, find latest local binary
2. Spawn agent process, connect via gRPC
3. Call `Builder.Load() → Builder.Create()` (scaffolds temp service dir)
4. Call `Runtime.Load() → Runtime.Init() → Runtime.Start()`
5. Extract configuration from `InitResponse`
6. Store connection strings in the `Env.configs` map

You can also load dependencies from a service file:

```go
env := sdk.New()
env.Load("./path/to/service/dir")  // reads service.codefly.yaml
env.Start(ctx)
```

## Options

### WithDependencies Options

```go
// Enable debug logging
env, _ := sdk.WithDependencies(ctx, sdk.Debug())

// Custom timeout (default: 10s)
env, _ := sdk.WithDependencies(ctx, sdk.Timeout(30*time.Second))
```

The underlying CLI also supports:

```go
import "github.com/codefly-dev/core/cli"

deps, _ := cli.WithDependencies(ctx,
    cli.WithDebug(),
    cli.WithTimeout(30*time.Second),
    cli.WithNamingScope("test-1"),    // port namespace isolation
    cli.WithSilence("store/redis"),   // suppress logs for specific services
)
```

### NamingScope

When running parallel tests, use `WithNamingScope` to isolate port allocation:

```go
// Test A
cli.WithDependencies(ctx, cli.WithNamingScope("test-a"))
// Test B
cli.WithDependencies(ctx, cli.WithNamingScope("test-b"))
// Different naming scopes → different deterministic ports → no collisions
```

The scope is appended to the endpoint name before hashing, so `ToNamedPort(ws, mod, svc, "grpc-test-a", "grpc")` produces a different port than `ToNamedPort(ws, mod, svc, "grpc-test-b", "grpc")`.

## Retrieving Connection Strings

### From the SDK

```go
// WithDependencies pattern
env, _ := sdk.WithDependencies(ctx)
url := env.Connection("postgres", "connection")

// Direct pattern
env := sdk.New()
env.Add("postgres")
env.Start(ctx)
url := env.Connection("postgres", "connection")
```

### From Environment Variables

All connection strings are also available as environment variables:

```go
url := os.Getenv("CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL")
```

Pattern:
```
CODEFLY__SERVICE_CONFIGURATION__{MODULE}__{SERVICE}__{INFO_NAME}__{KEY}
CODEFLY__SERVICE_SECRET_CONFIGURATION__{MODULE}__{SERVICE}__{INFO_NAME}__{KEY}
```

The `Connection()` helper tries both patterns:
```
CODEFLY__SERVICE_{SERVICE}__{NAME}__CONNECTION
CODEFLY__{SERVICE}__{NAME}__CONNECTION
```

## Cleanup

Always clean up in tests:

```go
// WithDependencies
env, _ := sdk.WithDependencies(ctx)
defer env.Stop(ctx)  // calls cli.Destroy → kills all agent processes

// Direct
env := sdk.New()
env.Start(ctx)
defer env.Stop(ctx)  // calls Runtime.Destroy on each agent, removes temp dirs
```

`Stop()` on the direct SDK:
1. Calls `Runtime.Destroy()` on each running agent
2. Closes gRPC connections
3. Removes temporary directories

`Stop()` on the CLI-based SDK:
1. Calls `cli.DestroyFlow()` via gRPC
2. Kills the CLI subprocess

## Full Example: Go Test with Postgres + Temporal

```go
package myservice_test

import (
    "context"
    "database/sql"
    "testing"

    "github.com/codefly-dev/core/sdk"
    "github.com/stretchr/testify/require"
    _ "github.com/lib/pq"
)

func TestServiceIntegration(t *testing.T) {
    ctx := context.Background()

    // Start all infrastructure
    env := sdk.New()
    env.Add("postgres")
    env.Add("external-temporal")
    require.NoError(t, env.Start(ctx))
    defer env.Stop(ctx)

    // Get connection strings
    pgURL := env.Connection("postgres", "connection")
    require.NotEmpty(t, pgURL)

    temporalAddr := env.Connection("external-temporal", "connection")
    require.NotEmpty(t, temporalAddr)

    // Test against real postgres
    db, err := sql.Open("postgres", pgURL)
    require.NoError(t, err)
    defer db.Close()

    err = db.PingContext(ctx)
    require.NoError(t, err)

    // Create table, insert data, query — all against real postgres
    _, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS test (id serial PRIMARY KEY, name text)")
    require.NoError(t, err)

    _, err = db.ExecContext(ctx, "INSERT INTO test (name) VALUES ($1)", "codefly")
    require.NoError(t, err)

    var name string
    err = db.QueryRowContext(ctx, "SELECT name FROM test WHERE id = 1").Scan(&name)
    require.NoError(t, err)
    require.Equal(t, "codefly", name)
}
```

## Runtime Context

The SDK respects `CODEFLY__RUNTIME_CONTEXT` environment variable:

| Value | Network access | Description |
|---|---|---|
| `native` (default) | `localhost:PORT` | Process on host |
| `nix` | `localhost:PORT` | Process in Nix shell |
| `container` | `host.docker.internal:PORT` | Process in Docker |
| `free` | varies | No assumptions |

The correct network instance is selected automatically based on this context. You do not need to handle this in your test code.
