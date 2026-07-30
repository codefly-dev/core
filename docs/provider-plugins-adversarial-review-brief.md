# External provider plugins: full adversarial review brief

Status: review commission

Prepared: 2026-07-30

Proposal under review:
[`docs/provider-plugins.md`](provider-plugins.md)

## Copy-paste assignment

You are the independent adversarial reviewer for Codefly's proposed external
provider plugin system.

Read this review brief completely, then read the complete proposal at
`core/docs/provider-plugins.md`. Inspect the referenced implementation in the
Codefly repositories and verify changing vendor behavior against current
official primary documentation. Do not implement the proposal. Do not create
GitHub issues. Your job is to determine whether the proposal is necessary,
coherent, safe, implementable, testable, appropriately scoped, and decomposed
well enough to become implementation issues.

Review the whole proposal. Permissions and secret handling are critical, but
they are not the only scope. Challenge:

- the product problem and whether a provider plugin is the right abstraction;
- the boundary between Codefly, provider agents, application services, and
  vendor APIs;
- every claimed reuse of existing Codefly tooling;
- the permission, sandbox, credential, network, approval, and audit model;
- provider protocol shape and versioning;
- deterministic observation, planning, application, state, ownership, drift,
  deletion, and uncertain-outcome recovery;
- configuration projections, secret sinks, one-time secret capture, endpoint
  resolution, ingress, and local forwarding;
- Stripe, Sentry, and Resend API assumptions and whether they create a useful
  cross-provider contract;
- local dogfood, production safety, testing, conformance, migration,
  operations, UX, and future extensibility;
- the proposed roadmap and minimum set of independently executable GitHub
  issues.

Treat the proposal as untrusted. Find contradictions, missing invariants,
unimplemented dependencies, confused ownership, unsafe failure modes,
unnecessary machinery, and abstractions shaped too closely around the first
provider. Explicitly consider rejecting the design or replacing part of it
with a simpler alternative.

Source code is authoritative for Codefly's current capabilities. Current
official vendor documentation is authoritative for external API behavior. The
proposal is only a hypothesis. Cite repository file paths and line numbers for
Codefly claims and direct official URLs for external claims. Clearly label
inferences.

Return one self-contained Markdown report using the exact output contract in
this brief. Prefer saving it as
`core/docs/provider-plugins-adversarial-review.md`; if the review environment
does not permit file creation, return the complete Markdown as the response.
Another engineer must be able to understand every material finding without
reading this conversation.

## Mission

The review must answer one decision:

> Is `codefly:provider`, as proposed, the correct and sufficiently safe
> foundation for Codefly to configure, dogfood, and eventually manage external
> SaaS dependencies such as Stripe, Sentry, and Resend?

The answer may be:

1. **Approve**: the protocol and trust boundaries can be implemented without
   a material redesign.
2. **Approve with required changes**: the core direction is sound, but named
   changes must be made before issues are created or implementation begins.
3. **Reject or reframe**: the abstraction, scope, or trust model is wrong
   enough that implementation should not begin.

This is a pre-implementation architecture gate. Favor finding expensive
mistakes now.

## What Codefly wants

Codefly wants a SaaS starter that can be dogfooded locally and eventually used
in production with real external platforms. Founders should not have to:

- manually copy dynamic local ports;
- paste credentials into shell command arguments;
- duplicate provider setup logic in every starter;
- hand-create webhook endpoints without drift visibility;
- put management credentials into application runtime environments;
- guess whether a remote resource was created or is owned;
- inspect several vendor dashboards to understand setup health;
- accept unaudited scripts with unrestricted network and filesystem access.

The desired founder experience is approximately:

```text
codefly provider setup billing --env local-dogfood
```

Codefly should then:

1. locate the workspace and environment;
2. load a versioned provider agent;
3. collect and validate non-secret inputs and secret references safely;
4. bind the operation to the initiating principal;
5. observe the selected vendor account and relevant remote resources;
6. resolve Codefly service endpoints and ingress instead of accepting copied
   ports;
7. calculate a deterministic, reviewable plan;
8. obtain policy admission and approval for exact actions;
9. apply only the approved plan;
10. safely capture one-time outputs;
11. project generic application configuration;
12. persist ownership and recovery state outside Git;
13. produce signed execution evidence;
14. run provider and workspace doctor checks;
15. rerun with no diff when nothing has changed.

Application code should continue consuming generic capabilities:

- `billing`;
- `email`;
- `error-tracking`;
- future contracts such as analytics, feature flags, CAPTCHA, observability,
  storage, or managed databases.

Application services must not depend on `provider-stripe`,
`provider-resend`, or `provider-sentry`. The external provider system is
control-plane setup and lifecycle machinery, not an application runtime
dependency.

## Why this proposal exists

The SaaS starter has accumulated setup scripts for real integrations,
including WorkOS, Stripe, Resend, PostHog, Sentry, OpenTelemetry, and
Turnstile. Stripe, Resend, and Sentry were selected as the first design cohort
because their differences pressure the abstraction:

- Stripe has sandbox/live separation, idempotent mutations, remotely managed
  webhooks, and a webhook signing secret returned during creation.
- Sentry is initially observation/projection oriented, has no required inbound
  webhook, exposes a browser-safe public DSN, supports regional/self-hosted
  origins, and separates setup and build credentials.
- Resend has full-access versus sending-only credentials, domain
  prerequisites, webhooks, different rate limits and forwarding, and changing
  documentation around signing-secret retrieval.

The proposal deliberately orders implementation:

1. Stripe vertical slice;
2. Sentry as a non-webhook counterexample;
3. Resend as a second webhook and credential-scope counterexample.

The intended result is not Terraform, Pulumi, or a general infrastructure
language. It is a narrow Codefly lifecycle for application-facing managed SaaS
dependencies.

## Proposal summary

The full design is in `core/docs/provider-plugins.md`. The following summary
exists so the reviewer can identify internal contradictions, but it does not
replace reading that document.

### New agent type

Add:

```yaml
kind: codefly:provider
```

The proposed provider agent:

- is started only while Codefly operates on a provider binding;
- is not a workload in the service graph;
- has no application endpoint, readiness loop, deployment image, or runtime
  dependency;
- is resolved and distributed through Codefly's existing agent machinery;
- implements a provider-specific leaf protocol.

### Environment binding

Provider selection and desired state are environment-scoped:

```yaml
environments:
  - name: local-dogfood
    provider-bindings:
      billing:
        agent:
          kind: codefly:provider
          publisher: codefly.dev
          name: stripe
          version: 0.1.0
        input-configuration: providers/stripe
        output-configuration: billing
        output-contract: codefly.dev/configuration/billing@1
        management: managed
        deletion-policy: retain
        spec:
          account-mode: sandbox
          webhook:
            lifecycle: managed
            callback:
              service: auth-sidecar
              endpoint: rest
              path: /v1/billing/webhook
            exposure: local-forwarded
```

The proposed management modes are:

- `observe`: inspect and project without remote mutation;
- `managed`: allow explicitly planned remote lifecycle;
- `disabled`: do not operate the binding.

Deletion defaults to `retain`.

### Host/provider responsibility boundary

The Codefly host is proposed to own:

- workspace and environment selection;
- configuration and secret-reference loading;
- safe prompting and input ingestion;
- agent resolution, launch, authentication, sandbox, and supervision;
- principal binding and policy decisions;
- endpoint and public-ingress resolution;
- HTTP transport and credential injection;
- plan canonicalization, hashing, confirmation, and approval;
- state durability, locking, and schema-upgrade orchestration;
- public configuration writes and secret sinks;
- execution receipts and audit correlation;
- doctor composition and output formatting.

The provider agent is proposed to own:

- vendor-specific schemas and validation;
- request descriptions and response decoding;
- stable remote identity and relevant canonical observation;
- vendor-specific diff and plan semantics;
- idempotency strategy;
- provider error and rate-limit translation;
- mapping into declared generic configuration contracts;
- provider-specific diagnostics and remediation.

The reviewer must determine whether this boundary is coherent, enforceable,
and practical for real vendor APIs.

### Protocol

The proposed RPC surface is:

| RPC | Intended property |
| --- | --- |
| `GetProviderInformation` | Offline schemas, capabilities, contracts, origins, operations, and state version |
| `Validate` | Deterministic and offline |
| `Observe` | Brokered read-only remote access |
| `Plan` | Deterministic and offline from desired + prior + observation |
| `Apply` | Execute one exact approved plan with checkpoints |
| `Doctor` | Bounded read-only local/remote checks |
| `UpgradeState` | Deterministic and offline |

There is no long-lived mutable `Configure` session. Requests carry explicit
provider context.

### Remote lifecycle

The proposal separates:

```text
Validate -> Observe -> Plan -> Approve -> Apply -> Project -> Doctor
```

Plans may contain:

```text
NOOP
PROJECT_OUTPUT
IMPORT
CREATE
UPDATE
REPLACE
DELETE
MANUAL_ACTION
BLOCKED
```

The plan digest is proposed to bind:

- agent identity and artifact digest;
- protocol and state versions;
- binding and desired-state hashes;
- prior and observed state;
- resolved endpoint identities and origins;
- ordered actions;
- output target and current digest;
- secret-sink identity and capability;
- relevant policy inputs.

Changing any bound input invalidates approval.

### State and ownership

Local state is proposed under:

```text
~/.codefly/providers/<workspace-sha>/<environment>/<binding>/
```

It should contain remote IDs, relevant observed fields, ownership, output
digests, opaque secret references/fingerprints, and operation/receipt IDs.
Raw secrets must never enter state.

Ownership distinguishes:

- observed;
- owned;
- explicitly adopted/imported;
- unmanaged/conflicting.

Automatic adoption based only on URL, name, domain, or email is forbidden.
Remote deletion requires exact identity, ownership, policy, and an explicit
destructive plan.

### Permission model

The proposal requires reuse of Codefly's existing permission stack rather than
a provider-specific authorization implementation.

The intended effective decision is:

```text
allow =
    manifest declaration
  intersection runtime catalog ceiling
  intersection principal role grant
  intersection operator policy and caveats
  intersection required approval
  intersection environment management mode
  intersection exact approved plan action
  intersection credential-handle constraints
  intersection HTTP broker constraints
  intersection process sandbox capacity
```

The intended canonical actions include:

```text
provider.validate
provider.observe
provider.plan
provider.create
provider.update
provider.delete
provider.import
provider.disconnect
provider.project.public
provider.project.secret
provider.doctor
```

A broad `provider.apply` RPC permission is explicitly insufficient.
Authorization is required for each exact planned action and output commit.

### Network and credentials

Initial provider agents should have:

- loopback/UDS access needed for host communication;
- no direct external network;
- no workspace writes;
- no direct secret-store access;
- no raw management/runtime/build credential bundle.

The proposed Codefly HTTP broker:

- derives exact origin ceilings from the packaged manifest;
- enforces method/path, redirect, timeout, and response-size policy;
- injects credentials by opaque handle;
- rejects caller-supplied authorization, cookie, and proxy headers;
- normalizes request IDs, retry-after, and rate-limit metadata;
- injects stable idempotency keys where declared;
- records sanitized deterministic cassettes for tests.

A credential handle is intended to be an attenuated capability bound to:

- principal and organization;
- provider audience and binding;
- operation and approved plan-action digest;
- credential purpose;
- exact origin and header placement;
- allowed methods and path templates;
- expiration and maximum uses.

### One-time secret capture

For successful API responses that return a secret only once, the proposal does
not allow raw secret-bearing responses to reach the provider process.

The packaged manifest declares a maximum response capture by resource type,
method, path, content type, JSON Pointer, output name, classification, and
purpose. The HTTP broker captures the declared value directly into a prepared
and authorized secret sink, replaces it with an opaque handle, sanitizes
recording/logging surfaces, and checkpoints the reference.

If capture or durable persistence fails after the remote mutation, the
operation becomes `UNCERTAIN` and later actions stop.

### Configuration projection

Providers return typed public values or opaque secret handles/references.
Codefly writes the generic configuration through a host-owned atomic writer.
The writer must protect against path traversal, symlinks, unignored secret
files, ownership conflicts, stale output digests, partial writes, and unsafe
overwrites.

Local v1 may use ignored owner-only secret files. Mutating production
operations that can generate one-time secrets require a writable managed
secret sink before the remote request is sent.

### Endpoints and local dogfood

Provider specs refer to semantic Codefly endpoints:

```yaml
callback:
  service: auth-sidecar
  endpoint: rest
  path: /v1/billing/webhook
```

They do not contain generated ports or previously resolved URLs. Codefly
resolves local origins, public ingress, and environment-specific endpoints.

Proposed exposure modes:

- `local-direct`;
- `local-forwarded`;
- `public`;
- `existing`.

Vendor forwarding CLIs are treated as separately supervised companions, not
as provider-managed remote resources.

### Testing

The proposal follows Codefly's integration-as-unit-test philosophy:

- deterministic pure contract tests;
- sanitized replay of real API interactions through the broker;
- provider conformance tests using `codefly agent ci`;
- explicit opt-in live vendor acceptance against dedicated sandbox/test
  accounts;
- full SaaS starter dogfood with the real backend, database, configurations,
  endpoints, and provider integrations.

It rejects hand-written fake provider clients as authoritative tests.

### Proposed implementation sequence

The proposal has:

- P0: approve design and threat model;
- P1: core/CLI provider foundation;
- P2: Stripe vertical slice;
- P3: Sentry;
- P4: Resend;
- P5: production hardening and general availability.

After review, this is intended to become approximately five self-contained
GitHub issues:

1. foundation;
2. Stripe;
3. Sentry;
4. Resend;
5. production hardening.

## Non-negotiable intended invariants

These are the proposal author's intended invariants. The reviewer should
challenge whether they are sufficient and achievable, but must flag any
recommendation that intentionally weakens one.

1. Application services consume generic configuration contracts, never
   provider agents.
2. Provider binaries are treated as potentially buggy or compromised.
3. Provider code declares capabilities; the Codefly host enforces them.
4. No provider-specific authorization system is introduced.
5. Every production operation is bound to a validated, non-expired principal.
6. A provider cannot impersonate another principal.
7. Manifest permission declarations are a maximum ceiling, not a grant.
8. Every remote mutation is authorized against an exact action and resource.
9. Approval binds the exact plan; any material change invalidates it.
10. `--yes` and `--force` cannot manufacture authority or bypass policy.
11. Provider agents do not get unrestricted external network.
12. Provider agents do not get arbitrary workspace writes.
13. Provider agents do not get secret-store credentials.
14. Raw management credentials are not exposed when an opaque broker handle
    can perform the required request.
15. Management, runtime, build, browser, and webhook-verification credential
    purposes remain distinct.
16. A setup credential is not automatically projected into runtime.
17. A provider cannot add its own authorization header or change the
    credential's approved origin.
18. One-time secret outputs do not enter provider state, logs, plans,
    diagnostics, receipts, cassettes, command arguments, or Git.
19. Provider agents never write projected configuration directly.
20. Provider state contains remote identity and recovery evidence, not raw
    secrets.
21. Observe and Plan do not mutate remote state.
22. Apply executes only actions present in the exact approved plan.
23. Remote deletes default to retained and require exact ownership and
    destructive authorization.
24. Ambiguous remote outcomes are not blindly retried.
25. Every externally visible effect is checkpointed.
26. Permission or policy backend failure fails closed.
27. Provider state and output writes are locked and transactionally durable to
    the degree the local filesystem permits.
28. Generated local ports and URLs come from Codefly configuration/runtime
    resolution.
29. Production mutation is blocked until required production state and secret
    durability exist.
30. No provider-specific conditional is added to generic core/CLI lifecycle
    code.
31. The second identical run produces no remote or local diff.
32. Scripts become compatibility shims only after plugin parity.
33. A denied operation changes no remote state, local state, configuration, or
    secret sink.
34. Security decisions and operation outcomes are attributable without
    exposing secrets.
35. The protocol is designed against all three initial providers before being
    declared stable.

## Explicit non-goals for the first release

The proposal does not intend to:

- replace Terraform, Pulumi, Crossplane, or general infrastructure as code;
- model every vendor object as a universal resource graph;
- continuously reconcile providers in a long-running controller;
- build a Codefly-hosted secrets product;
- create production accounts, payment products, prices, tax rules, email
  domains, or Sentry organizations automatically;
- expose provider binaries as application runtime services;
- support arbitrary provider-native SDK execution with unrestricted egress;
- make browser-driven dashboard automation the primary integration path;
- promise production mutations before remote state and writable secret sinks
  are production-grade;
- implement every current SaaS starter integration in the first protocol
  release.

Flag scope that accidentally contradicts these non-goals.

## Repository map

The workspace root is normally:

```text
/Users/antoine/development/deus/codefly
```

Read the root `AGENTS.md` before running repository commands. Its golden rule
is to use Codefly commands for builds and generation. Do not substitute direct
`go build`, `buf`, or equivalent commands when validation is required.

### Authoritative repositories

| Repository | Local path | Review purpose |
| --- | --- | --- |
| `codefly-dev/core` | `core/` | Agent identity, policy, sandbox integration, configurations, environment resources, proto, execution receipts |
| `codefly-dev/cli` | `cli/` | Agent lifecycle, provider command orchestration, doctor, state/journal patterns, endpoint/runtime integration |
| `codefly-dev/toolbox-web` | `toolbox-web/` | Existing guarded HTTP behavior proposed for reuse |
| `codefly-dev/module-saas-starter` | `module-saas-starter-six-integrations/` | Merged provider setup scripts and real generic configuration contracts |

The ordinary `module-saas-starter/` worktree may contain unrelated local
changes and may be behind. Use `module-saas-starter-six-integrations/` as the
merged reference for this review unless repository state proves otherwise.
Do not modify either worktree.

### Mandatory Codefly sources

Read these sources, plus directly referenced tests:

#### Agent model and transport

- `core/proto/codefly/base/v0/agent.proto`
- `core/proto/codefly/services/agent/v0/agent.proto`
- `core/proto/codefly/services/toolbox/v0/toolbox.proto`
- `core/resources/agent.go`
- `core/resources/toolbox.go`
- `core/agents/manager/loader.go`
- `core/agents/manager/manager.go`
- `core/agents/manager/sockets.go`
- relevant tests under `core/agents/manager/`
- agent commands under `cli/cmd/agents/`

Questions include whether a new agent kind is necessary, whether typed leaf
service behavior already exists, whether current advertisement helpers are
safe to reuse, and how many routing/build/install paths hardcode known kinds.

#### Permission and policy system

- `core/policy/doc.go`
- `core/policy/principal.go`
- `core/policy/permissions.go`
- `core/policy/pdp.go`
- `core/policy/pdp_ceiling.go`
- `core/policy/gateway.go`
- `core/policy/callback.go`
- `core/policy/scoped_auth.go`
- `core/policy/scoped_auth_v2.go`
- `core/policy/scoped_auth_v2_test.go`
- `core/policy/escalation.go`
- `core/policy/hardening.go`
- `core/policy/pdp_saas.go`
- `core/policy/observability.go`
- `core/policy/TWO_LEVEL_AUTHZ.md`
- `core/policy/MIND_INTEGRATION.md`
- `core/policy/PLUGIN_AUTHORS.md`
- related policy tests

Separate what is implemented and production-wired from what is proposed,
partially integrated, shadow-mode, optional, or test-only. Do not assume that a
type existing in `core/policy` proves all required CLI/provider wiring exists.

#### Configuration, secrets, environments, and endpoints

- `core/proto/codefly/base/v0/configuration.proto`
- `core/proto/codefly/base/v0/environment.proto`
- `core/proto/codefly/base/v0/endpoint.proto`
- `core/configurations/`
- `core/resources/environment.go`
- `core/resources/environment_variables_manager.go`
- relevant configuration/environment tests
- `cli/cmd/doctor_workspace.go`
- `cli/docs/doctor-workspace-qualification.md`
- endpoint/runtime resolution code discovered from `codefly endpoint`

Verify the exact read/write capability of the current configuration system,
secret reference/resolver behavior, existing managed-secret declarations, and
whether the proposed writer/sink boundaries fit current resource models.

#### State, locking, operations, and receipts

- `core/executionreceipt/`
- `core/proto/codefly/execution/v1/execution.proto`
- `cli/pkg/executionjournal/`
- `cli/pkg/executionruntime/`
- existing durable update/state patterns in Core and CLI

Verify what can genuinely be reused for:

- workspace isolation;
- locking;
- bbolt or equivalent durability;
- operation and attempt identity;
- admitted/started/terminal receipts;
- partial, compensated, and uncertain outcomes;
- Ed25519 signing;
- crash recovery and concurrent processes.

#### HTTP tooling

- the complete `toolbox-web/` request path;
- its manifest and permission declarations;
- origin and redirect validation;
- timeout and response limits;
- logging and diagnostics;
- sandbox policy and network mode;
- tests for redirects, DNS/host validation, proxy behavior, header handling,
  body limits, and credential leakage.

Determine whether code should be extracted into Core, reused as a library,
reimplemented as a host broker, or left in the toolbox. Pay attention to
dependency direction and whether application-layer checks are meaningful when
a provider process retains raw external network.

#### SaaS starter evidence

- `module-saas-starter-six-integrations/scripts/setup/provider-common.sh`
- `module-saas-starter-six-integrations/scripts/setup/stripe.sh`
- `module-saas-starter-six-integrations/scripts/setup/resend.sh`
- `module-saas-starter-six-integrations/scripts/setup/sentry.sh`
- relevant generic configuration files and service consumers;
- tests and dogfood documentation for these integrations.

Determine what behavior is actually shared, what is provider-specific, what
the scripts already do safely, and what the design assumes exists but does
not.

## Mandatory external sources

Use current official primary documentation. Do not rely on vendor blog
summaries, SEO articles, generated comparisons, or memory when behavior can
change.

### Plugin/lifecycle precedents

- Terraform Plugin Framework:
  <https://developer.hashicorp.com/terraform/plugin/framework>
- Terraform provider model:
  <https://developer.hashicorp.com/terraform/plugin/framework/providers>
- Terraform resource configuration:
  <https://developer.hashicorp.com/terraform/plugin/framework/resources/configure>
- Terraform state upgrades:
  <https://developer.hashicorp.com/terraform/plugin/framework/resources/state-upgrade>
- Terraform acceptance testing:
  <https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests>
- Terraform import:
  <https://developer.hashicorp.com/terraform/language/import>
- Pulumi provider protocol:
  <https://www.pulumi.com/docs/iac/guides/building-extending/providers/implementers/protocol-reference/>
- Pulumi resource providers:
  <https://www.pulumi.com/docs/iac/concepts/providers/>
- Crossplane providers:
  <https://docs.crossplane.io/latest/packages/providers/>
- Crossplane managed resources:
  <https://docs.crossplane.io/latest/managed-resources/managed-resources/>

Extract principles that matter to this proposal: configure/session state,
check/validate/diff/apply boundaries, import, refresh, state upgrade,
replacement, deletion policy, ownership, provider health, package revision,
acceptance testing, and unknown outcomes. Do not copy complexity without a
Codefly need.

### Stripe

- API keys and restricted keys:
  <https://docs.stripe.com/keys>
- idempotent requests:
  <https://docs.stripe.com/api/idempotent_requests>
- webhook endpoint API:
  <https://docs.stripe.com/api/webhook_endpoints>
- webhook local forwarding:
  <https://docs.stripe.com/webhooks>
- API versioning:
  <https://docs.stripe.com/api/versioning>

Verify:

- sandbox/test versus live detection;
- restricted key practicality for the proposed setup/runtime split;
- webhook create/read/update/delete and secret-return semantics;
- idempotency coverage, retention, and retry safety;
- event selection and API-version behavior;
- Stripe CLI forwarding lifecycle;
- resource discovery that can safely recover from a lost response;
- fields unsafe to include in plans, state, logs, or cassettes.

### Resend

- API introduction and rate limits:
  <https://resend.com/docs/api-reference/introduction>
- API key permissions:
  <https://resend.com/docs/api-reference/api-keys/create-api-key>
- webhook creation:
  <https://resend.com/docs/api-reference/webhooks/create-webhook>
- webhook overview:
  <https://resend.com/docs/webhooks/introduction>
- webhook verification:
  <https://resend.com/docs/webhooks/verify-webhooks-requests>
- CLI forwarding:
  <https://resend.com/docs/cli>

Verify:

- `full_access` versus `sending_access`;
- whether sending keys can be domain-restricted;
- webhook signing-secret presence across create/retrieve/list;
- contradictory documentation or response examples;
- at-least-once and out-of-order event delivery implications;
- webhook ID/deduplication semantics;
- default and endpoint-specific rate limits;
- domain verification prerequisites;
- local forwarding lifecycle and secret handling.

Do not resolve documentation ambiguity by assumption. Report it and recommend
how the plugin qualifies actual behavior through live acceptance.

### Sentry

- API authentication:
  <https://docs.sentry.io/api/auth/>
- API permissions:
  <https://docs.sentry.io/api/permissions/>
- project retrieval:
  <https://docs.sentry.io/api/projects/retrieve-a-project/>
- project client keys:
  <https://docs.sentry.io/api/projects/list-a-projects-client-keys/>
- project creation:
  <https://docs.sentry.io/api/projects/create-a-project-for-an-organization/>
- rate limits:
  <https://docs.sentry.io/api/ratelimits/>
- API and regional origins:
  <https://docs.sentry.io/api/>

Verify:

- current token model versus legacy API keys;
- minimum scopes for project discovery, client-key discovery, project
  creation, and release/source-map operations;
- whether `org:ci` is the correct build-purpose permission;
- public DSN classification and browser exposure;
- regional API routing and self-hosted base URLs;
- project creation prerequisites and whether it belongs in v0.1;
- rate-limit and error response handling.

## Review method

### Evidence rules

For every material claim:

1. cite Codefly source as `repository/path/file:line`;
2. cite external behavior using a direct official URL;
3. label an unverified statement as an inference or open question;
4. distinguish existing reusable behavior from required new implementation;
5. distinguish a design flaw from a normal implementation task.

Do not report hypothetical style preferences as blockers. A blocker must have
a concrete failure, security, compatibility, operability, or scope
consequence.

### Severity

Use exactly:

| Severity | Meaning |
| --- | --- |
| `BLOCKER` | Implementation must not start because the protocol/trust/product boundary is unsafe or likely to require a breaking redesign |
| `HIGH` | Must be resolved before the affected phase is accepted; may proceed elsewhere if independent |
| `MEDIUM` | Important correction or missing acceptance criterion that can be scheduled without changing the core protocol |
| `LOW` | Clarity, ergonomics, documentation, or future-hardening improvement |

Also record confidence:

- `HIGH`: directly proven by source or current primary documentation;
- `MEDIUM`: strong inference with named missing evidence;
- `LOW`: plausible concern requiring an experiment or vendor qualification.

### Review stance

- Try to falsify the proposal.
- Prefer the smallest coherent system.
- Do not preserve a component merely because substantial design text exists.
- Do not reject a component merely because implementation is difficult.
- Treat compromised provider code as an expected threat case.
- Treat remote APIs as non-transactional, rate-limited, evolving, and capable
  of returning malicious or unexpectedly large data.
- Treat local files, symlinks, environment variables, process arguments,
  logs, cassettes, state, and receipts as potential secret-exfiltration
  surfaces.
- Treat production, local dogfood, CI, Mind/orchestrator, and human CLI use as
  different authority contexts.
- Look for confused deputy problems at every host-mediated boundary.
- Look for time-of-check/time-of-use gaps between Observe, Plan, approval,
  Apply, broker request, and output commit.
- Look for partial success between every pair of remote/local effects.
- Look for places where documentation promises more than Codefly can enforce.

## Full adversarial review lanes

The final report must explicitly address every lane below, even if the
conclusion is “no material finding.”

### Lane 1: product need and abstraction choice

Determine:

- whether repeated scripts justify a first-class plugin system;
- whether `provider` is the right name and mental model;
- whether an environment binding is the right ownership scope;
- whether these integrations should instead be modules, toolboxes, services,
  secret providers, a CLI-only registry, or thin wrappers around Terraform or
  Pulumi;
- whether the proposed system is too broad for three integrations;
- whether it is too narrow to replace scripts meaningfully;
- whether founder UX improves after accounting for credentials, vendor
  dashboards, domain verification, public ingress, and production secrets;
- whether local dogfood and production lifecycle should share one protocol;
- whether observe-only and managed providers belong in one abstraction.

Provide the strongest alternative architecture and explain why it wins or
loses.

### Lane 2: Codefly reuse audit

For every row in the proposal's “What Codefly already has and should reuse”
table, classify it:

| Classification | Meaning |
| --- | --- |
| `REUSE AS-IS` | Existing public behavior satisfies the requirement |
| `REUSE WITH SMALL EXTENSION` | Local additive change, no semantic redesign |
| `EXTRACT/GENERALIZE` | Behavior exists but is trapped in a package/toolbox |
| `PARTIAL/UNWIRED` | Types/tests exist but production orchestration is absent |
| `NEW` | The proposal materially overstates existing capability |
| `DO NOT REUSE` | Existing primitive has the wrong trust or lifecycle model |

At minimum classify:

- agent kind, identity, routing, installation, release, and artifact
  verification;
- authenticated UDS and per-spawn process authentication;
- sandbox enforcement on supported operating systems;
- principals and production admission;
- permission manifests and ceiling PDP;
- gateway evaluation and scoped authorization;
- callback authorization and escalation;
- SaaS Starter PDP integration and revocation behavior;
- policy observability;
- structured failures;
- configurations and redaction;
- secret references and secret backends;
- endpoint/ingress resolution;
- Web toolbox guards;
- durable state and locking;
- execution receipts;
- doctor;
- `codefly agent ci`;
- cassette testing.

Flag duplicated implementations and incorrect dependency direction.

### Lane 3: agent-kind and packaging architecture

Challenge:

- whether `PROVIDER=6` and `codefly:provider` fit existing enums and
  compatibility rules;
- path/routing assumptions across Core and CLI;
- build, package, release, local-latest, OCI, GitHub, Nix, SBOM, and artifact
  digest behavior;
- whether provider manifests belong beside `agent.codefly.yaml`;
- manifest/artifact signature and tamper risks;
- version pinning and lock-file needs;
- runtime catalog versus packaged manifest validation;
- upgrade/downgrade behavior;
- uninstall behavior with active bindings/state;
- provider SDK portability beyond Go.

Find every hardcoded agent-kind switch that the roadmap must include.

### Lane 4: protocol coherence and evolvability

Review:

- RPC boundaries and whether a stateful Configure phase is actually needed;
- whether `Observe` and `Plan` can be cleanly separated for all three
  providers;
- whether Plan is genuinely deterministic;
- whether Apply contains too much provider-specific orchestration;
- request/response size bounds;
- cancellation, deadline, progress, and checkpoint semantics;
- diagnostics and structured failure mapping;
- feature/capability negotiation;
- protocol, manifest, provider-state, and output-contract versioning;
- forward/backward compatibility;
- `UpgradeState` sequencing and rollback;
- import, replace, delete, and manual-action modeling;
- pagination and multiple remote matches;
- how unknown provider fields survive upgrades;
- whether outputs belong in Plan, Apply, Observe, or a separate projection
  call;
- whether Doctor duplicates Observe;
- whether broker request descriptions need their own protocol.

Identify decisions that must be frozen in v0 and decisions that should remain
capability-negotiated.

### Lane 5: permission, principal, and approval architecture

Verify the exact status and suitability of:

- `policy.Principal`;
- `manager.WithPrincipal`;
- `policy.PermissionPolicy`;
- `policy.NewCeilingPDP`;
- `policy.GatewayEvaluator`;
- v1 HMAC and v2 Ed25519 scoped authorization;
- request/catalog/audience/resource bindings;
- use counts and replay protection;
- `manager.WithPermissionsCallback`;
- `manager.WithProductionAdmission`;
- escalation and grantor attribution;
- `policy.SaasPDP`;
- cache and revocation behavior;
- fail-closed behavior;
- shadow, allow-all, break-glass, and local/test bypasses.

Challenge:

- the proposed canonical actions and resource syntax;
- whether manifest prefix matching is sufficiently expressive;
- how prospective resource IDs are authorized before creation;
- how exact plan actions map to individual HTTP requests;
- whether read-only operations also require remote-data authorization;
- whether configuration and secret-sink writes require separate actions;
- whether the provider callback can lie about action/resource;
- whether the host broker must independently derive rather than trust those
  claims;
- whether approval remains valid across observation refresh, account changes,
  endpoint changes, or agent upgrades;
- how CI and Mind delegation differ from a human CLI;
- whether permission checks occur again after a long operation or revocation;
- what evidence is safe to put in policy logs and receipts.

Construct explicit allow/deny truth tables. Look for any path where one broad
grant yields ambient remote CRUD.

### Lane 6: sandbox and process isolation

Verify:

- actual sandbox backends on macOS and Linux;
- which backends production admission accepts;
- loopback behavior needed for gRPC/UDS;
- filesystem read/write defaults;
- inherited environment variables and file descriptors;
- proxy variables, DNS, subprocesses, and executable access;
- whether a provider can invoke `curl`, vendor CLIs, or another process;
- whether UDS paths expose more host APIs than intended;
- whether the provider can access another provider's callback or state;
- crash cleanup and orphaned process/socket behavior.

Prove or refute the claim that a compromised provider cannot reach an
unapproved origin or read workspace/secrets.

### Lane 7: HTTP broker design

Determine whether the broker is viable for Stripe, Sentry, and Resend without
exposing raw credentials or crippling provider semantics.

Review:

- ownership and package location;
- request-description schema;
- exact-origin and path-template matching;
- URL parsing, normalization, encoded paths, query parameters, and user info;
- DNS rebinding, redirects, IPv4/IPv6 literals, localhost aliases, and proxy
  environment variables;
- TLS verification and custom/self-hosted Sentry CAs;
- request headers, cookies, compression, streaming, and multipart bodies;
- body size and response decompression bombs;
- retries versus vendor idempotency;
- rate-limit normalization;
- credential injection and header placement;
- preventing caller-provided auth/proxy headers;
- request/response logging and diagnostic sanitization;
- pagination;
- provider API version headers;
- cassette record/replay determinism;
- whether provider-native SDK behavior can be represented;
- escape-hatch criteria and security review.

Attack the broker as a confused deputy: assume the provider can send any
syntactically valid request descriptor and tries to spend a valid credential
outside the approved plan.

### Lane 8: credentials and secret lifecycle

Trace every credential byte and secret byte from acquisition to destruction:

- hidden prompt or file input;
- secret reference resolution;
- resolver cache;
- credential handle minting;
- broker injection;
- provider request;
- vendor response;
- one-time capture;
- secret sink preparation, persistence, commit, and rollback;
- configuration projection;
- runtime/build injection;
- logs, diagnostics, state, receipts, support bundles, cassettes, crash dumps,
  and process environment.

Challenge:

- whether credential handles are genuinely opaque;
- whether the provider can infer or retrieve the underlying value;
- whether purpose separation is enforceable rather than advisory;
- whether a powerful management credential can accidentally become runtime;
- how already-existing runtime keys/references are projected host-to-host;
- how key rotation works;
- how revoked/expired keys are diagnosed;
- how write-only provider secrets are replaced;
- whether fingerprints leak useful information;
- what happens if the sink succeeds and later projection fails;
- what happens if remote creation succeeds and sink persistence fails;
- how local owner-only files compare with a managed production sink;
- whether 1Password's current resolver is read-only;
- which writable production sink should be first.

The final report must include a data-flow diagram or structured trace for:

1. a Stripe management request;
2. Stripe webhook signing-secret creation;
3. an existing Resend sending key projected to runtime;
4. Sentry setup token used to discover a public DSN;
5. Sentry build token projected only to an authorized build consumer.

### Lane 9: one-time secret capture

Independently attack the proposed declarative JSON Pointer capture:

- Can a malicious provider select a broader or different field?
- Can a malicious API return a shape that causes the wrong field to be
  captured?
- Are duplicate keys, arrays, numbers, nulls, encodings, or huge values safe?
- Is content-type verification sufficient?
- Can the provider observe the original raw body through another channel?
- Does the broker parse before logging, metrics, errors, or cassette capture?
- Can a redirect move capture to another origin?
- Can an unsuccessful response accidentally capture an attacker-controlled
  error field?
- Is the sink target bound to the approved plan?
- How is a captured secret referenced in the provider's canonical response?
- Can replay fixtures substitute deterministic fake secrets without weakening
  the live boundary?
- Does immediate sink commit conflict with overall Apply compensation?
- Is JSON-only capture sufficient for v1?

Recommend a safer alternative if declarative capture cannot be made robust.

### Lane 10: state, locking, ownership, and drift

Review:

- state address and workspace canonicalization;
- symlinked or moved workspaces;
- local versus team/remote state;
- directory/file permissions;
- bbolt or alternative choice;
- locking across CLI processes and crashes;
- state schema and backups;
- operation versus attempt identity;
- prior/observed/desired digests;
- state corruption handling;
- agent upgrade/downgrade;
- remote identity and ownership;
- import/adoption;
- drift outside provider-owned fields;
- resources shared across bindings;
- deletion protection;
- state loss and reconstruction;
- secrets known only by reference/fingerprint;
- support bundle safety.

Construct failure timelines for:

- process crash before remote mutation;
- crash after request send but before response;
- crash after response but before checkpoint;
- remote success plus secret-sink failure;
- state commit plus projection failure;
- projection success plus terminal receipt failure;
- concurrent plans and applies;
- stale approval;
- remote deletion outside Codefly;
- remote manual edit;
- provider upgrade during an operation.

Determine whether the proposal's `UNCERTAIN` state and reconciliation are
sufficient and which cases require manual intervention.

### Lane 11: configuration projections and contracts

Review:

- generic `billing@1`, `email@1`, and `error-tracking@1` contract ownership;
- compatibility/version negotiation;
- public versus secret classification;
- browser exposure;
- value provenance;
- stable/computed/write-once semantics;
- ownership conflicts with user-maintained configurations;
- atomic multi-file writes;
- path and symlink defenses;
- Git-ignore verification;
- file modes;
- compare-and-swap against approved output digests;
- validation and rollback;
- multiple providers targeting one contract;
- switching providers;
- disconnect versus destroy;
- runtime/build/service-specific consumers;
- whether environment-variable projection remains the right abstraction.

Verify that provider setup does not bypass normal Codefly SDK/configuration
injection.

### Lane 12: endpoints, ingress, and local forwarding

Review:

- exact endpoint identity and path validation;
- dynamic local port resolution;
- endpoint readiness timing;
- public ingress availability before provider Apply;
- callback URL stability across restarts;
- HTTPS requirements;
- local-direct versus local-forwarded versus public versus existing;
- WorkOS redirect behavior as a counterexample;
- Stripe and Resend CLI forwarders;
- supervision, lifecycle, restart, logs, and secret capture for forwarders;
- whether a companion process needs its own principal, permissions, and
  receipts;
- remote webhook events arriving before the application is ready;
- webhook retries after local restart;
- multiple developers sharing one sandbox account;
- CI dogfood without public ingress.

Reject any design that asks users or providers to hardcode port `42152` or any
other generated Codefly port.

### Lane 13: Stripe reference design

Validate the proposed Stripe v0.1 behavior:

- observe account and sandbox/live mode;
- enforce local-live guard;
- explicit API version;
- observe/create/update/import/destroy webhook endpoint;
- stable event ordering and comparison;
- duplicate endpoint recovery;
- idempotency-key derivation and retry window;
- signing-secret capture;
- restricted runtime key practicality;
- management/runtime shared-key local escape hatch;
- Stripe CLI forwarding;
- product-side checkout, portal, subscription, webhook verification, and
  reconciliation dogfood;
- cleanup and dedicated sandbox-account isolation.

Identify which fields Codefly owns and which remote changes it must ignore.

### Lane 14: Sentry reference design

Validate:

- regional and self-hosted origins;
- setup token scopes;
- project and client-key observation;
- public DSN classification;
- backend/browser DSN projection;
- environment and release fields;
- build token with `org:ci`;
- absence of setup token from runtime;
- rate limits;
- project creation as v0.1 versus later;
- controlled frontend/backend errors and source-map/release dogfood;
- no-webhook and observe/project-only lifecycle.

Determine whether Sentry actually validates the abstraction or is so simple
that it hides lifecycle deficiencies.

### Lane 15: Resend reference design

Validate:

- management full-access key;
- sending-only runtime key and optional domain restriction;
- domain verification observation;
- webhook lifecycle;
- signing-secret ambiguity;
- Svix verification and message IDs;
- at-least-once and out-of-order delivery;
- rate limits;
- CLI forwarding;
- sending and webhook dogfood;
- behavior when a webhook exists but its signing secret cannot be retrieved.

Require an explicit capability/result model for uncertain secret
retrievability rather than a hardcoded documentation assumption.

### Lane 16: error model, rate limits, retries, and reliability

Review:

- mapping vendor errors into Codefly structured failures;
- authentication versus authorization versus missing resource;
- retryable versus permanent errors;
- rate-limit metadata;
- safe request IDs;
- redacted provider errors;
- retry budgets and backoff;
- idempotent versus non-idempotent retries;
- deadlines and cancellation;
- partial pagination;
- API outages;
- provider documentation drift;
- local offline behavior;
- doctor latency and boundedness.

Ensure no generic retry layer duplicates mutations or turns ambiguous outcomes
into false success.

### Lane 17: CLI and operator experience

Review the proposed commands:

```text
codefly provider list
codefly provider show
codefly provider schema
codefly provider add
codefly provider setup
codefly provider validate
codefly provider observe
codefly provider plan
codefly provider apply
codefly provider doctor
codefly provider import
codefly provider disconnect
codefly provider destroy
```

Challenge:

- whether this is too many commands;
- whether setup is sufficiently predictable for founders;
- interactive versus CI behavior;
- plan-file portability and secret safety;
- human and JSON output;
- approval UX;
- manual-action reporting;
- remediation;
- discoverability;
- provider installation/version selection;
- multi-binding operations;
- exit codes;
- dry-run semantics;
- support bundles;
- dogfood workflow.

Propose a smaller command surface if it preserves automation and debugging.

### Lane 18: doctor, observability, metrics, and supportability

Review:

- local static checks;
- remote read-only checks;
- credential scope diagnostics;
- account/mode visibility;
- drift;
- endpoint/ingress readiness;
- state and lock health;
- provider agent and protocol compatibility;
- secret sink health;
- rate-limit visibility;
- receipt correlation;
- permission allow/deny/approval/fail-closed metrics;
- provider operation latency and outcome metrics;
- cardinality and secret leakage;
- support bundle contents;
- remediation stability.

Ensure Doctor cannot mutate or become an unbounded collection of expensive
provider calls.

### Lane 19: testing and conformance

Evaluate whether the proposed tests actually qualify the system:

#### Pure deterministic tests

- schema validation;
- normalization;
- canonicalization;
- diff/plan;
- state upgrade;
- permission matrix;
- ownership and deletion policy;
- projection.

#### Recorded real API interactions

- broker enforcement remains active during replay;
- cassettes are sanitized before disk;
- request matching is deterministic;
- no live fallback;
- vendor errors/rate limits/pagination are represented;
- secret capture uses safe substitutions.

#### Provider conformance

- manifests and runtime catalogs;
- offline RPCs truly have no network;
- Apply cannot execute undeclared actions;
- idempotency and checkpoints;
- import/disconnect/destroy;
- cancellation and crash recovery;
- output/secret invariants;
- production-admission failures.

#### Live acceptance

- explicit opt-in;
- dedicated test accounts/projects/domains;
- credential scope qualification;
- deterministic cleanup;
- no production account;
- API behavior that documentation leaves ambiguous.

#### Full starter dogfood

- real SaaS Starter backend and database;
- Codefly-provided configurations and endpoints;
- tiny React shell with library-owned onboarding logic where relevant;
- Stripe billing lifecycle;
- Resend email/webhook lifecycle;
- Sentry frontend/backend/release lifecycle;
- permission and receipt visibility.

Determine what “NO MOCKS” should mean. Reject false confidence from either
hand-written fake provider clients or brittle tests that depend on live vendor
availability for every run.

### Lane 20: migration and compatibility

Review:

- coexistence with current scripts;
- plugin-versus-script ownership conflicts;
- import/adoption of script-created resources;
- safe detection of existing configuration;
- rollback to scripts;
- agent/protocol rollout;
- output contract compatibility;
- old CLI behavior;
- existing workspaces;
- documentation and deprecation;
- whether scripts can become thin shims without recursion or different
  semantics;
- what happens when a provider plugin is unavailable.

No migration may silently adopt or delete a remote resource.

### Lane 21: production readiness

Identify which features are mandatory before any production mutation:

- artifact digest/signature locking;
- remote encrypted team state and locking;
- writable managed secret sink;
- production ingress;
- non-local account guard;
- principal and PDP availability;
- approval;
- support and recovery;
- provider version lifecycle;
- audit/receipt retention;
- disaster recovery;
- key rotation;
- multi-user concurrency.

Separate “local dogfood ready” from “production observe ready” and “production
mutation ready.” Reject a single vague “ready” milestone.

### Lane 22: roadmap and GitHub issue decomposition

Determine whether the proposed five-issue structure is executable.

For each proposed issue:

- identify owning repository or repositories;
- list hard dependencies;
- identify work that can run independently;
- identify the contract that must be frozen before parallel work;
- identify acceptance evidence;
- flag an issue that is too large to review safely;
- avoid splitting inseparable protocol/host work merely for smaller issue
  count;
- avoid combining provider-specific work that can proceed independently.

Recommend the fewest self-contained issues that preserve genuine independent
work. If five is wrong, give the replacement number and structure.

### Lane 23: future-provider pressure

Without expanding v1 implementation, test the abstraction mentally against:

- WorkOS;
- PostHog;
- Cloudflare Turnstile;
- generic OTLP/observability backends;
- feature-flag providers;
- object storage;
- managed Postgres;
- a provider requiring OAuth browser consent;
- a provider with asynchronous provisioning;
- a provider with multiple regional API origins;
- a provider that cannot retrieve secrets after creation;
- a provider with no management API.

The goal is to find protocol assumptions that would immediately break, not to
add every future capability.

### Lane 24: contradiction and completeness audit

Search the entire proposal for:

- the same responsibility assigned to both host and provider;
- an operation described as offline in one section and networked elsewhere;
- a secret described as both public and sensitive;
- deletion or ownership rules that conflict;
- output timing inconsistencies;
- state that is required but not persisted;
- rollback promises impossible across remote/local systems;
- permission names inconsistent with examples;
- resource syntax inconsistent with manifest examples;
- roadmap deliverables absent from architecture;
- acceptance criteria that require postponed infrastructure;
- claims of existing Codefly behavior that are actually future work;
- vendor behavior contradicted by current official docs.

List factual corrections separately from architectural findings.

## Required adversarial scenarios

The report must walk through at least these scenarios:

1. A malicious provider advertises fewer capabilities than its binary tries to
   exercise.
2. A provider changes its runtime catalog after approval.
3. A provider asks the broker to call an allowed origin with an unplanned
   destructive path.
4. A provider supplies its own `Authorization` header.
5. A provider tries an HTTP redirect to a credential-stealing origin.
6. A provider tries proxy environment variables or a subprocess to bypass the
   broker.
7. A provider claims a different principal in the permission callback.
8. A human's permission is revoked after Plan but during Apply.
9. A CI principal reuses a scoped authorization in another environment.
10. A Mind agent delegates authority beyond the human grant.
11. A local Stripe configuration points at a live account.
12. A Stripe webhook create succeeds but the response is lost.
13. A Stripe webhook secret is captured but configuration projection fails.
14. The secret sink fails after the remote webhook exists.
15. Resend returns a different signing-secret shape than documentation.
16. A Resend sending-only key is accidentally used for management.
17. A Sentry setup token is accidentally projected as a runtime build token.
18. A Sentry DSN is over-classified as secret and becomes unavailable to the
    browser, or under-classified and exposes a true secret.
19. Two developers apply the same binding against one sandbox account.
20. Two CLI processes apply different plans concurrently.
21. A workspace moves to another filesystem path.
22. Provider state is deleted while remote resources remain.
23. A user manually edits the remote webhook.
24. A user deletes the remote webhook outside Codefly.
25. A provider agent is upgraded between Plan and Apply.
26. An old agent reads newer state.
27. A cassette accidentally contains a live token or webhook secret.
28. The policy backend is unavailable.
29. The permission callback socket is unavailable mid-operation.
30. A crash occurs after the last remote effect but before the success receipt.
31. Public ingress changes after the webhook plan is approved.
32. The local forwarded webhook arrives before the backend is ready.
33. Disconnect removes local projection but runtime processes still hold old
    credentials.
34. Destroy sees an unmanaged remote resource with the same URL.
35. A provider returns a very large or malicious JSON response.
36. A provider API begins returning a new field that looks secret.
37. A provider needs pagination to determine whether an existing resource is
    unique.
38. A retry crosses the vendor's idempotency retention window.
39. A production secret is successfully persisted but state commit fails.
40. Doctor runs with a principal allowed to inspect configuration but not
    provider account metadata.

For each, state expected behavior, enforcement point, durable evidence, and
remaining recovery action.

## Experiments the reviewer may require

Do not implement the provider system, but recommend or run bounded read-only
experiments when source inspection cannot answer a protocol question:

- compile/run existing policy tests through approved Codefly commands;
- demonstrate current sandbox network/filesystem behavior;
- inspect actual agent launch environment and UDS permissions;
- replay existing Web toolbox redirect/header tests;
- inspect current Stripe/Resend/Sentry API schemas;
- make an opt-in read-only API request only if credentials and authorization
  are already intentionally available to the review environment;
- prototype no production mutation.

Do not expose or copy credential values. Do not create/delete vendor resources
as part of this review unless separately and explicitly authorized.

## Required output contract

Return one Markdown document with these sections in this order.

### 1. Executive verdict

Include:

- `APPROVE`, `APPROVE WITH REQUIRED CHANGES`, or `REJECT/REFRAME`;
- a five-to-ten sentence rationale;
- whether GitHub issue creation may proceed;
- the exact preconditions if it may not.

### 2. Decision scorecard

Score each from 1 to 5 and justify:

| Dimension | Score | Evidence-based justification |
| --- | ---: | --- |
| Product value | | |
| Abstraction fit | | |
| Codefly reuse | | |
| Permission model | | |
| Process/network isolation | | |
| Secret lifecycle | | |
| Protocol coherence | | |
| State and recovery | | |
| Provider API fit | | |
| Local dogfood | | |
| Production readiness path | | |
| Testability | | |
| Operability/UX | | |
| Migration | | |
| Roadmap executability | | |

### 3. Blocking findings

Every finding uses:

```text
ID: ADV-###
Severity: BLOCKER | HIGH | MEDIUM | LOW
Confidence: HIGH | MEDIUM | LOW
Area:
Claim challenged:
Evidence:
Failure or attack sequence:
Impact:
Required change:
Protocol-breaking if deferred: yes | no
Affected phase/issues:
Acceptance test:
```

Do not put non-blockers in this section.

### 4. High, medium, and low findings

Group by severity. Use the same finding shape.

### 5. Factual corrections

List inaccurate Codefly capability claims, stale vendor claims, inconsistent
examples, and broken references independently of severity findings.

### 6. Codefly reuse matrix

Use the classifications from Lane 2. Include exact source evidence and the
smallest required extension.

### 7. Trust-boundary and permission analysis

Include:

- a data-flow/trust-boundary diagram;
- principals and delegation;
- permission intersection;
- action/resource vocabulary;
- approval and revocation;
- broker and credential-handle enforcement;
- bypass analysis;
- safe audit evidence;
- explicit allow/deny matrix.

### 8. Credential and secret data-flow analysis

Include all five traces required by Lane 8 and identify every process or
persistence surface that can see raw bytes.

### 9. Lifecycle and failure analysis

Include:

- Validate/Observe/Plan/Apply/Project/Doctor assessment;
- partial/uncertain outcome table;
- state/ownership/import/delete analysis;
- crash and concurrency timelines;
- recovery behavior.

### 10. Provider-by-provider validation

Separate Stripe, Sentry, and Resend. For each:

- confirmed API capabilities;
- disproven assumptions;
- credential roles and minimum scopes;
- managed and observed resources;
- output classification;
- local dogfood;
- live acceptance requirements;
- unresolved vendor ambiguity.

### 11. Testing gap analysis

Map every major invariant and required adversarial scenario to:

- pure test;
- recorded real-API cassette;
- provider conformance;
- live vendor acceptance;
- starter dogfood.

Flag invariants with no credible test.

### 12. Required design changes

Provide an ordered, minimal change list:

- change before issue creation;
- change during foundation;
- defer safely;
- reject.

Include replacement text or protocol sketches where ambiguity would otherwise
remain.

### 13. Recommended roadmap and GitHub issue map

Give the fewest independently executable issues. For each:

- title;
- owning repository/repositories;
- objective;
- dependencies;
- contract inputs;
- deliverables;
- permission/security invariants;
- tests;
- dogfood evidence;
- acceptance criteria;
- explicit non-goals.

State which issues may run in parallel.

### 14. Open decisions

For each unresolved decision:

- options;
- recommendation;
- consequence;
- latest responsible decision phase;
- experiment/evidence needed.

### 15. Coverage appendix

List:

- every local file inspected;
- commands/tests run;
- official external pages consulted;
- review lanes with no finding;
- areas not verified and why.

## Review completeness gate

The review is incomplete unless:

- the full proposal was read;
- all 24 review lanes are addressed;
- all 40 adversarial scenarios have an expected outcome;
- all mandatory Codefly primitives are classified by actual implementation
  status;
- all three initial providers are checked against current official docs;
- at least one serious alternative architecture is evaluated;
- permission and secret data flows are explicit;
- every `BLOCKER` and `HIGH` finding has concrete evidence and an acceptance
  test;
- facts, inferences, and preferences are distinguished;
- local dogfood and production readiness are separated;
- the recommended issue decomposition identifies repositories and
  dependencies;
- the reviewer states whether issue creation may proceed.

## Important constraints

- Do not implement the provider system.
- Do not edit the proposal while reviewing it.
- Do not create or mutate GitHub issues.
- Do not create, update, or delete provider-side resources.
- Do not print, copy, or persist credentials.
- Preserve unrelated worktree changes.
- Use `rg`/`rg --files` for repository search.
- Follow the workspace `AGENTS.md`.
- Use Codefly commands for builds/generation/tests when that file requires
  them.
- Prefer primary sources.
- Keep findings reproducible and evidence-backed.

The output of this review is a decision artifact. It should be direct,
skeptical, and specific enough that the proposal can either be corrected and
converted into implementation issues or rejected before expensive work
begins.
