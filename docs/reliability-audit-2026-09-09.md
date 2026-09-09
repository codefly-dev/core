# Reliability audit — 2026-09-09

Tracker: [codefly-dev/core#415](https://github.com/codefly-dev/core/issues/415)

Scope: a source and targeted behavioral audit of core, CLI, go-grpc and Postgres,
plus a follow-up audit of Redis, Python FastAPI, object storage and generic.
Twenty-five findings across the two passes, split into independently assignable
implementation issues.

This document is the shared statement of the **design defaults** those issues are
implemented against. The findings span six repositories; without one citable
record of the defaults, each repository resolves the same question — what does
"ready" mean, what does `Stop` destroy, when may a session be reused — in its own
direction. It records the audit's limits for the same reason: several findings
were *reproduced*, not *fixed*, and a reproduction must not be read as a passing
behavior.

It does not by itself change behavior. Every fix lands in its own issue and its
own repository, listed under [Ownership](#ownership).

## Audited snapshots

| Component | Revision |
| --- | --- |
| core | `535d050a7570c68fbdfafbba427b3f4b54f9ec38` |
| cli | `6dfdb4005d5a0a5ea45862392331ea0e2f70636b` |
| service-postgres | `0833c37a09b8aa714f760499050dd15f44af74e0` |
| service-go-grpc | `fa790a18891c6db4d10474aa7e2a4d89662bb96a` |

The follow-up service audit reviewed fresh `main` snapshots of service-redis,
service-python-fastapi, service-object-storage and generic.

## What the audit established

**Reproduced against real processes, sockets and Git** — permissive HTTP/TCP
readiness; stale pin retention after a requested pin fails to resolve; process
descendants surviving their group leader; a lost service lookup after a
dependency graph is restricted; colliding default SDK control addresses; Redis
accepting a protocol error as a ready reply; FastAPI `Destroy` leaving a native
process alive; and object-storage readiness reporting ready with an unreachable
backend.

**Not established** — live database recovery, Docker/Nix parity, and Kubernetes
health qualification. No child issue may be closed on the strength of a
characterization test alone; each carries its own behavioral acceptance case.

**Reviewed with no defect asserted** — the generic agent's passive lifecycle and
its explicit unsupported-capability responses. Its Go suite passed. Absence of an
asserted defect here is a reviewed result, not an unexamined one.

## Design defaults

These are normative for the whole roadmap.

### 1. Disposable tests own fresh invocation-scoped state

A disposable test owns state created for that invocation. Warm reuse and
borrowed infrastructure are opt-in and named, never the fallback.

In core this is the `sdk.WithDependencies` seam. `cliServerAddress` in
`sdk/dependencies.go` derives the control address from the workspace name alone
unless `WithNamingScope` is set, so two concurrent invocations in one workspace
resolve to the same address and `attachDependencies` adopts whichever session is
already listening. Identity must be per-invocation by default, and attachment
must verify it is talking to its own session rather than to whatever answers.
→ F05, F15.

### 2. Stop retains data; destructive reset is explicit

`Stop` releases execution resources and retains data. Destroying data requires
explicit disposable ownership of that data — an agent may not infer the right to
reset from the fact that it started the process. → F10, A03.

### 3. Readiness is declared, not guessed

Readiness requires **both** current lifecycle success and the declared
predicates of consumed endpoints and completion jobs. An open port is not
readiness; neither is a lifecycle that merely returned.

Legacy custom services do not acquire guessed health routes. go-grpc #58
deliberately introduced transport probes because customized services had no
declared health surface — that lesson stands. The replacement is *declared
capabilities*, so a service that declares nothing keeps its transport probe
rather than silently gaining a route that was never implemented. → F07, F08.

### 4. One bootstrap plan for local and deployed

Source-owned migration lineages and required extensions resolve through a single
bootstrap plan shared by local and deployed Postgres. Divergence here is what
makes a migration pass locally and fail on deploy. Required inputs are validated
before ready is reported, and every bootstrap image input is verified and
locked. → F03, F14, F16.

### 5. A failed pin fails; it does not fall back

When a required pin fails to resolve, the materialized module is rejected. It
must not silently select older cached code — a fallback here means a test run
reports a result for a revision nobody asked for. Explicit user checkout
overrides remain supported. → F04.

### 6. Reuse is a compatibility check

Reusing existing infrastructure checks semantic plan, artifact, backend, fixture
and profile compatibility. `WithFixture` and `WithRunProfile` already partition
intent in the SDK; reuse must respect that partition rather than matching on
liveness. → A01.

### 7. Deployment stages are distinct

Rendered, applied, bootstrapped and healthy are four different results and are
reported as four different results. Collapsing them is what lets a deploy report
success before anything is serving. → A04.

### 8. Compatibility claims need tested rows

A supported combination is one with a real tested version/backend row. Agreement
on a protocol version is not a compatibility claim. Unsupported and skipped
infrastructure rows are recorded explicitly rather than omitted. → A05.

## Delivery sequence

1. **Database safety** — F01 dirty-state recovery, then F02 forward-only hot
   reload. F01 is the release blocker and does not wait on architectural work.
2. **Truthful state and resolution** — F04, F07, F08, F11, F12.
3. **Isolated tests and cleanup** — F05, F06, F09, F10, F15.
4. **Deployment parity and bounded operations** — F03, F13, F14, F16.
5. **Shared contracts and qualification** — A01–A05. These start incrementally
   alongside the fixes; each child issue states its actual prerequisites.

## Ownership

Status is tracked on the [tracker](https://github.com/codefly-dev/core/issues/415),
not here.

### core

| Finding | Issue |
| --- | --- |
| F05 — dependency session identity and verified control ownership | [#416](https://github.com/codefly-dev/core/issues/416) |
| F06 — reap resistant process-group descendants | [#417](https://github.com/codefly-dev/core/issues/417) |
| F08 contract — portable declared readiness predicates | [#418](https://github.com/codefly-dev/core/issues/418) |
| F12 — preserve service lookup across graph restriction | [#419](https://github.com/codefly-dev/core/issues/419) |
| F13 core — bound temporary-port allocation | [#420](https://github.com/codefly-dev/core/issues/420) |
| F15 — scope SDK identity and environment to sessions | [#421](https://github.com/codefly-dev/core/issues/421) |
| A01 — immutable resolved execution plan | [#422](https://github.com/codefly-dev/core/issues/422) |
| A02 — typed build/runtime/schema/completion dependencies | [#423](https://github.com/codefly-dev/core/issues/423) |
| A03 — ownership, retained state, crash-recoverable sessions | [#424](https://github.com/codefly-dev/core/issues/424) |

### cli

| Finding | Issue |
| --- | --- |
| F04 — reject stale materialized modules | [#588](https://github.com/codefly-dev/cli/issues/588) |
| F05 companion — invocation identity through CLI flows | [#589](https://github.com/codefly-dev/cli/issues/589) |
| F08 — lifecycle completion and endpoint health for readiness | [#590](https://github.com/codefly-dev/cli/issues/590) |
| F09 — reverse topological teardown barriers | [#591](https://github.com/codefly-dev/cli/issues/591) |
| F11 — publish validated agent-accepted network mappings | [#592](https://github.com/codefly-dev/cli/issues/592) |
| A04 — rendered/applied/bootstrapped/healthy results | [#593](https://github.com/codefly-dev/cli/issues/593) |
| A05 — gate combinations on real lifecycle conformance | [#594](https://github.com/codefly-dev/cli/issues/594) |

### service-postgres

| Finding | Issue |
| --- | --- |
| F01 — fail closed on dirty migrations | [#76](https://github.com/codefly-dev/service-postgres/issues/76) |
| F02 — forward-only hot reload | [#77](https://github.com/codefly-dev/service-postgres/issues/77) |
| F03 — same migration sources local and deployed | [#78](https://github.com/codefly-dev/service-postgres/issues/78) |
| F10 — consistent stop/retain/reset across Docker and Nix | [#79](https://github.com/codefly-dev/service-postgres/issues/79) |
| F13 Postgres — explicit deadlines | [#80](https://github.com/codefly-dev/service-postgres/issues/80) |
| F14 — validate required sources and extensions | [#81](https://github.com/codefly-dev/service-postgres/issues/81) |
| F16 — verify and lock bootstrap image inputs | [#82](https://github.com/codefly-dev/service-postgres/issues/82) |

### service-go-grpc

| Finding | Issue |
| --- | --- |
| F07 — keep compile failures out of STARTED | [#95](https://github.com/codefly-dev/service-go-grpc/issues/95) |
| F08 deployment — render probes from declared capabilities | [#96](https://github.com/codefly-dev/service-go-grpc/issues/96) |

### Follow-up service audit

| Finding | Issue |
| --- | --- |
| E01 — require authenticated Redis PONG | [redis#35](https://github.com/codefly-dev/service-redis/issues/35) |
| E02 — own and stop native Redis | [redis#36](https://github.com/codefly-dev/service-redis/issues/36) |
| E03 — invocation scope in Redis config and state paths | [redis#37](https://github.com/codefly-dev/service-redis/issues/37) |
| E04 — resolve redis-server from the locked Nix environment | [redis#38](https://github.com/codefly-dev/service-redis/issues/38) |
| E05 — supervise FastAPI processes, safe repeated Start | [fastapi#19](https://github.com/codefly-dev/service-python-fastapi/issues/19) |
| E06 — Destroy stops FastAPI and its watcher | [fastapi#20](https://github.com/codefly-dev/service-python-fastapi/issues/20) |
| E07 — no unauthenticated storage via all-interface publishing | [storage#16](https://github.com/codefly-dev/service-object-storage/issues/16) |
| E08 — probe backend access for storage readiness | [storage#17](https://github.com/codefly-dev/service-object-storage/issues/17) |
| E09 — project host GCS credentials explicitly | [storage#18](https://github.com/codefly-dev/service-object-storage/issues/18) |

## Related work

- [core#318](https://github.com/codefly-dev/core/issues/318) — generic agent
  shutdown leak. F06 is a distinct SDK reproduction; A03 coordinates the shared
  ownership model.
- [cli#585](https://github.com/codefly-dev/cli/issues/585) — aborted flow strands
  native services. It remains the owner of failure unwind; F09 does not absorb it.
- [core#380](https://github.com/codefly-dev/core/issues/380) — sidecar placement.
  Typed dependency and plan work stays compatible with it.
- [cli#530](https://github.com/codefly-dev/cli/issues/530) — remote deployment
  feedback. A04 preserves the verified-local direct-apply boundary.

## Closing rules

A child issue is done when it has a merged fix, its behavioral acceptance
evidence, and the actual consumer dependency bump. None of the following is
sufficient on its own:

- source tests passing;
- the defect being characterized;
- another repository having changed, without a released and pinned consumer path.
