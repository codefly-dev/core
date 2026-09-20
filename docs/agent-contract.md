# CLI-agent compatibility

`codefly.services.agent.v0.AgentInformation.contract` is the CLI-agent wire
contract. Its protocol generation is independent of Core's Go module version,
the agent release, and the `v0` protobuf package namespace. Implementations can
compile the schema under `proto/codefly/services/agent/v0` in any language;
sharing Core's Go implementation is not a compatibility requirement.

## No static agent compatibility knowledge

Core and the CLI know their own supported protocols, not which named agent
releases are compatible. Compatibility is a runtime verdict on the actual
process's authenticated advertisement. There must be no agent-name exception,
minimum agent-release table, linked-Core comparison, or compiled fleet roster
in that decision. A binary built independently, in another language, or against
an older Core is accepted when it implements the required protocol. A newer
binary with an unsupported or missing declaration is rejected before work.

Artifact selection and compatibility are different. A user may select an
immutable artifact for reproducibility; that selection is never proof of
compatibility and is never rewritten merely because the host upgraded. Do not
replace a rejected selection with another release behind the user's back.
Automatic selection must use agent-owned declarations, not a host-maintained
list of named agents and approved versions. Ambiguity requires an explicit
selection, not a guessed default.

Changing Core or the CLI without changing the wire contract must not require
rebuilding, republishing, or repinning agents. Do not bump the protocol just
because a library release was cut. Real incompatible protocol changes require
explicit adoption, checked at runtime; optional capabilities are required only
by operations that use them. Generic runtime checks remain mandatory even for
artifacts that passed release qualification.

Review every compatibility change with an unknown agent identity, independently
versioned builds, missing and unsupported protocol declarations, absent required
capabilities, and unknown extra capabilities. Tests must show that identity and
release metadata cannot change the verdict. Framework build paths, native
product cleanup and source-to-agent mappings belong to their owning plugins,
not to compatibility enforcement in Core or the CLI.

`startup_protocol_version` names the stdout handshake used to discover the gRPC
endpoint. It must match the host before lifecycle compatibility can be checked.
The manifest records both versions; the startup parser and contract checker
share one startup-version constant, tested against the release manifest.

Protocol **1** uses the existing authenticated agent discovery and
Builder/Runtime lifecycle RPCs. The host calls `GetAgentInformation` before
dispatching lifecycle operations. It accepts only its supported protocol
generation, then checks that the agent advertises every capability required by
the selected operation. Extra capabilities are ignored. A missing declaration
or version zero is undeclared, never evidence inferred from a Core version or
an acknowledgement header. Existing operation-specific advertisements such as
validation and build-cache support remain authoritative for those operations.

Core's `services.Load` rejects undeclared or incompatible protocol versions
before returning an instance. Callers use `Instance.RequireAgentCapabilities`
before dispatching operations that require optional features. Core does not
choose a CLI run's requirements. The CLI adoption, including replacing its
existing recovery check and publishing CLI compatibility notes, is tracked in
[codefly-dev/cli#747](https://github.com/codefly-dev/cli/issues/747). Until that
lands, this is a Core contract implementation, not a completed CLI rollout.

## Container recovery

`container-recovery-scope/v1` promises validation of the inherited process
marker, discovery's `codefly-container-recovery-scope` response header, and
ownership labels on containers created through Core's Docker runner. See
`runners/recoveryscope` and `runners/dockerrun` for the marker and label formats.
An implementation must provide all three behaviors before advertising it.

`agents.Serve` supplies its implementation's contract only when a handler
leaves the contract absent. An explicit declaration is authoritative, including
its protocol version and omitted capabilities. Plugins extending the shared
contract can start from `contract.Current()` and add their own capabilities.
The shared implementation advertises support even when this particular process
received no valid scope. Handwritten servers must declare their own contract
and implement the behavior; importing the generated types alone promises
nothing.

Before an operation that creates recoverable containers, the host calls
`Instance.RequireContainerRecoveryScope(expected)`, where `expected` is the
identity captured by that flow when it projected the scope. This first checks
capability support, then requires an exact acknowledgement of this run's
identity. Missing capability and wrong/missing acknowledgement are distinct
errors. Native operations that do not need recovery should not require this
capability. Advertising support never bypasses the ownership check.

## Evolution and releases

- Incompatible lifecycle semantics increment `protocol_version`. Both peers
  must explicitly adopt that generation; there is no implicit downgrade.
- Additive optional behavior gets a stable, versioned capability identifier.
  Changing its meaning requires a new identifier. Require it only for the
  operation that needs it.
- Core changes outside this contract leave its generation and capabilities
  unchanged. An unchanged contract requires no agent rebuild solely because
  Core changed.

`agents/contract/contract.json` is the protobuf-JSON release manifest for
`AgentContract` and the embedded source of the shared server's advertisement.
The version-tag workflow attaches it to the GitHub release and compares it
with the latest reachable stable release tag. Release notes explicitly report
introduction, unchanged compatibility, protocol changes, or capability changes.
Startup changes also require compatible agents and are reported separately.
Publication resumes an existing draft after interruption and verifies the
uploaded manifest before publishing. Retrying a published release verifies its
manifest without replacing it; a missing or different artifact is an error.
Keep the manifest, schema, implementation, and this document together when
changing the contract. CLI releases must additionally state their required
capabilities; a Core server advertisement cannot determine CLI run policy.

The first rollout requires agents to declare protocol 1. Previously published
agents without a declaration are rejected during discovery, including native
agents. They need a one-time adoption, not ongoing rebuilds for every Core
release. Source adoption and official artifact publication are separate from
qualification of the published agent in a consuming workflow.

## Artifact identities and installation

Publisher, name and version are individual path components. Each starts with an
ASCII letter or digit and contains only letters, digits, `.`, `_`, `+` or `-`.
This is a syntax constraint, not an identity or compatibility roster. Explicit
release labels and semantic versions with prerelease/build metadata are valid;
empty versions and path/URL delimiters are rejected. Parsing, protobuf conversion,
cache path construction and GitHub release lookup enforce the same rule,
including for directly constructed resource values.
Versions cannot contain `__`: its last occurrence separates name from version
in the cache filename. Names may contain it, and single underscores remain valid
in release labels. This keeps accepted identities distinct without changing
existing cache paths or silently interpreting an ambiguous selection.

Agent downloads finish extraction before publishing a binary. Publication copies
into a temporary file beside the destination, sets permissions, flushes and closes
the file, then renames it atomically. Concurrent readers retain the old complete
binary or open the new complete binary. An interrupted transfer or failed staging
copy does not truncate an active installation. Installation does not establish
protocol compatibility: authenticated live admission remains required before
lifecycle operations.
