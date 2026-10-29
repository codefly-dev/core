# Environment image bindings

An environment can select immutable images for roles declared by a service's
running builder agent. This is the image-selection portion of customer build
policy; it does not implement package-source restrictions, dependency adaptation,
network isolation or universal vulnerability clearance.

The optional `base-image-bindings/v1` operation uses the existing
`BuildCapabilities` and `Build` RPCs. `BuildCapabilities.image_inputs` declares
unique roles, default fully qualified sha256-pinned references and agent-owned
recipe variants for the loaded service. A role can name `scratch`. Declarations
must reflect the service's CGO, native library and runtime package requirements.
Generated bindings alone do not advertise implementation.

The CLI resolves environment defaults, service-type selections and explicit
service selections into `DockerBuildContext.image_bindings`. A replacement names
its role, exact reference and supported variant. The agent validates these with
`services.ResolveBuildImages` and renders from the returned values. Unknown or
duplicate roles, mutable references, variant mismatches and replacement between
scratch and a registry image refuse. No agent identity or release roster decides
compatibility. `BuilderAgent.Build` refuses unsupported bindings before dispatch.

Registry relocation preserves content digest and variant. A different base
requires an explicit digest and the agent's supported recipe variant. Naming a
variant asserts compatibility; it does not prove arbitrary image contents have
that ABI or tooling. Image execution and target-platform qualification remain
required before adoption. A Wolfi recipe requires an implementation in its agent;
changing an Alpine image name cannot provide it.

The CLI must verify the emitted recipe actually consumes the resolved references
before fetching/building, retain the effective bindings with build evidence, and
include them in cache/reuse decisions. Agent recipe files are never rewritten by
the CLI. Builds without a selected image policy keep the existing contract and
require no fleet update. This Core contract alone does not enable CLI or agent
support or update any deployed image.
