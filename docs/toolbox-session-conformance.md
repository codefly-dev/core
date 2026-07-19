# Toolbox session conformance evidence

Date: 2026-07-16

Scope: roadmap gates CF0 and CF1; verification levels V0-V4 only

Environment: macOS arm64, Go 1.26.5

This document records the first Codefly agent-roadmap implementation slice. It
does not authorize or claim Kubernetes, AWS, Warden, Mind, or production
deployment verification (V5-V8).

## Baseline component revisions

The slice started from these independent repository revisions:

| Component | Revision | Starting tag |
| --- | --- | --- |
| core | `094d5f8d5b03875ed3a83097f35b57408389afaa` | `v0.2.18` |
| cli | `1e6b1e8dfa6c7d35e95d21f1ef7165a1a5e4d67d` | `v0.1.8` |
| service-postgres | `045e7ec02e6ac069762e2669c1d4d4307672945d` | `v0.0.98` |
| toolbox-docker | `2d9f0ffaa31c81c02280747b9a4f9cb3c49ec915` | `v0.0.9` |
| toolbox-grpc | `20cfc0e1f6b6f8c6a86aacd909f61b1202b6ca01` | `v0.0.9` |
| toolbox-nix | `c7ac91fd26d14ff0d438c0209cd8bcd152e09760` | `v0.0.9` |
| toolbox-python-repl | `b994b00866acfa414b26724901a57bf1d96ebb0f` | `v0.0.9` |
| toolbox-web | `589c4e7a7aaeb35f429a83c3819e8ce61f88b933` | `v0.0.10` |

The repositories were inspected independently; changes are committed in their
own repositories rather than treating the aggregate `go.work` directory as one
Git repository.

## Contract delivered

- `toolbox/session.ToolboxSession` is the supported host composition root for
  production manifest validation, process launch, principal binding, PDP
  evaluation, scoped authorization, invocation, audit, and cleanup.
- Discovery is two-phase: identity and summaries first, then exactly one named
  descriptor. Approval binds the requested name, full descriptor, identity,
  summary catalog, and deterministic digest.
- Every invocation binds principal, organization, toolbox audience, action,
  resource, catalog digest, exact request digest, expiry, one use, and typed
  caveats. Bound mode rejects an absent token instead of downgrading to the raw
  PDP path.
- Correlation carries request, session, objective, task, invocation, trace, and
  release identifiers. Principal, tenant, release authority, signing material,
  and authorization tokens cannot be supplied through tool arguments.
- Audit events contain identities, decisions, digests, counts, and stable error
  categories. They never serialize raw arguments, results, credentials, or
  bearer tokens. The standalone schema redactor fails closed on unknown fields,
  unknown classifications, references, composite schemas, and type ambiguity.
- Retry advice is a closed set (`never`, `safe`,
  `reconcile_before_retry`) derived from approved idempotency and the observed
  transport outcome. The session never retries implicitly.
- Agent handshake protocol 2 accepts only a private absolute Unix socket or an
  explicit loopback DNS target. Numeric-port, remote-target, caller-selected
  bind-address, unauthenticated-agent, and caller-overridden-principal paths are
  removed.
- `manager.Load` requires explicit sandbox and principal choices for every
  spawn. Production admission additionally requires an enforcing sandbox,
  valid non-expired principal, host PDP callback, and a 32-byte scoped secret.
- The conformance fixture and CLI-independent host harness prove the supported
  path without importing Codefly CLI internals.

Intentional breaking changes are accepted for this pre-customer phase. Core,
generated bindings, CLI, SDK, service plugin, and every aggregate-workspace
toolbox consumer are updated and verified as one release operation; no legacy
wire fallback or compatibility shim is retained.

## Protocol and manifest evidence

- Agent handshake: `2`
- Fixture contract: `codefly.toolbox.conformance/v1`
- Scoped authorization envelopes: `v1-hmac`, `v2-ed25519`
- Gateway proto SHA-256:
  `ba5c1afc96dedc3f815285fc249553fbc250ad091706160abe748dced8953fe9`
- Generated Go gateway binding SHA-256:
  `4ccf4c03e1d1c10d0d5ff9cc7922ebd92790ed65f36ff4644068522d10d16532`

Every external toolbox manifest now uses a valid network enum and declares an
explicit permission for every advertised tool. Each owning repository has a
manifest/catalog conformance test.

## V0-V4 verification contract

The aggregate release gate must be expressed only through Codefly-owned
operations:

```text
core:
  codefly test source --dir .
  codefly schema generate
  codefly schema lint
  codefly schema breaking

cli:
  codefly test source --dir .

service-postgres:
  codefly test source --dir .

toolbox-docker, toolbox-grpc, toolbox-nix, toolbox-python-repl, toolbox-web:
  codefly test source --dir .
```

The schema commands above name the canonical plugin-owned surface. Until the
schema-agent migration is complete, the release gate must report that surface
as unavailable rather than embedding raw Buf commands in CI or this contract.

The core suite is package-serialized because its Docker/SDK fixtures share
machine-level ports and process resources. A package-parallel run reproduced
contention in `runners/golang` and SDK timing tests; every exact failure passed
in isolation and the complete serialized suite passed.

The final release gate also runs `codefly self build` followed by
`codefly agent build --all`; those installed-agent results are recorded in the
release commit/report after the aggregate build completes.

### Hostile and negative evidence

| Case | Expected result | Evidence |
| --- | --- | --- |
| host policy denial | no handler effect | effect counter remains zero |
| missing/invalid scoped token | deny, no PDP downgrade in bound mode | handler not reached |
| expired/replayed token | deny | use stays consumed through clock-skew window |
| wrong audience/resource/tenant | deny | exact binding tests pass |
| catalog/request mutation | deny | deterministic digest mismatch |
| timeout | stable `timeout`; safe retry only for approved idempotent tool | token remains consumed |
| cancellation | stable `canceled`; never auto-retry | cancellation audit emitted |
| crash before authorization | no authorization/invoke audit | hard process exit contained |
| crash after authorization | `reconcile_before_retry` | authorization ID and invoke audit exist |
| panic during execution | stable `internal_failure` | process remains available; no effect |
| panic after side effect | `reconcile_before_retry` | effect count is one after recovery |
| response serialization failure | stable `internal_failure` | later calls succeed |
| concurrent sessions | no principal, callback, replay, or trace crossover | unique one-use authorization per call |
| cleanup | idempotent | private UDS directory and callback socket removed |
