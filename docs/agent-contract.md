# CLI-agent compatibility

`codefly.services.agent.v0.AgentInformation.contract` is the CLI-agent wire
contract. Its protocol generation is independent of Core's Go module version,
the agent release, and the `v0` protobuf package namespace. Implementations can
compile the schema under `proto/codefly/services/agent/v0` in any language;
sharing Core's Go implementation is not a compatibility requirement.

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

`agents.Serve` owns this implementation and stamps its protocol generation and
capability into successful discovery responses, preserving plugin-defined
extra capabilities. It advertises support even when this particular process
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
Keep the manifest, schema, implementation, and this document together when
changing the contract. CLI releases must additionally state their required
capabilities; a Core server advertisement cannot determine CLI run policy.

The first rollout requires agents to declare protocol 1. Previously published
agents without a declaration are rejected during discovery, including native
agents. They need a one-time adoption, not ongoing rebuilds for every Core
release. This PR does not republish the fleet or bump Core's release version.
