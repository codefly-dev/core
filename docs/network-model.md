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
   - Creates Native + Container instances (+ Public if exposure: public)

4. CLI passes NetworkMappings to agent in Init()

5. Agent binds to the assigned port

6. Dependent services receive the mapping via their own Init()
   - Filter by access type matching their runtime context
   - Get the correct address for their environment
```

### Endpoint Visibility

Visibility answers *who may reach this endpoint*, and nothing else. It is one of
three axes an endpoint declares, each on its own and each read on its own:

| Axis | Question | Values |
|---|---|---|
| `visibility` | Who may reach it (**reach**) | `private`, `internal`, `public` |
| `location` | Where it lives | unset (in-system), `external` |
| `exposure` | Whether an address reachable from outside the workspace exists (**addressing**) | `none`, `public` — a public endpoint states one; unset means none on the others |

| Visibility | Who can reach | Names anybody? |
|---|---|---|
| `private` | The same module only | No |
| `internal` | Across modules, from within the workspace: whatever composes it | No |
| `public` | From outside the workspace | No |

The ladder is complete and no value names a module. `internal` is the whole of
what a host can honestly say about itself — *reachable by whatever composes me*
— and it stops at the workspace boundary, which no module of the composition is
on the far side of. So the static passes permit a cross-module dependency onto
an `internal` or a `public` endpoint and refuse one onto a `private` endpoint;
what `internal` denies is the outside, which no module ever asks core about.

```yaml
endpoints:
  - name: http
    api: http
    visibility: internal   # reachable by whatever composes this module; names nobody
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

**Visibility has no addressing consequence.** `public` used to do two jobs: it
permitted every module *and* allocated the Public network instance (the address
outside the workspace, and whatever a deployment renders from it). Those are
two independent facts, and an endpoint that every module should reach but that
must not be addressable from outside could not be declared. Addressing is now
[`exposure`](#exposure), beside the visibility, and **a public endpoint states
it** — `exposure: public` or `exposure: none` — rather than omitting it, so a
manifest written when `visibility: public` meant an address fails to load
(`exposure-declared`) instead of quietly losing it.

Both axes travel into delivery: the presence document carries `visibility` and
`exposure` per endpoint, judged by the same rules, so a platform standing up a
route reads the addressing from the document rather than inferring it from the
reach — see
[solution-host-binding.md](solution-host-binding.md#an-endpoints-reach-and-its-addressing-and-why-this-document-holds-both-to-cores-vocabulary).

### The allow-list is derived, never authored

Which modules actually reach an endpoint is **derived by the composition** from
the consumers' declared `service-dependencies`, by the one implementation in
core, `resources.Workspace.DeriveAllowModules`. A service declares what it
requires, in its own repository, as part of its own declaration; the
composition that performs the join (the CLI, at render — the only component
holding both the declarations and the environment) calls the derivation for the
allow-list it writes into the rendered artifact, and that derived list is what
the platform enforces.

The ask lives with the asker, never with the target. An `allow-modules` list
written on an endpoint, or on a module interface entry, is the target naming its
own consumers — a list that is unmaintainable at that layer (every new consumer
is an edit to the host's repository), inverted (the consumed names its
consumers), and forbidden by the boundary rules — so **a hand-authored
`allow-modules` is refused by presence when the manifest is read**: any spelling
(`allow-modules`, `allow_modules`, `allowModules`), any value (a module, the
wildcard `["*"]`, `[]`, `null`), judged on the mapping's keys before decoding,
because a decoder that has already run cannot tell `null` from absence and drops
a spelling it does not know. On the wire the reserved field numbers
(`Endpoint.allow_modules = 9`, `InterfaceEndpoint.allow_modules = 4`) are
refused by number before projection: protobuf keeps bytes for a field the
schema does not define as unknown data rather than refusing them, so every proto
ingress judges the declaration whole, unknown fields included. The former
`module` visibility is written as `visibility: internal` and nothing else.

What the derivation buys, beyond fixing the boundary:

- The grant stays explicit, enforceable and reviewable — in the rendered
  artifact, computed rather than hand-maintained.
- A grant cannot exist without a declared need, and a stale entry cannot survive
  the dependency being removed: the list is exactly the set of asks.
- Adding a consumer is a change in the consumer, and no edit crosses a
  repository boundary.

**What derives an entry is what the dependency model says, not stage
participation.** Only an edge that *reaches* the producer's endpoints at run
time derives one (`DependencyKind.ReachesEndpoints`: untyped and `runtime`); a
`build` or `schema` edge reads the producer's contract and never calls it, a
`completion` prerequisite waits for the producer to finish and consumes no
endpoint — it is a run-stage edge, and that is not consumption, so an omitted
endpoint list on it never reads as "all" — and an `external` edge has no
producer in the workspace. A list applies to an `internal` endpoint only:
`public` is open and `private` is closed, and neither carries one. The
producer's own module appears when one of its services asks.

**The join is over the composition, never over bare services, and every
declarer asks.** Every module of a composition carries its provenance
(`Workspace.Member`: the role it was declared in, `module` or `solution`, and
the workspace that declared it — inherited through composition, so a composed
workspace's own solutions stay solutions), and the provenance is **in the
signature of the one verdict** every reader consults
(`resources.ConsumedDependencyEndpoints` and what builds on it:
`PermittedDependencyEndpoints`, `ResolveDependencyNetworkMappings`, the
workspace and closure static passes, `architecture.VerifyVisibility`,
`Closure.Verify`, the plan, and the derivation). The verdict judges the edge by
the provenance of its two ends first (`resources.JudgeCompositionEdge`): **a
solution reaches modules only through the host**, so a solution's run-stage
edge onto a module's endpoint — of the composed platform or of the product's
own `modules:` — is refused (`resources.ErrSolutionReachesThroughHost`)
whatever the endpoint's visibility grants, on every path alike; a `build` or
`schema` edge reads the module's contract and is not that route, and an edge
between two solutions or from a module is judged by visibility alone. No
provenance, or an end the composition does not carry, is
`resources.ErrUnjudgedProvenance`. Every holder of a consumer's dependency
addresses judges with the composition it holds: core's CLI-side wrappers
(`services.RuntimeInstance.Init`/`Start` and `services.BuilderInstance.Deploy`,
with the instance's workspace, and refusing addresses handed to an instance
with no module or service to judge them for), the SDK dependency session (the
workspace that composed its module, else the one above its directory) and the
builder agent (the workspace above the service directory it was loaded from).
A holder that can find no composition refuses the addresses as unjudged rather
than wiring them, and there is no selecting from what another holder judged.
Every declarer of `service-dependencies` is judged and asks alike — a service,
a job, a runnable, an application (`Module.LoadDependencyDeclarers`) — so a
module's runnable that calls an internal endpoint gets its entry and a
solution's runnable gets its refusal. The derivation runs the workspace's own
static validation first, so nothing is derived for a composition that does not
validate: a cross-module ask for a private endpoint, a solution's route, a
dependency on an endpoint the producer does not declare, are refused there
exactly as the static pass refuses them.

**A static allow-list is reachability, not authorization.** The derived list can
only answer *may anything in module X reach this endpoint at all* — a
deployment-time fact enforced by mesh policy and NetworkPolicy. *Whether a
particular call is permitted* — on whose behalf, for which installation, within
which scopes — is per-call and runtime, and that machinery already exists:
`WorkContextV1` carries `audience` (the exact service or trust boundary allowed
to consume it), `authority_scopes` (each hop's grant a subset of the preceding
effective scopes) and a `seal` binding the capability to one installation and
one execution ([work-context.md](work-context.md)). Neither substitutes for the
other: derive the static list from declared dependencies for defence in depth,
and never let it stand in for the runtime check. Who validates that a declared
dependency is *permitted* — a module asking for a host endpoint is not the same
as being allowed to have it — is an authorization question for the installation
and approval layer, not for endpoint visibility.

**Only these three spellings load.** A visibility the model does not define —
including the former `module` (every module) and `external` (a location written
as a permission) — is refused when the manifest is read, as an invalid
declaration, never read as private by one path and denied by another. `module`
is written as `visibility: internal`; `external` is written as
`location: external` (see below) beside the visibility that applies.

Every refusal the endpoint model makes is one named rule, run in a fixed order,
each carrying its own witness in the table beside its check; each is protected
by a fixture in a shipped kit — the manifest kit
(`resources.EndpointDeclarationFixtures`, driven through a consumer's own loader
by `resources.RunEndpointDeclarationKit`) for the rules a manifest reaches, the
wire kit (`resources.EndpointWireFixtures`, `resources.RunEndpointWireKit`,
and `resources.InterfaceEndpointWireFixtures` with
`resources.RunInterfaceEndpointWireKit`) for the rule only bytes reach — and the
package's self-check deletes each rule in turn and proves a fixture notices:

| Rule | Judged on | Refuses |
|---|---|---|
| `endpoint-keys-known` | the mapping's keys, before decoding | a key the endpoint model does not define (a misspelt one would otherwise decode to nothing) |
| `allow-modules-derived` | the keys, before decoding; the value, in memory | any authored `allow-modules`, in any spelling, with any value |
| `wire-fields-known` | the proto, before projection | a field the schema does not define, the reserved `allow_modules` included |
| `visibility-known` | the declaration | a visibility other than `private`, `internal`, `public` or none |
| `location-known` | the declaration | a location other than `external` or none |
| `exposure-known` | the declaration | an exposure other than `public`, `none` or omitted |
| `exposure-declared` | the declaration | `visibility: public` with no exposure stated |
| `exposure-within-reach` | the declaration | `exposure: public` on an endpoint not `visibility: public` |
| `exposure-in-system` | the declaration | `exposure: public` on a `location: external` endpoint |

Every proto ingress judges the declaration whole — the projection into the
resource model (`resources.FromProtoEndpoints`), the dependency verdict, the
public split, the environment-variable prefix and the network allocation
(`network.RuntimeManager.GenerateNetworkMappings`, before any instance is
allocated) — so a proto that passes the schema's per-field checks with
`visibility: private, exposure: public`, or with reserved bytes kept as unknown
data, is refused as an invalid declaration rather than handed an address.

A module's interface entry is the whole export declaration: it names the reach
the module grants across its boundary — `public`, or `internal` (the default) —
and the service's own visibility is not consulted once the module has spoken.
An entry names nobody, and its keys are judged before decoding like an
endpoint's. An export the endpoint's own declaration contradicts — an endpoint
declaring `exposure: public` that the interface omits or exports at `internal`
— is refused at module load, naming the entry in the author's own words. (The
`InterfaceEndpoint` wire message has no reader in core; `resources.UnknownWireFields`
is what a reader of published module protos refuses the reserved field with.)

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
| (unset) | In-system, port-allocated | Native + Container (+ Public if exposed) |
| `external` | Managed resource outside the system | DNS-resolved instances |

Because the two axes are independent, a managed database can be both
`location: external` and `visibility: internal`.

### Exposure

Exposure answers *whether an address reachable from outside the workspace is
allocated*, independently of who may reach it. It is the addressing half of the
job the word `public` used to do, declared on its own:

| Exposure | Meaning | Network instances generated |
|---|---|---|
| `none` (or unset on a non-public endpoint) | No outward address | Native + Container |
| `public` | Addressed from outside the workspace | Native + Container + Public |

```yaml
endpoints:
  - name: http
    api: http
    visibility: public    # reach: anything outside the workspace may call
    exposure: public      # addressing: the Public instance is allocated, a deployment renders an outward address
  - name: grpc
    api: grpc
    visibility: public    # reachable by anything that can address it — and published nowhere
    exposure: none        # stated, never omitted on a public endpoint
```

`resources.IsExposedEndpoint` is the one condition `network.RuntimeManager`
emits the Public instance on, `resources.SplitPublicNetworkMappings` splits on
(exposed or external), and a deployment renders an outward address from. A
visibility is never that condition. Exposure is declared only on an endpoint
reachable from outside the workspace (`visibility: public`) that the system
addresses (not `location: external`): the other combinations contradict
themselves and are refused when the manifest is read. Internet exposure — a
route with hostnames — is still the deployment's ingress, declared there;
exposure is what gives it an endpoint to route to.

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
