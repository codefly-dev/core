# Session ledger — resource ownership, retained state and crash recovery

`core/sessionledger` records what one Codefly invocation owns, so a later
invocation can clean up exactly what a crashed one created — and nothing else.

## The problem it solves

Codefly already had two ownership mechanisms, and neither answers the question
cleanup actually asks.

- **Process groups** (`runners/base`) persist an authenticated record per spawned
  group under `~/.codefly/runs/authenticated-v1/`. It is PID-reuse safe and
  proves which groups a dead CLI left behind — but it knows nothing about
  containers, volumes, data, or whether a resource was ours to begin with.
- **Docker containers** carried the spawning CLI's PID in a
  `codefly.session` label, and the startup sweep removed containers whose owner
  PID was no longer alive. A bare PID is not an identity: a recycled PID makes a
  dead owner look alive, and a name collision makes someone else's container
  look like ours. Whether a container was reaped or preserved was then decided by
  a heuristic (`codefly.ephemeral`), not by a record of who created it.

Neither records **ownership** (did this invocation create it, or borrow it),
**disposition** (is it running, stopped, deliberately retained, or gone), or a
**receipt** of what cleanup actually did.

## The record

One JSON file per invocation, under:

```
~/.codefly/runs/session-ledger/v1/
  sessions/<invocation-id>.json   the records
  locks/<sha256(key)>.lock        per-key advisory lock
```

The schema version is in the directory name *and* in every record's `schema`
field, fixed to `codefly.session-ledger/v1`. A release that speaks another
contract writes to a different directory and never parses these files — the same
namespacing rule the process-group registry uses. The directory sits under a
subdirectory of `~/.codefly/runs`, which the process-group reaper's legacy scan
skips, so the two mechanisms cannot see each other's records.

A record carries:

| Field | Meaning |
|---|---|
| `invocation_id` | 128 bits of randomness. Backends bind resources to it, so ownership is provable from the backend's own state. |
| `key` | The reuse scope. Sessions sharing a key compete for the same warm state. |
| `mode` | `disposable` or `reusable`. |
| `fingerprint` | The semantic plan digest warm reuse must match. Excludes invocation identity by construction, so two runs of the same plan can reattach to each other's state. |
| `owner` / `lease.holder` | Full process identity — PID **plus** boot id, start id and executable — of the creating and currently attached process. |
| `resources[]` | Every backend object the session touched. |

Each resource records `kind` (namespaced, e.g. `docker.container`), `backend`
(which adapter speaks for it), `id` (the backend's own identifier),
`ownership`, `disposition`, `data`, `vanishes_on_stop`, a `witness`, and the
`outcome` of the last cleanup attempt.

`vanishes_on_stop` is set by the adapter that creates the resource and says
whether stopping it destroys it. A process group is gone once stopped; a
container still exists and still holds its writable layer. That is a fact about
the backend, not about the disposition, so it is recorded rather than inferred —
without it a stopped container's record would be dropped while the container was
still on disk.

## Lifecycle policy

| Lifecycle | Owned execution resource | Owned data | Borrowed anything |
|---|---|---|---|
| `stop` | stopped | **retained** | untouched |
| `keep-running` | untouched | untouched | untouched |
| `reset` | deleted | deleted | **refused before anything runs** |

Three rules make this a contract rather than a set of defaults:

1. **Stop retains data.** Ending execution never removes state. This is the
   end-of-run default and the only lifecycle crash recovery applies on its own,
   because a dead invocation's intent for its data is unknowable.
2. **Borrowed resources are never stopped and never deleted**, under any
   lifecycle. A nested SDK session that borrows its parent's database cannot take
   that database down when it ends.
3. **Reset requires owned disposable state.** A reset asked of a session holding
   borrowed data fails whole, before a single backend is called, so nothing is
   partially applied.

### Relationship to the existing `Destroy` RPC

`RuntimeService.Destroy` is unchanged by this package, and its behavior today is
**not** "delete the data": agents stop their process or container and leave
persistent state in place (this is F10, `service-postgres#79`). The SDK's own
`Dependencies.Destroy` doc comment describes it as removing state; that comment
describes an intent, not what the agent paths do. `LifecycleReset` is the new,
explicit disposal authorization — deliberately a separate operation, so no
existing `Destroy` caller is silently reinterpreted as permission to drop a
developer's database.

## Ownership and disposition

```
Declare ──► declared ──Commit──► running ──┬── stop  ──► stopped   (no data)
   │                                       ├── stop  ──► retained (data)
   │           Adopt ────────────►         └── reset ──► deleted
   └── (crash here: the record already names what may exist)
```

`declared` is written **before** the resource is created and is durable when
`Declare` returns — file synced, directory synced. A crash between declare and
create still leaves the identifier the resource would carry, which is all
recovery needs to ask the backend whether it exists.

That only works for a backend whose resource can be named ahead of creation and
whose ownership can be proven independently of the ledger — a container name
reserved up front and a `codefly.invocation` label to prove it by. A process
group has neither: its pgid does not exist until the leader runs, and a pgid
cannot be labelled. Use `Adopt` for those: it records the resource and its
witness in one durable write, so there is never a moment where the ledger names
a process group it cannot prove is ours. Nothing is lost, because
`runners/base` has already persisted its own authenticated registration before
the pgid is knowable.

## Crash recovery

`Recover` reconciles every session that still holds a lease whose holder is no
longer running. A released session — warm state kept on purpose, or a run that
finished — already had its lifecycle applied by whoever released it and is
never touched.

Liveness is decided by re-authenticating the full recorded identity, not by
signalling the PID: a PID whose boot id or start id differs is a different
process, so a recycled PID reads as dead and its current occupant is never
mistaken for ours.

For each resource, recovery asks the backend to **Claim** it. The backend — not
the ledger — decides ownership, because only it can read its own state:

- `ErrNotFound` → nothing is there. Recorded `deleted`, nothing done.
- `ErrNotOwned` → something is there but cannot be proven this invocation's.
  **Preserved**, disposition unchanged, reported.
- no adapter registered for the backend → **refused**, preserved, reported. The
  record survives so a later run with that adapter can finish the job.
- claimed, but the recorded witness does not match what the backend observes →
  preserved as not-owned.
- a `process.group` record with no witness at all → preserved as not-owned. The
  ledger and the process-group registry are garbage-collected independently, so
  a ledger entry can outlive the registration that authenticates it; once that
  registration is reaped the pgid is free to be handed to an unrelated run, and
  claiming on the number alone would terminate it.

Nothing is ever found by name sweep, and nothing is ever killed by PID alone.
Each backend call is bounded by `ReconcileOptions.PerResourceTimeout` (30s by
default), so one unresponsive engine cannot stall recovery of everything else,
and every outcome is written durably before the next resource is touched — an
interrupted recovery never repeats work it already finished.

## Warm reuse

A `reusable` session outlives its process. `Acquire` reattaches to it only when
the caller presents the same fingerprint:

- same fingerprint, holder released or dead → reattached; `Handle.Reattached()`
  is true and the caller reuses the recorded resources instead of creating new
  ones.
- different fingerprint → `IncompatibleReuseError` naming both digests. The warm
  state is left exactly as it was. A changed fixture, artifact or backend is a
  reason to explain the mismatch, never a licence to delete a database.
- holder still live → `ErrSessionBusy`.
- lease still held by an invocation that died → `ErrRecoveryRequired`. Nobody
  applied a lifecycle to those resources, so adopting them would hand the caller
  a container that may have died with its invocation. Run `Recover`, then
  acquire again.

`Store.Reset(ctx, key, backends, options)` is the way out of an
`IncompatibleReuseError`: it applies `LifecycleReset` to the warm state and
forgets it, so the key is usable again. Without it the instruction in that error
would be unfollowable — the only handle to a session is `Acquire`, and `Acquire`
is what refused. Reset obeys the same rules as any other reset: refused while a
live invocation holds the session, and refused whole when the session holds
borrowed data.

Acquire, release and recovery all take a per-key advisory file lock (plus an
in-process mutex, so goroutines of one process are serialized deterministically
rather than racing their retry timers), which is what makes concurrent attach and
reset safe.

## Retention and expiry

`Expire` removes a record only when the session is released, its last update is
older than the retention window (`DefaultRetention`, 7 days), and every resource
it names is **forgettable**: deleted, borrowed, or stopped with no data.

A record still naming retained data is kept however old it is. That data is
deliberately still on the machine, and forgetting the record is exactly how it
becomes unattributable garbage that no later run is entitled to remove. Those
records are what an inspection command shows a developer asking what is still on
their disk, and an explicit `reset` is what clears them.

An unreadable record is reported once and quarantined — renamed beside itself
with a `.invalid` suffix. It is never deleted: it may still name live resources
and nothing here can read them, so the bytes are kept for a human. Quarantining
is what stops it being re-reported on every invocation forever, which would
leave `Recover` permanently returning an error no caller can clear. This mirrors
how `runners/base` handles an unreadable process-group record.

## Secrets

The record has no free-form text field. Every persisted string is validated
against a bounded pattern — lowercase slugs for keys, namespaced kinds, sha256
hex for fingerprints, `[A-Za-z0-9][A-Za-z0-9._-]*` for backend identifiers,
bare binary names for executables, lowercase hex for adapter digests. A
connection string does not match any of them, so a caller cannot write one into
the ledger even by trying. Witnesses hold machine identity (pid, boot id, start
id, creation instant) and nothing else; argv and environment are never recorded.

## Backend adapters

An adapter implements `sessionledger.Backend`: `Claim`, `Stop`, `Delete`.
Two ship with core:

- **`sessionledger.NativeBackend`** (`native` / `process.group`) resolves a
  recorded pgid through the authenticated process-group registry rather than
  signalling the number directly. The registry record carries the leader's boot
  and start identity plus a per-spawn authentication secret, so a group whose
  pgid was recycled fails the claim instead of being killed. `Stop` is SIGTERM,
  grace, SIGKILL, delivered only to authenticated members.
- **`dockerrun.SessionBackend`** (`docker` / `docker.container`, `docker.volume`)
  claims a resource only when its `codefly.invocation` label matches the recorded
  invocation and its creation instant matches the recorded witness. Bind it at
  creation time with `DockerEnvironment.WithInvocation(id)`. `Stop` stops the
  container and keeps everything it holds; `Delete` removes the container (with
  its anonymous volumes) or the named volume. Named volumes are separate ledger
  resources and are only removed when the ledger recorded them as owned.

The `codefly.session` owner-PID sweep (`ReapStaleContainers`) skips any
container carrying `codefly.invocation`. It has to: its "owner dead + stopped →
reap" rule is exactly inverted for a ledgered container, because stopping a data
container is how the ledger *retains* it. A ledgered container is disposed of by
recovery, which can read the ownership record; the sweep cannot.

Registering no adapter for a backend is safe: its resources are recorded as
`refused` and preserved. That is why a caller for whom Docker is optional should
skip registering the adapter when the engine is unreachable rather than register
a failing one.

## Using it

```go
store, _ := sessionledger.Open("")             // ~/.codefly/runs
owner, _ := sessionledger.ProcessWitness(os.Getpid())

handle, err := store.Acquire(ctx, sessionledger.AcquireRequest{
    Key:         "myworkspace.mysvc",
    Mode:        sessionledger.ModeReusable,
    Fingerprint: planFingerprint,               // sha256 of the semantic plan
    Owner:       owner,
}, nil)

if !handle.Reattached() {
    resource := dockerrun.ContainerResource("mysvc-db", sessionledger.Created, true)
    ref, _ := handle.Declare(resource)          // durable before anything exists
    env.WithInvocation(handle.InvocationID())
    _ = env.Init(ctx)
    witness, _ := docker.ContainerWitness(ctx, "mysvc-db")
    _ = handle.Commit(ref, witness)

    // A process group has no name to reserve: record it and its witness at once.
    group, _ := base.StartTrackedProcessGroup(cmd)
    _, _ = handle.Adopt(sessionledger.NativeProcessGroup(group))
}

report, err := handle.Release(ctx, sessionledger.LifecycleStop,
    sessionledger.Backends{
        sessionledger.BackendNative: sessionledger.NativeBackend{},
        dockerrun.BackendDocker:     docker,
    }, sessionledger.ReconcileOptions{})
```

At startup, before taking a session:

```go
recovery, err := sessionledger.Recover(ctx, store, backends, sessionledger.ReconcileOptions{})
```

`sessionledger.Explain(session)` renders a session's receipt for display.

## State format migration

There is one schema, `v1`, and no migration to perform yet. A future contract
adds a sibling directory (`session-ledger/v2/`) and its own record type; `v1`
records are neither parsed nor quarantined by it, and the `v1` reader ignores
`v2`. A release that must reconcile both registers both readers. Records are
decoded with unknown fields rejected, so an additive field is a schema bump, not
a silent forward-compatibility trick.

## Startup container recovery scope

The legacy PID-based startup sweep requires a `codefly.recovery-scope`
label before checking owner liveness. The label hashes canonical Codefly home,
workspace path and resolved naming scope. This exact-scope sweep cannot claim
a different home, workspace or scope, even when its creator PID is gone. Containers without
this label, or with missing/malformed owner PIDs, require explicit owner recovery;
startup does not infer ownership from container names.

Disposable SDK invocations additionally delegate orphan cleanup through a durable
`codefly.recovery-group` label. Its identity includes the same canonical home and
workspace and the caller's naming scope, excluding only the current SDK session's
verified invocation suffix. This delegation is issued only in ephemeral mode with
matching session metadata. A later disposable invocation in that group can remove
dead-owner ephemeral siblings; live owners, stateful siblings and ledgered
containers remain protected. Ordinary naming scopes never gain this delegation.

The CLI sets the scope after resolving its run environment and before spawning
agents. `SetContainerRecoveryScope` projects a PID-bound process marker to direct
children; Docker environments built against this Core version emit the label.
Children retain their launch-time parent identity, so reparenting after a CLI
crash does not discard recovery or ephemeral labels. A nonempty invalid marker
refuses container creation instead of silently creating an unowned container.
Older agents do not emit it and their containers remain outside startup cleanup.
No automatic relabeling or migration of retained containers occurs.

Name-based lookup requires the same exact recovery scope before adoption,
replacement or shutdown. A scoped caller cannot claim an unscoped container, and
an unscoped caller cannot claim a scoped one. A mismatch is an error requiring
explicit owner recovery, even when runtime configurations match. Each Docker
environment retains its scope from first use so another flow changing the process
marker cannot redirect its cleanup. Recovery groups authorize orphan sweeping
only; they never authorize adopting another invocation's container.

The agent's `GetAgentInformation` response includes the
`codefly-container-recovery-scope` gRPC header with the validated inherited scope.
The CLI must require this acknowledgement before Docker provisioning. The
startup parent identity is retained after reparenting so a CLI crash cannot make
an already-spawned agent create unlabeled containers.

Containers also carry `codefly.recovery-namespace`, a hash of the canonical
home and workspace without the invocation's naming scope. This durable identity
lets `ReapDisposableContainers` recover explicitly ephemeral containers after
an SDK/test invocation dies, even when the next invocation has a fresh scope.
Unlike a recovery group, it needs no session metadata and survives a successor
that picked an entirely unrelated naming scope. It checks the original agent PID
and preserves live owners, stateful containers, and session-ledger containers.
A different home/workspace or a container without both ownership labels is never
eligible. No on-disk registry or inference from names is needed; failed removals
leave the labels available for retry.

Within the same scope, live owners and running stateful containers are retained.
Stopped containers and running ephemeral containers with dead owners can be
removed, without requesting volume removal. Containers carrying an invocation
label always remain owned by session-ledger recovery, including when stopped.
The scope is an isolation boundary between cooperating local runs, not protection
against a user with direct Docker-daemon access.
