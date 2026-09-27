# Runnable binding delivery

How a prepared Runnable binding reaches the process that calls it, and how any
large workspace configuration value reaches a process at all. Two parts, and
they are independent: a prepared binding carries only what a call needs, and a
configuration value too large for a process environment is delivered as a file
whose path the environment carries.

Status: design of record for codefly-dev/core#670. `docs/runnable.md` owns the
package and binding contracts themselves; this page owns their delivery.

## What a prepared binding is

For an operation derived from an owner's service method or route (`docs/runnable.md`,
"Operations derived from a service method"), `codefly generate runnable-bindings`
*prepares* one value per operation for one environment. Composition wires the
owner's resolved address into it, so the caller never names an owner. The value
is written to the `runnable-bindings` workspace configuration group, keyed
`<MODULE>__<OPERATION>`, and a caller that declares that group as a workspace
configuration dependency receives it like any other group. No module names
another.

What a call needs is five facts — *this endpoint, this method, this identity,
this input, this policy* — and they have two lifetimes. The four fixed when the
operation is installed are the prepared value; identity and input belong to one
call and are no part of an installation fact.

The value is a `codefly.runnable.v0.PreparedBinding`, schema
`codefly.runnable-prepared/v3`, delivered as its canonical proto3 JSON
(`runnable.EncodePrepared`):

| Field | What it is |
| --- | --- |
| `operation` | the owner coordinates (module, service, endpoint) and the operation **as the owner spells it**: `/pkg.Service/Method` or `POST /path`. This is the audit identity, and what an effect receipt is keyed by |
| `call` | where one call is sent: the resolved `address`, and a typed route — `connect` (a procedure POSTed as JSON on the owner's Connect endpoint) or `rest` (the owner's own verb and path with the plain JSON body) |
| `contract` | the bounded input and output schema, carried whole |
| `contract_digest` | `sha256:<hex>` over that contract's canonical form (`runnable.ContractDigest`) |
| `policy` | the `codefly.runnable.v0.Operation` the owner declared — the attempt budget, and the authority: audience, invoke and lookup scopes |

It is a proto message rather than a hand-written JSON document because it is a
contract between core, the CLI, a caller and the SDKs of several languages:
protovalidate states its bounds once, every language generates a reader, and a
later field is a field on one schema rather than a third parser.

## What it does not carry, and why that is not a size optimization

An owner is called with **JSON**: a gRPC owner on its Connect endpoint
(`POST /pkg.Service/Method`, `application/json`), a REST owner on its real
route. Protobuf descriptors existed so a generic caller could resolve a method
for a *binary* gRPC call, so calling with JSON removes the reason for them and
not merely their bulk. The form that carried them embedded an owner's whole
descriptor closure per operation — about 100 KB decoded, 138 KB as the base64 a
value holds — and five operations of one endpoint were 689 KB of the same bytes
five times over. A prepared value is now a few hundred bytes to a few kilobytes:
the bounded contract is the size of the operation's own field list.

The canonical `RunnablePackage` and `RunnableBinding` are not carried either.
They are the immutable installation facts of a *launched* release
(`docs/runnable.md`); what a caller of a derived operation does with them is
exactly what the five fields above state directly.

## What a caller verifies

`runnable.DecodePrepared` reads a value strictly — a field the schema does not
declare is refused rather than dropped, because an unknown field in an
installation fact is either another schema's value or one the reader would have
had to act on. `runnable.VerifyPrepared` then holds it to four things:

- **the wire contract's own bounds**, through protovalidate;
- **the contract digest covers the contract delivered with it.** An owner that
  republishes a changed contract derives another digest, so the value prepared
  for the contract it used to publish is refused rather than called with a
  payload shaped for a contract nobody serves. `runnable.ContractDigest` is
  prefixed by `ContractDigestFormatV1`, so a change in how the digest is
  computed changes every digest instead of colliding with the previous format;
- **the route is the operation the value names.** A `connect` procedure must be
  the operation's spelling, and a `rest` verb and path must spell it. The two
  are checked together because they had one cause: a form that read one string
  as both the audit identity and the dial target refused every REST operation,
  since `POST /path` is not a method path;
- **the policy is inside the bounds an installation enforces**, by reading it
  back as the `OperationSpec` core already validates. The retry vocabulary
  follows from the route — `google.rpc.Code` names for `connect`, HTTP statuses
  for `rest` — so it is never restated, and a policy that installs is a policy
  that would have generated.

Everything fails closed, and the error names the key, the operation or the
digest — never a value.

## How a value is delivered: environment or file

A workspace configuration value has always reached a process as an environment
variable, set at process start: `CODEFLY__WORKSPACE_CONFIGURATION__<GROUP>__<KEY>`
(and the secret, service and document namespaces beside it). Two platform
limits make that the wrong carrier for a large value:

- Linux refuses `execve` when one `KEY=VALUE` string is longer than
  `MAX_ARG_STRLEN`, 128 KiB (`E2BIG`).
- macOS caps the environment plus arguments at about 1 MiB (`ARG_MAX`).

So a flat configuration value is delivered **by file** when it is larger than
`resources.FileCarrierThreshold` (32 KiB), by every emitter, automatically
(`EnvironmentVariable.File`). The value's own key is then absent from the
environment, and
`resources.FileCarrierKey(key)` — `CODEFLY__FILE__` followed by the key without
its `CODEFLY__` prefix — carries the absolute path of a file whose content is
the value, byte for byte. The rule is on the size of the value, never on the
name of a group, so no emitter needs to know what the value is.

| Where | Who writes the file | Where the file is |
| --- | --- | --- |
| a native or Nix process | the core runner, at process start | a per-process `0700` directory, `0600` files, removed when the process ends |
| a Docker container | the core Docker runner, between creating the container and starting it (and before an exec) | copied into the container at `/codefly/configuration`, owned by the user the container runs as, directory `0500` and files `0400`; nothing stays on the host |
| Kubernetes | the core Kustomize render | ConfigMap `cmf-<service>` (public, `defaultMode: 0444`) and Secret `secretf-<service>` (secret, local-apply only, `defaultMode: 0440` with the pod's `fsGroup`) mounted read-only at `/var/run/codefly/configuration` and `/var/run/codefly/secret-configuration` |

### Who can read a delivered file

A file is readable by the user the workload runs as, and a secret file by
nobody else:

- **Native and Nix.** The process runs as the user that wrote the file.
- **Docker.** A bind mount keeps host ownership: a `0600` file is unreadable
  to a container running as another non-root user, and a file readable on the
  host is readable by every host user. So the runner copies the files into the
  container instead, owned by its user. That user is the container's `User`, or
  the image's; a name is resolved through the container's own `/etc/passwd`
  and `/etc/group`. A user that cannot be resolved refuses the delivery: a
  value whose reader nobody can name is not delivered.
- **Kubernetes.** Volume files are owned by root. A public file is `0444`,
  readable whatever user the container runs as; it is public configuration.
  A secret file is `0440`, and its group is the pod's `fsGroup`, which the
  kubelet adds to every container's groups. The render keeps a declared
  `fsGroup`. Otherwise it sets it to the group the pod runs as (its
  `runAsGroup`, else its `runAsUser`, from the pod or from every container
  alike). A pod that declares neither is refused rather than given a
  world-readable secret.

Two carriers keep their values inline. A configuration document is already
bounded at 64 KiB by its envelope (`docs/configuration-contract.md`), under the
per-string limit, and counts toward the total below. An `--output-env` export
is itself a file the SDK loads in-process (`LoadRuntimeEnvironmentFile`), not
an `exec` environment, so no platform limit applies to it.

Readers do not change. `resources.ResolveFileCarriers` resolves every
`CODEFLY__FILE__*` entry of an environment into the value it carries, and the
SDKs call it when they snapshot the environment (`sdk-go`'s
`LoadEnvironmentVariables`) and on a direct lookup, so `WorkspaceValue`,
`WorkspaceConfiguration`, `WorkspaceSecret` and configuration documents return
the value whichever carrier delivered it.

## Lifecycle

- **Fixed at process start.** The carrier is chosen and written before the
  process starts, and the SDK reads a file carrier once and keeps it, exactly
  as it keeps an environment value (codefly-dev/cli#740). A Kubernetes
  ConfigMap volume that the kubelet later refreshes does not change what a
  running process reads; a new value reaches a process by restarting it, as it
  always has.
- **Regeneration.** `codefly generate runnable-bindings` rewrites the group
  whole: every prepared value of the workspace and only those. `--check`
  compares the whole file, so a value prepared against a contract the owner no
  longer publishes is drift at generation time, before any caller reads it.
- **One schema, no transition window.** A reader accepts exactly
  `codefly.runnable-prepared/v3`. A value of an earlier schema, or carrying a
  field this schema does not declare, is refused rather than partly read — and a
  field a caller must not proceed without therefore arrives as a new schema,
  never as one an older reader tolerates by ignoring it. Regenerating the group
  is what moves a workspace forward, and a caller that refuses at installation
  fails before it admits any work.

## Failure modes

Everything fails closed, and as early as the information exists:

- **Plan and render time.** `resources.CheckProcessEnvironment` refuses a
  process environment in which any one `KEY=VALUE` string reaches 128 KiB, or
  in which the Codefly-emitted carriers together exceed
  `resources.MaxCarriedEnvironmentBytes` (512 KiB), naming the largest keys and
  never a value. Runners call it before starting a process, so the failure is a
  Codefly error rather than an `E2BIG` from the kernel. A carrier marked for
  file delivery that reaches a path unable to deliver files is refused rather
  than silently inlined. The Kubernetes render applies the same check to the
  ConfigMap and Secret it loads as the container's environment, and refuses a
  file ConfigMap or Secret above 1 MiB, the API server's object limit.
- **Read time.** A file carrier whose path is relative, is not a regular file,
  exceeds `resources.MaxFileCarrierBytes`, or cannot be read is an error, not
  an absent value: an unreadable credential never reads as an unset one. A key
  delivered both inline and by file is refused as two sources for one fact.
- **Install time.** A contract digest that does not cover the contract delivered
  with it, a route that is not the operation the value names, a policy outside
  the installation bounds, an unknown field and an earlier schema are each
  refused before any operation is installed (*What a caller verifies*).

## Security

- Host files are written `0600` in a `0700` directory owned by the running
  user, and are removed with the process that read them. A container's files
  are copied into it, owned by its user, and go with the container.
- A secret delivered by file is read with the same rule the SDK applies to an
  `--output-env` export: a secret file accessible by others is refused.
  Kubernetes secret volumes are mounted with `defaultMode: 0440` and the
  pod's `fsGroup` (see *Who can read a delivered file*), which grants nothing
  to others.
- A secret value never enters a restricted render in plaintext: the restricted
  profile carries no secret values at all (`docs/configuration-contract.md`),
  so there is nothing to file-deliver; its secrets reach the workload through
  the declared secret references. The file Secret exists only in the
  local-apply profile, which already carries secret values.
- Values are never logged. Errors name keys and sizes, never content; a file
  carrier's path is not a secret, its content may be.

## Decisions, and what was rejected

- **Size decides the carrier, not the group.** Delivering a *declared* group
  (such as `runnable-bindings`) by file regardless of size was considered and
  rejected: it would make core name a group, and a small value gains nothing
  from a file. Every value above the threshold goes by file whoever declared
  it, so a group that grows past the threshold later needs no declaration.
- **Descriptors, and sharing them by digest, were both removed.** An earlier
  design shared one descriptor set per owner endpoint, referenced by digest, so
  that N operations on one endpoint carried one copy instead of N. It made the
  group smaller and left the reason for descriptors in place. Calling an owner
  with JSON removes that reason, so there is no set to share, no
  `DESCRIPTOR_SET__` key space and no digest to resolve a set by — and the
  bounded contract, which a caller does need, is small enough to carry whole.
- **The bounded contract travels in the value, not by reference.** A digest-only
  value would be smaller by a kilobyte and would oblige every caller to fetch a
  contract from somewhere before it could validate a payload. The digest is
  there to make drift visible, not to stand in for the contract.
- **The audit spelling and the dial target are separate fields.** One string
  serving as both is what refused every REST operation. Keeping the owner's own
  spelling matters independently: it is what an effect receipt is keyed by.
- **Core owns the reader's rules.** `DecodePrepared`, `VerifyPrepared` and
  `ResolveFileCarriers` are the rules; a caller and an SDK call them rather than
  restating them, so they cannot drift.

## Rollout order

Every part fails closed against every other, so the order is one of
usefulness, never of safety:

1. core (this document, the types, the carriers and their emitters);
2. `sdk-go`: `LoadEnvironmentVariables` and direct lookups resolve file
   carriers;
3. the CLI: `codefly generate runnable-bindings` writes `v3`;
4. the caller: reads `v3` through core and refuses anything else;
5. each workspace regenerates its `runnable-bindings` group.

A caller built before step 4 reading a `v3` value refuses it as an unknown
schema, and one built after it refuses an earlier value the same way. Neither
calls an owner from the wrong bytes.

The emitters run inside the process that starts or renders the workload — a
service agent built with core — so a local run and a Kubernetes render deliver
by file once that service's agent is built with this core. Until then the agent
emits every value inline exactly as before. A prepared binding is far below the
threshold either way: file delivery is a general capability for large values,
and no longer something runnable bindings depend on.

## Not covered

- A secret delivered through a restricted render's secret reference is
  resolved by the cluster, so its size is unknown at render time; it is still
  delivered as `secretKeyRef` environment. A large secret there needs a
  secret-volume reference, which the restricted contract does not declare yet.
- A service that reads the process environment directly, rather than through a
  Codefly SDK, must read `CODEFLY__FILE__*` itself.
