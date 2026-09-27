# Runnable binding delivery

How a prepared Runnable binding reaches the process that installs it, and how
any large workspace configuration value reaches a process at all. Two parts,
one design: a prepared binding stops carrying what it can share, and a
configuration value that is too large for a process environment is delivered
as a file whose path the environment carries.

Status: design of record for codefly-dev/core#670. `docs/runnable.md` owns the
package and binding contracts themselves; this page owns their delivery.

## What a binding is

A `RunnableBinding` (`docs/runnable.md`, "Immutable installation facts") is the
immutable installation of one verified `RunnablePackage` on one execution
facility. For an operation derived from an owner's service method, `codefly
generate runnable-bindings` *prepares* one per operation for one environment:
the package, the binding targeting the owner endpoint at the address that
environment resolves, the execution policy and authority the method declared,
and — for a gRPC owner — the descriptors a generic caller resolves the method
in. The prepared value is written to the `runnable-bindings` workspace
configuration group, keyed `<MODULE>__<OPERATION>`, and a worker that declares
that group as a workspace configuration dependency receives it like any other
group. No module names another.

The prepared value is a `runnable.Prepared` document, schema
`codefly.runnable-prepared/v2`:

| Field | What it is |
| --- | --- |
| `package` | the canonical `RunnablePackage` (`runnable.CanonicalJSON`) |
| `binding` | the canonical `RunnableBinding`, prepared and verified by core |
| `operation` | the policy and authority the method declared; owned by the writer and its reader, opaque to core |
| `descriptor_set` | a **reference** to the owner endpoint's descriptor set: its `digest`, and the `contract` digest it was derived from |

## What is shared by digest

The operations of one owner endpoint are resolved in the same descriptors. In
the embedded form (`v1`, no `schema` field) every prepared value carried its own
copy of the method's descriptor closure: roughly 100 KB decoded, 138 KB as the
base64 the value holds, per operation — five operations of one endpoint were
689 KB of the same bytes five times over.

In `v2` a prepared value references its owner endpoint's descriptor set by
digest instead:

- **The set.** `runnable.LeanDescriptorSet` takes the endpoint's published
  contract — the `contract.binpb` the API contract catalog records for that
  endpoint (`codefly generate contracts`) — and returns it with every file's
  `SourceCodeInfo` removed, marshalled deterministically. Comments and source
  locations are no part of resolving a method, and they are most of a
  descriptor's size. The whole endpoint set is kept rather than a per-method
  closure: it is a superset of every closure on that endpoint, so one copy
  serves every operation on it.
- **The digest.** `runnable.DescriptorSetDigest` is `sha256:<hex>` of those
  bytes. The reference also records the catalog's own digest of the source
  `contract.binpb` as `contract`, so a reviewer can tie the delivered set to
  the published contract without it being delivered twice.
- **The delivery.** The set is written once per distinct digest into the same
  `runnable-bindings` group, under `runnable.DescriptorSetKey(digest)`
  (`DESCRIPTOR_SET__<HEX>`), as standard base64. N operations on one endpoint
  are N small values plus one set.

## What the worker verifies

`runnable.DecodePrepared` reads a value strictly: unknown fields are refused,
the schema must be `v2`, and the reference's digest must be well formed.
`runnable.ResolveDescriptorSet(reference, lookup)` derives the key from the
digest, reads the value through the caller's lookup, decodes it, and **refuses
it unless the sha256 of the decoded bytes equals the referenced digest**. Only
then is it parsed as a `FileDescriptorSet`. A worker never resolves a method in
bytes whose identity it did not check; a mismatch — a stale group, a value
from another generation, a truncated file — fails the installation closed and
names the key, never the content.

The package and binding keep their existing verification (`VerifyBinding`
against the package); the descriptor digest is not part of either digest, so
sharing the set changed no package or binding identity.

## How a value is delivered: environment or file

A workspace configuration value has always reached a process as an environment
variable, set at process start: `CODEFLY__WORKSPACE_CONFIGURATION__<GROUP>__<KEY>`
(and the secret, service and document namespaces beside it). Two platform
limits make that the wrong carrier for a large value:

- Linux refuses `execve` when one `KEY=VALUE` string is longer than
  `MAX_ARG_STRLEN`, 128 KiB (`E2BIG`). One owner's descriptor set alone is
  above that.
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
  whole: every prepared value and the descriptor sets they reference, and only
  those. A set no value references is not written. `--check` compares the whole
  file, so a stale set is drift.
- **Migration from the embedded form.** There is no transition window. A value
  with no `schema`, or with an inline `descriptors` field, is refused by
  `DecodePrepared` with `runnable.ErrEmbeddedDescriptors`, whose message says to
  run `codefly generate runnable-bindings`. Accepting both would keep the
  oversized form alive in every workspace that never regenerates, which is the
  failure this change exists to remove, and a worker refusing at installation
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
- **Install time.** A descriptor set whose digest does not match, a missing set,
  and the embedded form are refused before any operation is installed.

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
- **The whole endpoint set, not a per-method closure.** A closure per method
  is smaller for one operation and larger for every endpoint with two or more,
  and it gives each method its own digest, which defeats sharing.
- **No dual reader.** A worker that also read the embedded form would keep it
  alive in every workspace that never regenerates (see *Migration*).
- **Core owns the reader's rules.** `DecodePrepared`,
  `ResolveDescriptorSet` and `ResolveFileCarriers` are the rules; a worker
  and an SDK call them rather than restating them, so they cannot drift.

## Rollout order

Every part fails closed against every other, so the order is one of
usefulness, never of safety:

1. core (this document, the types, the carriers and their emitters);
2. `sdk-go`: `LoadEnvironmentVariables` and direct lookups resolve file
   carriers;
3. the CLI: `codefly generate runnable-bindings` writes `v2` and the shared
   sets;
4. the worker: reads `v2` through core and refuses the embedded form;
5. each workspace regenerates its `runnable-bindings` group.

A worker built before step 4 that reads a `v2` value finds no inline
descriptors and refuses the binding; a worker built after it refuses an
embedded value with `ErrEmbeddedDescriptors`. Neither installs from the wrong
bytes.

The emitters run inside the process that starts or renders the workload — a
service agent built with core — so a local run and a Kubernetes render deliver
by file once that service's agent is built with this core. Until then the
agent emits every value inline exactly as before; the shared descriptor sets
still shrink the group, but a set above 128 KiB is still an inline string.

## Not covered

- A secret delivered through a restricted render's secret reference is
  resolved by the cluster, so its size is unknown at render time; it is still
  delivered as `secretKeyRef` environment. A large secret there needs a
  secret-volume reference, which the restricted contract does not declare yet.
- A service that reads the process environment directly, rather than through a
  Codefly SDK, must read `CODEFLY__FILE__*` itself.
