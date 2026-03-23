# Network & Configuration Model

## The Problem

Connection strings change across environments:

| Environment | Postgres address |
|---|---|
| Local (native) | `localhost:5432` |
| Local (Docker) | `host.docker.internal:5432` |
| k8s (same namespace) | `postgres.default.svc:5432` |
| Production | `db.prod.internal:5432` |

Every service needs the right address for its runtime context. Hardcoding any of these means your code only works in one environment.

## The Solution: NetworkMapping

Every endpoint gets a **NetworkMapping** containing multiple **NetworkInstances** -- one per access type:

```go
mapping := &basev0.NetworkMapping{
    Endpoint: grpcEndpoint,  // the logical endpoint
    Instances: []*basev0.NetworkInstance{
        Native(endpoint, port),    // localhost:PORT
        Container(endpoint, port), // host.docker.internal:PORT
        Public(endpoint, port),    // configurable hostname:PORT
    },
}
```

At runtime, the consumer filters for the access type matching its context:

```go
access := resources.NewNativeNetworkAccess() // or Container, Public
instance := resources.FilterNetworkInstance(ctx, mapping.Instances, access)
// instance.Address = "localhost:34523"
```

### Access Types

| Type | Hostname | When to use |
|---|---|---|
| `native` | `localhost` | Process runs directly on host |
| `container` | `host.docker.internal` | Process runs in Docker, connecting to host |
| `public` | configurable | Production, external access |

## Deterministic Port Hashing

Port allocation uses SHA-256 hashing of the service identity:

```go
func ToNamedPort(ctx, workspace, module, service, endpointName, api string) uint16 {
    combined := strings.Join([]string{workspace, module, service, endpointName}, "-")
    hash := sha256.Sum256([]byte(combined))
    num := binary.BigEndian.Uint64(hash[:8])
    basePort := 1024 + (num % 64502)
    basePort = basePort - (basePort % 10)  // clear last digit
    return uint16(basePort) + uint16(APIInt(api))  // last digit = API type
}
```

The last digit encodes the API type:
- `0` = TCP
- `1` = HTTP
- `2` = REST
- `3` = gRPC

### Why Stable Ports Matter

Users connect external tools to services: pgAdmin to postgres, DataGrip to databases, browsers to frontends. If ports change on every restart, users reconfigure tools constantly. Deterministic hashing means:

- Same workspace + module + service + endpoint → same port, always
- Restart the service → same port
- Restart the machine → same port
- Different developer, same workspace config → same port

### Ephemeral Ports

For tests and CI where stability does not matter and parallel isolation does:

```go
mgr, _ := network.NewRuntimeManager(ctx, nil)
mgr.WithTemporaryPorts()
// Uses GetFreePort() with dedup tracking
// Random start between 20000-40000 to avoid parallel test collisions
```

`GetFreePort()` tries each port sequentially, checks both the internal allocation map and actual TCP availability via `net.Listen`, preventing collisions between parallel tests.

## Configuration Flow

### Producer Side

A service agent produces configuration during `Init()`. For example, an `external-postgres` agent produces:

```go
// In InitResponse
configs := []*basev0.Configuration{
    {
        Origin: "store/postgres",
        Infos: []*basev0.ConfigurationInformation{
            {
                Name: "connection",
                ConfigurationValues: []*basev0.ConfigurationValue{
                    {Key: "url", Value: "postgresql://localhost:5432/mydb"},
                    {Key: "password", Value: "secret", Secret: true},
                },
            },
        },
    },
}
```

### Consumer Side

The CLI resolves the dependency graph, collects configs from all upstream services, and injects them as environment variables:

```
CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL=postgresql://localhost:5432/mydb
CODEFLY__SERVICE_SECRET_CONFIGURATION__STORE__POSTGRES__CONNECTION__PASSWORD=secret
```

Pattern:
```
CODEFLY__SERVICE_CONFIGURATION__{MODULE}__{SERVICE}__{INFO_NAME}__{KEY}=value
CODEFLY__SERVICE_SECRET_CONFIGURATION__{MODULE}__{SERVICE}__{INFO_NAME}__{KEY}=value
```

### Workspace-Level Configuration

Configuration can also come from the workspace level:

```
CODEFLY__WORKSPACE_CONFIGURATION__{NAME}__{KEY}=value
```

### Reading Configuration in Code

From any language, read the environment variable:

```go
// Go
url := os.Getenv("CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL")
```

```python
# Python
url = os.environ["CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL"]
```

```typescript
// TypeScript
const url = process.env.CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL;
```

Or use the SDK helper:

```go
env, _ := sdk.WithDependencies(ctx)
url := env.Connection("postgres", "connection")
```

### Secret Handling

Configuration values with `Secret: true` get:
- A separate env var prefix (`SECRET_CONFIGURATION` instead of `CONFIGURATION`)
- Never logged by the system
- Same injection mechanism, just a different namespace

## Network Mapping for Endpoints

The full flow from service declaration to usable connection:

```
1. service.codefly.yaml declares endpoints:
   endpoints:
     - name: grpc
       api: grpc

2. Agent Load() reads endpoints, returns them as proto objects

3. RuntimeManager.GenerateNetworkMappings() allocates ports:
   - Deterministic port via ToNamedPort()
   - Creates Native + Container instances (+ Public if visibility=public)

4. CLI passes NetworkMappings to agent in Init()

5. Agent binds to the assigned port

6. Dependent services receive the mapping via their own Init()
   - Filter by access type matching their runtime context
   - Get the correct address for their environment
```

### Endpoint Visibility

| Visibility | Who can access | Network instances generated |
|---|---|---|
| `private` | Same module only | Native + Container |
| `module` | Cross-module within workspace | Native + Container |
| `public` | External access | Native + Container + Public |
| `external` | Endpoint exists outside the system | DNS-resolved instances |

### External Endpoints

For services that exist outside codefly (e.g., a managed cloud database), the `DNSManager` resolves the actual hostname and port:

```go
dns, _ := dnsManager.GetDNS(ctx, serviceIdentity, "grpc")
// dns.Host = "api.prod.example.com", dns.Port = 443, dns.Secured = true
instance := DNS(serviceIdentity, endpoint, dns)
// instance.Address = "https://api.prod.example.com:443"
```

## Example: Postgres Connection Across Contexts

```yaml
# service.codefly.yaml for the API server
service-dependencies:
  - name: store/postgres
```

When the CLI starts:

1. Starts `external-postgres` agent → Docker container on deterministic port (e.g., 23450)
2. Generates NetworkMapping:
   - Native: `localhost:23450`
   - Container: `host.docker.internal:23450`
3. Starts `api-server` agent, passes postgres NetworkMapping
4. Sets env var: `CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL=postgresql://localhost:23450/postgres`
5. API server reads env var, connects to postgres

If the API server runs in Docker instead of natively, the CLI detects `CODEFLY__RUNTIME_CONTEXT=container` and the env var becomes:
`postgresql://host.docker.internal:23450/postgres`

Same code. Same agent. Different context. Correct address.
