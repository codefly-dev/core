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
// Uses AllocateTemporaryPort(ctx) with dedup tracking
// The kernel picks the port, so this manager never hands out the same one twice
```

`AllocateTemporaryPort(ctx)` binds an ephemeral loopback port through the kernel and records it in the manager's allocation map before releasing the probe listener. That makes allocations unique **within this manager** — it does not make them unique across processes; see the cross-process caveat below.

Allocation is bounded rather than best-effort. A host that cannot bind loopback at all fails on the first attempt with `ErrTemporaryPortUnsupported`; a full descriptor table, a reservation collision, or a probe-close failure retries a fixed number of times with a backoff and then fails with `ErrTemporaryPortUnavailable` wrapping the last cause. The two are separated so a caller can tell a host it should retry from one it must reconfigure. Cancellation is checked before every attempt, so a cancelled context returns promptly. `ReleasePort` hands a reservation back. Callers propagate the failure — `GenerateNetworkMappings` aborts the mapping rather than handing back a port it never reserved.

Closing the probe listener does **not** reserve the port against other processes. The reservation is in-process only, so two CLIs running in parallel *can* be handed the same port: the first releases its probe, and the kernel is free to offer that port to the second before the first service binds it. Temporary ports remove collisions between endpoints of one run, not between concurrent runs — isolate concurrent runs at the container or netns boundary. Cross-process listener ownership is tracked separately.

## Configuration Flow

### Producer Side

A service agent produces configuration during `Init()`. For example, an `postgres` agent produces:

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

Visibility answers *who may reach this endpoint*. It is one axis; **location**
(below) is a separate one.

| Visibility | Who can access | Network instances generated |
|---|---|---|
| `private` | Same module only | Native + Container |
| `internal` | The modules listed in `allow-modules` (`*` = all) | Native + Container |
| `public` | Outside the workspace | Native + Container + Public |

`internal` carries an explicit allow-list, so least privilege is expressible:

```yaml
endpoints:
  - name: http
    api: http
    visibility: internal
    allow-modules: [platform]   # only the platform module may reach this
```

A dependency onto an endpoint whose visibility does not permit the consuming
service's module is a hard error — the declaration and any NetworkPolicy
generated from the same topology cannot drift. Every path that asks reaches the
same verdict, because they all resolve through
`resources.ConsumedDependencyEndpoints`: the static workspace pass, the stage
closure, `architecture.ServiceDependencies.VerifyVisibility` at `verify` time,
the plan `Closure.Verify` gates, and the addresses a run injects.

A dependency that *names* endpoints consumes exactly those, and naming one the
producer does not grant fails naming that endpoint. A dependency that names none
consumes "all", which means all it is permitted: the rest are dropped, so a
producer adding one private endpoint does not break every consumer that did not
enumerate. Being permitted none of them is still an error — the edge is declared
and the export boundary grants nothing for it.

**Only these three spellings load.** A visibility the model does not define —
including the former `module` (every module) and `external` (a location written
as a permission) — is refused when the manifest is read, as an invalid
declaration, never read as private by one path and denied by another. `module`
is written as `visibility: internal` with `allow-modules: ["*"]`; `external` is
written as `location: external` (see below) beside the visibility that applies.
An `allow-modules` list is only read for `internal` and is refused elsewhere.

A module's interface entry is the whole export declaration: it names the
visibility the module grants across its boundary — `public`, or `internal`
(the default) with its own `allow-modules` — and the service's own visibility
and allow-list are not consulted once the module has spoken. An `internal`
entry that names no module is refused: it would export to nobody.

### Endpoint References

A configuration value may name an endpoint instead of typing its address:
`${endpoint:<module>/<service>/<endpoint>}` (with `|authority` for host:port, and
`::<api>` to qualify the name with the API it must serve). The reference is
resolved to the address the run published for the consuming service's access —
and *which* endpoint it names is decided once, in core, by the same rule for the
plan-time check, the readiness graph and the value's resolution
(`resources.SelectEndpointForReference`). No caller models the selection beside
it; a consumer of this package that ordered or pruned the mappings it handed
over to influence the answer was the defect this rule removed.

The rule, per reference, for the consumer's module:

1. **The exact name wins, and is found first.** An endpoint whose *name* is the
   reference's token is the endpoint named, before any matching by API. A
   producer declaring `grpc` (api grpc) and `admin` (api grpc) has two endpoints
   that *serve* grpc; `${…/grpc}` names the one called `grpc`. Nothing else may
   answer it: not a sibling published earlier, not a sibling that has an address
   for this access when the named one does not, not a sibling when the reference
   qualifies the name with an API the named endpoint does not serve (that is a
   refusal, never a substitution).
2. **An exact name the consumer may not reach is refused, never replaced.** The
   reference is an edge into the consumer's module like a declared dependency,
   judged by the same export boundary. Resolving to a permitted sibling instead
   would answer a reference for an endpoint the consumer was refused with another
   endpoint's address, silently.
3. **With no exact name, the token is an API**, and the candidates are the
   endpoints serving it that the consumer may reach. Exactly one is the answer;
   several are ambiguous and refused (name the one you mean); none is a refusal
   carrying the visibility reason when an endpoint existed but was not
   reachable.

Two inputs are required for any resolution, and a value carrying a reference is
refused when either is missing: the **consumer's module**, because visibility is
a statement about it, and the **producers' declared endpoints** (the manifest),
because published mappings establish neither the complete declared set nor a
declaration that was never published, and carry no authority on who may reach
what. A producer the workspace does not declare is a composition fault; a
declared endpoint the run published no mapping for is an availability fact.

Once selected, only that endpoint's mappings are bound, in publication order,
by name; a mapping published under that name that states another API is
conflicting metadata and refused. Duplicate mappings of one endpoint are one
candidate.

Omission versus refusal: a configuration injected run-wide reaches every
service, so a value whose endpoint this consumer was handed no mapping for, or
may not reach under a valid export policy, is **omitted** for that consumer (the
value was not for it). Those are the only two omissions. Every other failure —
an ambiguous reference, a malformed one, a qualifier the named endpoint does not
serve, a producer the workspace does not declare, an invalid declaration (an
unsupported visibility value), an endpoint with no instance for the consumer's
access — is **refused** with the configuration and key named, never a key
silently missing from a delivered configuration. The refusal wins whatever the
order of the references in a value: every reference of a value and of its
template literals is classified before an omission is allowed, so a fault
behind an omittable reference is still reported.

### Location

Location answers *where the endpoint lives*, independently of who may reach it:

| Location | Meaning | Network instances generated |
|---|---|---|
| (unset) | In-system, port-allocated | Native + Container (+ Public if public) |
| `external` | Managed resource outside the system | DNS-resolved instances |

Because the two axes are independent, a managed database can be both
`location: external` and `visibility: internal` with an allow-list.

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

1. Starts `postgres` agent → Docker container on deterministic port (e.g., 23450)
2. Generates NetworkMapping:
   - Native: `localhost:23450`
   - Container: `host.docker.internal:23450`
3. Starts `api-server` agent, passes postgres NetworkMapping
4. Sets env var: `CODEFLY__SERVICE_CONFIGURATION__STORE__POSTGRES__CONNECTION__URL=postgresql://localhost:23450/postgres`
5. API server reads env var, connects to postgres

If the API server runs in Docker instead of natively, the CLI detects `CODEFLY__RUNTIME_CONTEXT=container` and the env var becomes:
`postgresql://host.docker.internal:23450/postgres`

Same code. Same agent. Different context. Correct address.
