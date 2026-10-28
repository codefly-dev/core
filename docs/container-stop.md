# Explicit container stop with retained data

`runners/dockerrun` supplies a bounded operator-selected stop operation, separate
from workspace adoption, process-group recovery, ledger reconciliation and
container destruction. CLI owns target selection and operator authorization.
Core owns inspection, expectation comparison, delivery and observed disposition.
Scope/name labels are comparison evidence, not permission to infer a parent
service. A receipt grants no authority by itself.

## API and receipt

`PreviewContainerStop(ctx, fullID)` accepts only a complete lowercase 64-hex
Docker ID. It never resolves a name or a shortened ID. The inspection must name
that exact ID, include Created, Config, HostConfig and State, have
`codefly.owner=true`, and agree between the actual container name and
`codefly.name`. **AutoRemove is refused before stopping**: Docker could otherwise
delete the object on exit. Missing HostConfig also refuses.

The returned `ContainerStopExpectation` contains ID, Created, Name, every label,
and every field of Docker's `container.MountPoint` records. The producer sorts
mounts by their complete JSON representation and emits an explicit empty `[]`
for an unmounted container. These are custody facts, not a hash of stored data
or an invented ledger ownership claim. No environment variables or secret
configuration bodies are included. Existing labels and mount paths are included
verbatim, so receipts remain operator evidence and should be retained privately.

Persist the receipt with the default `json.Encoder` or `json.Marshal`, and decode
with `ParseContainerStopExpectation`. It refuses unknown fields, duplicate JSON
keys, nulls, trailing documents and missing identity/owner/name/mount evidence.
Receipt fields use their exact lowercase schema spelling; nested MountPoint
fields use Docker's exact spelling (for example `RW`). Case aliases refuse,
including when placed before or after a correctly spelled field. Arbitrary
label keys remain case-sensitive data. Legal JSON escapes and whitespace remain
accepted; byte-canonical JSON is not required.
Missing or null mounts are not an empty mount list. The owner constant
`MaxContainerStopExpectationBytes` bounds the whole receipt to 64 KiB, including
the default Encoder newline. Preview, parser and direct execution validate the
same canonical-output budget; oversized inspections refuse instead of emitting
unusable receipts. CLI uses that constant and parser, not its own schema.

## Conditional stop and disposition

`StopContainer(ctx, expected)` independently inspects the same full ID, refuses
AutoRemove or missing evidence, and compares every expected field before
calling the existing Docker `ContainerStop` on that ID. Mount order is not
significant; every mount field and label is. The stop has a three-second Docker
grace period and an overall fifteen-second context bound. It never resolves the
name again, claims a session ledger resource, changes labels, removes an object
or volume, starts a container, or falls back to another operation.

Readback must find the same ID and Created, unchanged labels/name/mounts, and a
non-running, non-restarting, non-paused state. `ContainerStopResult` reports
`Stopped`, `Retained` and `MountsUnchanged`; success requires a nil error and all
three true. A repeat for the same unchanged stopped object succeeds without
another stop. A missing object is an error, not proof it was retained. A
same-name replacement is never selected by the old ID.

Cancellation, timeout or a lost reply may follow a successful Docker effect.
Such errors do not claim that nothing happened. Readback failure returns an
error and only postconditions actually observed. Docker supplies no atomic
compare-and-stop transaction: the full immutable ID prevents a replacement from
being stopped, but an external actor can rename or restart the selected object
between observations. Changed custody in readback refuses success. This API
does not promise the object stays stopped after the observation.

The intended CLI surface is `recover container FULL_ID --preview` and
`recover container FULL_ID --expect FILE`. Execution checks that the positional
ID equals the receipt ID and delegates to Core. It reports the owner result and
requires all postconditions for exit zero. Ordinary `stop`/`clear` remain
unchanged. Operational use requires a reviewed binary and fresh exact receipt;
the API does not authorize a target through its name or scope.

## Qualification and limits

Maintained Docker tests use only their own created IDs. They prove refusal of
changed ID/creation/name/scope/namespace/mount evidence, AutoRemove refusal while
the container stays running, preserved writable-layer and named-volume content,
same stopped-generation retry, survival of an authenticated sibling and an
unattributed control, and refusal to reach a same-name replacement. Transport
tests cover explicit empty mounts, bounds including Encoder newline, malformed
receipts and unavailable inspection fields.

Stopping an old container does not add a parent-service label. The producer's
later exact-name reuse is a separate operation: its configuration fingerprint
may cause recreation, and identical reuse leaves legacy labels unchanged.
Qualify the corrected producer's retained-resource/data behavior independently
before claiming migration or workspace adoption. A matching mount receipt alone
does not prove application data contents or consistency.
