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

Shared language/framework tooling can live in Core when consumers explicitly
invoke it. That does not authorize host-side agent selection, compatibility
rules, framework-specific runtime paths or product-specific recovery policy.

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

Core's `services.LoadAgent` rejects undeclared or incompatible protocol versions
before caching a connection, including for callers that bypass `services.Load`.
Connection and instance caches retain the admitted selection. A request for a
different agent or version under an occupied service key fails without stopping
the active process; its owner must call `services.ClearAgent` before replacing
it. Mutating the caller's resource does not relabel an already-running agent.
The direct SDK launcher performs the same live protocol check and requires its
Builder and Runtime capabilities before creating a service. Agent updates inspect
the candidate before changing a saved selection; a rejected candidate or failed
configuration write leaves the previous in-memory selection intact.
Callers use `Instance.RequireAgentCapabilities`
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

### Local runtime image handoff

`InitRequest.runtime_image` (field 10) carries one container image reference,
supplied by the CLI after it builds the agent's recipe for a local run needing
a container runtime. The reference is a name with an explicit non-`latest` tag
or a `sha256` digest. It is invocation data, not image bytes, a recipe, registry
credentials or a deployed artifact selection. A scalar string is sufficient;
omitted and empty both mean no supplied image. The agent emits the recipe and
the CLI executes buildx; Init does not acquire a build responsibility.

An adopting agent may use the supplied image when the local manifest names
none. A nonempty manifest image retains precedence and must validate; a valid
request must not hide an invalid manifest. Image validation remains the agent's
responsibility: refuse malformed references, implicit `latest` (a name with
neither tag nor digest), explicit `latest` even beside a digest, and any
non-`sha256` digest. If neither source supplies an image, the existing local
missing-image refusal remains. Core transports the value without parsing or selecting
images. Every deployed path leaves this field empty and retains the agent's
existing image resolution; absence is not permission to guess or build an image.

`runtime-init-image/v1` names this optional behavior. The CLI requires it on
the live `AgentContract` through the existing capability check when relying on
the handoff. An agent advertises it only after implementing selection and
refusals; linking generated bindings or Core's shared server does not advertise
adoption. The release manifest records the operation contract while wire
protocol 1, startup protocol 2 and shared server capabilities remain unchanged.

The proposal was opened first in
[handbook#260](https://github.com/obin-ai/handbook/pull/260).
This is the Core transport for
[core#748](https://github.com/codefly-dev/core/issues/748), not a completed local
run: the build-to-Init producer is
[cli#932](https://github.com/codefly-dev/cli/issues/932), and consumer selection
and refusal tests belong to
[service-libreoffice#63](https://github.com/obin-ai/service-libreoffice/issues/63).
Those implementations and a combined boot must qualify the downstream rollout.

### Deployment composition provenance

`DeploymentRequest.composition_provenance` carries every member of the CLI's
one loaded, resolved composition, including transitive imports under their
**composed names**. Each member carries its declared role (`module` or
`solution`) and the name of its owning workspace, preserved through imports.
The CLI fills this from the same composition its own dependency judge uses.

The shared builder converts supplied membership to `resources.Provenance` and
passes it to `ResolveDependencyNetworkMappings`, which already takes that
interface. It uses the existing edge and endpoint verdicts. Request provenance
wins whenever present: the agent neither loads nor merges a repository's dev
workspace into it. An empty supplied composition still has presence; missing
edge members, duplicate names, empty names or owners, and unspecified or unknown
roles refuse as `ErrUnjudgedProvenance`, never trigger another source.

Only an absent field falls back to `FindWorkspaceUpFrom` at the service's
physical location. That older-CLI behavior is correct **only for a checkout
inside the rendering composition**. It does not fix pinned module caches,
composed renames or imports for an older CLI. No discovered workspace still
means unjudged dependency addresses.

This is stricter as well as correct: a solution's direct route to a module
endpoint returns `ErrSolutionReachesThroughHost`, even when its repo's dev
workspace treats it as a module and would admit the edge. The request preserves
the role needed for that refusal and the owning workspaces in its diagnostic.

`deployment-composition-provenance/v1` names this optional operation contract.
The CLI requires the live `AgentContract` capability when relying on the
handoff. Agents advertise it only when their deployment path implements these
semantics; the shared server does not automatically advertise adoption.
Wire protocol 1 and startup protocol 2 are unchanged. No version roster or
fleet repin substitutes for live adoption.

[Handbook proposal #266](https://github.com/obin-ai/handbook/pull/266) preceded
the proto edit for [core#751](https://github.com/codefly-dev/core/issues/751).
The CLI producer is sequenced after Core in
[cli#937](https://github.com/codefly-dev/cli/issues/937). Core tests do
not qualify that handoff, downstream agent adoption, or a staging render.

### Compatibility reporting

- Incompatible lifecycle semantics increment `protocol_version`. Both peers
  must explicitly adopt that generation; there is no implicit downgrade.
- Additive optional behavior gets a stable, versioned capability identifier.
  Changing its meaning requires a new identifier. Require it only for the
  operation that needs it.
- Core changes outside this contract leave its generation and capabilities
  unchanged. An unchanged contract requires no agent rebuild solely because
  Core changed.

`agents/contract/contract.json` is the JSON release manifest. Its
`protocolVersion`, `startupProtocolVersion` and `capabilities` fields supply
the shared server's `AgentContract` advertisement. `operationContracts` records
host-supported optional operation contracts, not capabilities automatically
advertised by executors. `artifact-execution/v1` must be truthfully advertised
on the live Builder `BuildCapabilities` or Solution `GetSolutionInformation`
probe before selection-bound operations. A successful response must acknowledge
the exact request identity and every named output digest and relative file path.
Embedding a newer Core server does not implement or advertise this behavior.
`codefly.dev/docker-build-recipe/v4` records support for explicit recipe context
roots. Only plans selecting an explicit root use v4; default service-root plans
retain v3 and its digest. The plan version, not the agent identity or Core pin,
prevents an older host from silently building the wrong tree. See
[build recipe context selection](build-cache.md#cli-executor-selection).
`runtime-init-dependency-mappings/v1` is explicitly advertised by runtimes that
consume `InitRequest.dependencies_network_mappings` before executing a test
whose target is never started. These are the dependencies' accepted addresses,
not a fresh deterministic allocation. Hosts send them after dependency Init;
runtimes must reject a dependency-backed test with missing addresses rather than
reconstructing another invocation's endpoints. Ordinary Start still carries
dependency mappings. Merely linking this schema does not advertise adoption.
See [composition selections](composition-selections.md#artifact-execution).
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

For release qualification, use `codefly agent install` to populate an isolated
`CODEFLY_HOME` with the official selections in the rollout inventory, then run:

```sh
GOWORK=off CODEFLY_HOME=/path/to/qualification-cache go test ./services \
  -tags=published_agents_required -run TestPublishedAgentsDeclareRuntimeContract -count=1 -v
```

This checks every selected executable in that cache through authenticated live
admission. Record download failures separately: an absent artifact is not a
passing qualification. The inventory is release evidence, never a runtime roster.

## Artifact identities and installation

Publisher, name and version are individual path components. Each starts with an
ASCII letter or digit and contains only letters, digits, `.`, `_`, `+` or `-`.
This is a syntax constraint, not an identity or compatibility roster. Explicit
release labels and semantic versions with prerelease/build metadata are valid;
empty versions and path/URL delimiters are rejected. Parsing, protobuf conversion,
cache path construction and GitHub release lookup enforce the same rule,
including for directly constructed resource values.
Names and versions cannot contain `__`, the cache filename separator. This
rejects both interpretations of previously colliding cache entries rather than
trusting their contents. Single underscores remain valid in names and release
labels. Existing unambiguous cache paths are unchanged.

GitHub agent downloads use `GITHUB_TOKEN`, then `GH_TOKEN`, when supplied.
Authenticated acquisition resolves the exact published tag and asset through
the GitHub API, then streams the asset through its authenticated API endpoint.
The bounded download client follows asset redirects without API credentials.
Draft releases, duplicate assets and an asset URL that disagrees with the loader
selection are refused. Without a token, the public download path is unchanged.
`manager.OpenReleaseAsset` lets a publisher verify the same selected asset with
its existing authenticated client; a private release is not a public URL probe.

Agent downloads finish extraction before publishing a binary. Publication copies
into a temporary file beside the destination, sets permissions, flushes and closes
the file, then renames it atomically. Concurrent readers retain the old complete
binary or open the new complete binary. An interrupted transfer or failed staging
copy does not truncate an active installation. Installation does not establish
protocol compatibility: authenticated live admission remains required before
lifecycle operations.

Running service connections bind both the explicit selection and the SHA-256
digest of the executable admitted at startup. Reusing Agent, Builder, Runtime,
Code, or Instance clients rechecks current executable content, including local
symlink targets. Installing different bytes at the same version returns an
error without killing the running agent. The flow owner must explicitly clear
that service before admitting the replacement. Identical bytes remain reusable
after atomic reinstallation. Startup also rejects a concurrently replaced
executable before publishing the connection. These checks do not establish
publisher authority or qualify a rebuilt runtime output.
