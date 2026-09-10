# Declared Readiness

## The Problem

"Is this dependency ready?" used to be answered by opening a socket. That
answer is wrong in ways that are easy to reproduce:

- A service exposing an admin port and a gRPC port is called ready as soon as
  *either* accepts a connection. An open admin port masks a closed gRPC
  endpoint.
- An HTTP server answering `503 Service Unavailable` is reachable. Accepting any
  HTTP status makes "reachable" and "working" the same thing.
- A dependency that exposes no endpoint at all — a migration, a seed job — was
  either skipped entirely or judged by whether its agent had *started* it, not
  by whether it had *finished*.
- A service that died after a successful `Start` kept the ready verdict its
  start had earned.

## The Solution: declare the predicate

An endpoint declares what healthy means for it. A consumer declares which
endpoints it actually needs. Both are data, so the CLI, the runtime agents and
the Kubernetes renderers evaluate the same predicate instead of each inventing
one.

```yaml
# service.codefly.yaml — the producer
endpoints:
  - name: grpc
    health:
      readiness:
        kind: grpc-health
        service: accounts.v1.Accounts
      liveness:
        kind: grpc-health
      startup:
        kind: transport
        initial-delay: 1s
        period: 500ms
        failure-threshold: 30
  - name: http
    visibility: public
    health:
      readiness:
        kind: http
        path: /healthz
        statuses: ["200", "204-299"]
        body-contains: ok
  - name: admin
    api: tcp
    health:
      readiness:
        kind: agent
```

```yaml
# service.codefly.yaml — the consumer
service-dependencies:
  - name: accounts
    module: saas
    endpoints:
      - name: grpc
      - name: http
      - name: admin
        required: false   # consumed, but readiness does not wait on it
  - name: seeder
    module: saas
    kind: completion      # exposes no endpoint; must finish, not merely run
```

## Predicate kinds

| kind | Applies to | Holds when |
|---|---|---|
| `transport` | any endpoint | the address accepts a connection |
| `grpc-health` | `grpc` endpoints | `grpc.health.v1.Health/Check` answers `SERVING` for `service` (empty = whole server) |
| `http` | `http`, `rest`, `connect`, `mcp` endpoints | `GET path` returns a status in `statuses` (default `200-399`) and, when set, a body containing `body-contains`. Redirects are **not** followed: the probe judges the resource it named, so a `/healthz` that 302s to a login page fails a `statuses: ["200"]` predicate instead of passing on the redirect target |
| `agent` | any endpoint, or an endpointless dependency | the owning runtime agent reports a started, live service — for health only that agent can judge, such as an authenticated SQL or schema check |
| `completion` | endpointless dependencies only | a one-shot workload terminated successfully |

`readiness`, `liveness` and `startup` are independent. Declaring readiness never
implies a restart policy: `liveness` and `startup` stay absent unless written
down.

Requirements combine conjunctively — a consumer is ready only when *every*
required predicate holds.

The endpoint's declared transport security (`secured` in its API details) rides
on the plan, so a probe against a TLS endpoint dials TLS. A caller never has to
supply it, and so cannot forget to.

### Timing is binding

`initial-delay`, `period`, `failure-threshold` and `success-threshold` are part
of the predicate, not advice. An evaluator waits out `initial-delay`, then
attempts the check every `period` until it sees `success-threshold` consecutive
passes or `failure-threshold` consecutive failures. Both thresholds default to
1, so a probe that declares no timing is a single attempt — but one that
declares a boot window gets it, rather than failing on its first attempt.

## Absence is legacy, not a new requirement

An endpoint that declares no `health` keeps transport-only semantics. A
hand-customized gRPC server that never registered `grpc.health.v1` is not
retroactively required to, and no service is asked for a `/healthz` it never
served — there is deliberately no default HTTP path.

The fallback is explicit rather than silent: `ReadinessRequirement.Declared` is
false and the rendered predicate says so, so a consumer can report *transport
connect (legacy: endpoint declares no readiness)* instead of implying it
verified health.

Newly generated services declare the richer predicate they actually support.

## Endpointless dependencies

What a consumer waits for is decided by the dependency's `kind` — the same
declaration that types the edge for the execution graph (see
`resources/dependency_kind.go`). Readiness does not add a second vocabulary for
it; it is the evaluation half of `DependencyKind.Prerequisite()`.

| `kind` | prerequisite | ready when |
|---|---|---|
| absent (legacy), `runtime` | `endpoint-health` | every required endpoint's predicate holds — or, with no endpoint to probe, the owning agent reports a started service |
| `completion` | `completion` | the workload finished successfully |
| `build`, `schema`, `external` | `none` | never gates: the endpoints are still resolved and mapped, they just do not hold up startup |

A service dependency defaults to legacy, preserving the behavior of every
workspace that never declared a kind. A **job** dependency defaults to
`completion` instead: a job is one-shot work, and a migration that is still
running has not prepared anything. A long-running job is declared
`kind: runtime`.

Two contradictions are rejected. `kind: completion` alongside declared
endpoints is caught at load by `ServiceDependency.Validate` — completed work
serves nothing. `kind: runtime` onto a producer that exports no endpoint is
caught when readiness is planned, by `ValidateDependencyPrerequisite`, because
only that caller sees both sides.

Marking *every* endpoint of a dependency `required: false` is not the same as a
dependency that exposes none, so it is not silently treated as one: it is an
error until you say which you meant by dropping the `required: false` or
declaring `kind: external`.

## Lifecycle generation

`StartStatus.generation` increments on every successful `Start`, counting from
1, and every `HealthReport` is stamped with the generation it was taken in. A
ready verdict recorded before a restart cannot be read as evidence about the
process running now, and a service that dies after `Start` reports `ERROR` at
the generation that failed.

An agent that predates the field reports `0` on every start, so `0` means
"unreported", never "the first one". Read it through
`services.StartGeneration`, which returns that distinction rather than letting
a consumer compare two zeroes and conclude nothing changed.

## Using it

```go
// Consumer side: what must hold before this service may start.
requirements, err := resources.PlanReadiness(service.ServiceDependencies, dependencyEndpoints)

report := readiness.Evaluate(ctx, requirements, generation, func(r *resources.ReadinessRequirement) readiness.Target {
    return readiness.Target{Address: addressFor(r), Started: started[r.Dependency]}
})
if !report.Ready {
    return fmt.Errorf("not ready: %s", strings.Join(resources.FailingPredicates(report), "; "))
}

// Producer side: what a deployment renderer emits for one endpoint.
plan := resources.PlanEndpointProbes(endpoint)  // Readiness, Liveness, Startup
```

Invalid declarations fail at load, naming the source: an unsupported kind, a
`grpc-health` probe on an HTTP endpoint, an `http` probe with no path or an
unroutable status, a `completion` probe on an endpoint, a field belonging to a
different kind, or an endpoint reference the producer does not declare.
The same rules apply to endpoints arriving from an agent over the wire —
`resources.ValidateEndpointHealth`, applied by `FromProtoEndpoints` — so a
contradiction is caught at ingestion rather than when the manifest is written
back to disk.

A target that can never be probed — no resolved address, an address that is not
`host:port`, a path that cannot form a URL — reports
`PROBE_FAILURE_KIND_INVALID_TARGET` rather than `UNREACHABLE`, so a caller
stops retrying a declaration bug and reports it.

## Proto

The wire contract lives in `proto/codefly/base/v0/readiness.proto`
(`Probe`, `Health`, `ProbeResult`, `HealthReport`), reachable from
`Endpoint.health` and from `InformationResponse.health`. Regenerate with:

```bash
codefly generate proto --proto ./proto --output ./generated --local
```
