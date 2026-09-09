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

1. Mints an invocation identity for the session and creates a private `0700`
   directory to hold its control socket
2. Runs `codefly run service --exclude-root --cli-server` as a subprocess, with
   the socket path and the session credentials in the child's environment
3. Waits for the child to bind the socket, then makes it prove it owns the
   session (`SessionHandshake`)
4. Waits for all services to be ready (`GetFlowStatus`)
5. Extracts network mappings and configurations from the CLI
6. Sets environment variables for the calling process

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

// Keep a stable local stack warm across test runs.
env, _ := sdk.WithDependencies(ctx, sdk.WithKeepRunning(), sdk.WithNamingScope("dev"))

// Omit optional dependencies for this run.
env, _ := sdk.WithDependencies(ctx, sdk.WithExcludedDependencies("infra/temporal"))
```

`WithKeepRunning` is for local development loops. It first tries to attach to an
already-running CLI server started from the same plan. If none is available, it
starts one and releases it instead of destroying it during `Stop` / `Destroy`.
Use a stable naming scope, then clean test data explicitly between runs. See
[Session Isolation](#session-isolation) for what "the same plan" means.

`WithExcludedDependencies` removes optional services from the dependency graph
for the run. For example, tests that only need Postgres and Neo4j can exclude
`infra/temporal` while the Temporal teardown leak is being fixed.

Set `CODEFLY_BINARY=/path/to/codefly` when dogfooding a local CLI build before
installing it onto `PATH`.

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

## Session Isolation

Every default `WithDependencies` call is a **disposable session**. It mints an
invocation identity from `crypto/rand`, so nothing about the session is derived
from the workspace name:

| | control channel | naming scope |
|---|---|---|
| disposable (default) | Unix socket in a private `0700` directory owned by this process | `<your label>-s<invocation>` |
| reusable (`WithKeepRunning`) | Unix socket in a directory keyed by the reuse fingerprint | your label, unchanged |
| shared (`WithSharedControlChannel`) | TCP port hashed from the workspace name | your label, unchanged |

Two test packages in one workspace, or two worktrees checked out under the same
workspace name, therefore get separate control channels, state directories and
containers with no naming flags at all. Stopping one session does not touch the
other.

**Ownership.** The SDK creates the socket's directory, so only the SDK process
can put a socket there — the child cannot lose a bind race, and there is no
deterministic port for an unrelated server to occupy. On top of that, the child
must answer `SessionHandshake` by signing a fresh nonce with the per-invocation
secret it received through its private environment. The SDK verifies the proof
before it reads any environment, polls readiness, or sends `StopFlow` /
`DestroyFlow`. A server that cannot produce the proof is refused.

**Platform support.** Unix domain sockets, and therefore isolated sessions, are
available on Linux and macOS. Windows callers must pass
`WithSharedControlChannel()`.

**Contract for CLI implementers.** A control server started by the SDK reads:

| Variable | Meaning |
|---|---|
| `CODEFLY_CLI_SERVER_SOCKET` | absolute path the control server must bind instead of any TCP port |
| `CODEFLY_SESSION_ID` | invocation identity to echo in the handshake, and to carry into state paths, container labels and log roots |
| `CODEFLY_SESSION_SECRET` | HMAC key for the handshake proof — never log it or write it to persistent state |

`CODEFLY_CLI_SERVER_PORT` is removed from the child environment for isolated
sessions. The control server owns the socket file: remove any stale path before
listening, and remove it again on shutdown — the same contract `agents.Serve`
follows for `CODEFLY_AGENT_UDS_PATH`. Use
`github.com/codefly-dev/core/sdk/session` to read the session and compute the
proof rather than reimplementing it.

### Running against an older CLI

A CLI that ignores `CODEFLY_CLI_SERVER_SOCKET`, or that does not implement
`SessionHandshake`, is reported as an explicit error naming the missing
capability. The SDK never silently falls back to the shared workspace port
while claiming isolation. To drive such a CLI, opt out deliberately:

```go
env, _ := sdk.WithDependencies(ctx, sdk.WithSharedControlChannel())
```

That restores the pre-isolation behaviour — a workspace-hashed control port,
shared by every invocation of that workspace name, with no proof that the
server answering it is the child that was started.

### Reusable sessions

`WithKeepRunning` is explicit reuse mode, so its naming scope stays stable and
its containers survive between runs. Attaching to a warm stack requires more
than a matching workspace name: the SDK records a receipt next to the control
socket and reuses the stack only when the **reuse fingerprint** matches —
workspace, service and its declared dependencies, naming scope, fixture, run
profile, exclusions, silenced services, dependency home and CLI binary. A run
with a different fixture or profile starts its own stack instead of inheriting
one built from another plan.

### Borrowed sessions

When a parent Codefly runtime already injected live dependency endpoints, the
SDK reuses them instead of nesting a second flow. That session is *borrowed*:
`Stop` and `Destroy` leave the parent's stack running, and invocation-scoped
configuration overrides are rejected rather than silently ignored.

### NamingScope

`WithNamingScope` is a human label, not an isolation primitive — disposable
sessions are already unique without it. Use it to make a scope readable in
container names and logs, or to name a reusable stack:

```go
// Test A
cli.WithDependencies(ctx, cli.WithNamingScope("test-a"))
// Test B
cli.WithDependencies(ctx, cli.WithNamingScope("test-b"))
// Different naming scopes → different deterministic ports → no collisions
```

Under `sdk.WithDependencies` the effective scope is `test-a-s<invocation>`, so
two concurrent runs that both pass `test-a` still stay apart.

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
