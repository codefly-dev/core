# How to Write an Agent

An agent is a gRPC server process that implements codefly's development API for a specific service type. When the CLI needs to run, build, test, or scaffold a service, it spawns the agent binary, connects over gRPC, and calls the appropriate methods.

## The 4 Interfaces

An agent can implement up to 4 gRPC interfaces:

### 1. Agent — Identity

Every agent has an identity defined in `agent.codefly.yaml`:

```yaml
name: go-grpc
version: 0.1.0
publisher: codefly.dev
```

### 2. Runtime — Run & Test

```protobuf
service Runtime {
  rpc Load(LoadRequest)       returns (LoadResponse);     // Read config, declare endpoints
  rpc Init(InitRequest)       returns (InitResponse);     // Compile, resolve deps, accept port mappings
  rpc Start(StartRequest)     returns (StartResponse);    // Start the service process
  rpc Stop(StopRequest)       returns (StopResponse);     // Graceful shutdown
  rpc Destroy(DestroyRequest) returns (DestroyResponse);  // Full cleanup
  rpc Build(BuildRequest)     returns (BuildResponse);    // Dev compile check
  rpc Test(TestRequest)       returns (TestResponse);     // Run tests
}
```

### 3. Builder — Scaffold, Build & Deploy

```protobuf
service Builder {
  rpc Load(LoadRequest)           returns (LoadResponse);       // Read config
  rpc Init(InitRequest)           returns (InitResponse);       // Prepare build context
  rpc Create(CreateRequest)       returns (CreateResponse);     // Scaffold new service
  rpc Update(UpdateRequest)       returns (UpdateResponse);     // Update existing service
  rpc Sync(SyncRequest)           returns (SyncResponse);       // Sync data
  rpc Build(BuildRequest)         returns (BuildResponse);      // Compile/package
  rpc Deploy(DeploymentRequest)   returns (DeploymentResponse); // Ship to environment
  rpc Communicate(stream Answer)  returns (stream Question);    // Interactive Q&A
}
```

### 4. Code — Analysis (optional)

For agents that support code intelligence: `ListSymbols()`, `GetSymbol()`.

## Step-by-Step Guide

### Directory Structure

```
agents/services/go-grpc/
├── agent.codefly.yaml      # Agent identity
├── go.mod
├── main.go                 # Entry point: register + serve
├── runtime.go              # Runtime interface implementation
├── builder.go              # Builder interface implementation
└── testdata/               # Test fixtures
```

### main.go — Entry Point

```go
package main

import (
    "github.com/codefly-dev/core/agents"
)

func main() {
    // Create the agent with both Runtime and Builder implementations
    agent := &Agent{}
    agents.Serve(agent)
}
```

### The Agent Struct

```go
type Agent struct {
    // Embed the base types for unimplemented methods
    runtimev0.UnimplementedRuntimeServer
    builderv0.UnimplementedBuilderServer

    // Agent state
    identity  *basev0.ServiceIdentity
    endpoints []*basev0.Endpoint
    settings  *Settings
}
```

### Settings Pattern

Agent settings are Go structs stored in `service.codefly.yaml` under the agent's key:

```go
type Settings struct {
    GoVersion   string `yaml:"go-version"`
    WithGateway bool   `yaml:"with-gateway"`
}
```

```yaml
# service.codefly.yaml
name: my-api
agent: go-grpc
# Agent reads its settings from this file
go-version: "1.25"
with-gateway: true
```

### Runtime Implementation

```go
func (a *Agent) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
    // 1. Store identity
    a.identity = req.Identity

    // 2. Load service.codefly.yaml from the service directory
    svc, err := resources.LoadServiceFromDir(ctx, a.serviceDir())
    if err != nil {
        return nil, err
    }

    // 3. Load endpoints
    endpoints, err := svc.LoadEndpoints(ctx)
    if err != nil {
        return nil, err
    }

    // 4. Convert to proto endpoints
    var protoEndpoints []*basev0.Endpoint
    for _, ep := range endpoints {
        p, _ := ep.Proto()
        protoEndpoints = append(protoEndpoints, p)
    }
    a.endpoints = protoEndpoints

    return &runtimev0.LoadResponse{
        Endpoints: protoEndpoints,
    }, nil
}

func (a *Agent) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
    // 1. Receive network mappings (ports allocated by the CLI)
    a.networkMappings = req.ProposedNetworkMappings

    // 2. Find the port assigned to our gRPC endpoint
    instance, err := resources.FindNetworkInstanceInNetworkMappings(ctx,
        a.networkMappings,
        a.grpcEndpoint,
        resources.NewNativeNetworkAccess(),
    )
    if err != nil {
        return nil, err
    }
    a.port = uint16(instance.Port)

    // 3. Compile if needed
    // ...

    return &runtimev0.InitResponse{
        // Pass accepted mappings and any runtime configs back
    }, nil
}

func (a *Agent) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
    // 1. Start the service process
    cmd := exec.Command("go", "run", ".", "-port", fmt.Sprintf("%d", a.port))
    cmd.Dir = a.serviceDir()
    if err := cmd.Start(); err != nil {
        return nil, err
    }

    // 2. Wait for readiness (real health check, not TCP)
    if err := a.waitForReady(ctx); err != nil {
        return nil, err
    }

    return &runtimev0.StartResponse{}, nil
}
```

## Port Allocation

### Deterministic Ports (default)

Use `network.ToNamedPort()` for user-facing services. The port is a deterministic hash of workspace+module+service+endpoint+api, so it stays stable across restarts.

```go
port := network.ToNamedPort(ctx, workspace, module, service, endpointName, api)
// Same inputs → same port, always
```

This matters because users connect tools (pgAdmin, DataGrip, browsers) to these ports. Changing ports breaks their workflow.

### Ephemeral Ports (tests)

Use `RuntimeManager.WithTemporaryPorts()` for test environments where stable ports do not matter and parallel tests need isolated ports.

```go
mgr, _ := network.NewRuntimeManager(ctx, nil)
mgr.WithTemporaryPorts() // random starting port, dedup tracking
```

### Port Collision Prevention

The `RuntimeManager` tracks all allocated ports in `allocatedPorts map[uint16]string`. If a port is already allocated to a different service, `GenerateNetworkMappings` returns an error. Always use the manager -- never allocate ports manually.

## Readiness

Each agent owns its readiness definition. Implement `waitForReady` with real health checks:

```go
func (a *Agent) waitForReady(ctx context.Context) error {
    // For gRPC services: use gRPC health check
    // For databases: run a real query
    // For HTTP: hit a health endpoint
    // NEVER just check if the TCP port is open

    deadline := time.Now().Add(30 * time.Second)
    for time.Now().Before(deadline) {
        conn, err := grpc.Dial(addr, grpc.WithInsecure())
        if err == nil {
            healthClient := grpc_health_v1.NewHealthClient(conn)
            resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
            if err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING {
                return nil
            }
        }
        time.Sleep(500 * time.Millisecond)
    }
    return fmt.Errorf("service not ready after 30s")
}
```

## How Agents Receive Data

### Endpoints

Declared in `service.codefly.yaml`, loaded by the agent in `Load()`, returned as proto `Endpoint` objects to the CLI.

### Network Mappings

The CLI allocates ports and sends `NetworkMapping` objects to the agent in `Init()`. Each mapping contains multiple `NetworkInstance` entries (native, container, public). The agent picks the one matching its runtime context.

### Dependency Configs

If your service depends on postgres, the CLI starts postgres first, extracts its configuration (connection string, port), and injects it as environment variables before calling your agent's `Init()`.

## Common Pitfalls

1. **TCP-only readiness.** A port being open does not mean the service is ready. Always use application-level health checks.
2. **Port collisions.** Never hardcode ports. Never allocate ports without going through `RuntimeManager`. Parallel tests with the same deterministic ports will collide -- use `WithTemporaryPorts()`.
3. **Missing cleanup.** Always implement `Stop()` and `Destroy()` properly. Kill child processes, remove temp files, release ports.
4. **Forgetting to return endpoints from Load().** The CLI needs the endpoint list to allocate ports. If `Load()` returns empty endpoints, nothing works downstream.
5. **Ignoring the network mapping from Init().** Do not compute your own port. Use the port from `ProposedNetworkMappings` -- the CLI has already ensured it is free and tracked.

## Reference: go-grpc Agent

The `go-grpc` agent is the gold standard implementation. Study it for patterns around:
- Proto compilation via the proto companion
- Gateway generation (gRPC → REST)
- Hot reload via file watching
- Test execution with `go test`
