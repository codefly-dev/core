# Registry build layer cache

Core supports the `registry-v1` cache contract through
`DockerBuildContext.cache`. This is optional layer reuse: every build still
executes BuildKit, verifies the requested architecture on the in-agent path,
and produces its own image. Cache references never substitute for artifact
manifest digests, recipe verification, tests, or release authorization.

A caller can supply this protobuf JSON fragment in its Docker build context:

```json
{
  "cache": {
    "backend": "registry",
    "imports": ["ghcr.io/example/build-cache"],
    "exports": ["ghcr.io/example/build-cache"],
    "scope": "workspace/service/app/protected",
    "mode": "max"
  }
}
```

Repositories must be fully qualified and omit tags and digests. Core constructs
`codefly-<sha256>` tags from contract version, scope, and target platform. Use a
stable workspace/service/recipe identity and trust domain in scope; do not add
a commit SHA or aggregate source digest. The platform is part of the transport
identity, while BuildKit keys layers by instruction, resolved base image,
platform, build arguments and files consumed by each instruction. Cache-enabled
builds use `--pull` to resolve mutable base tags again.

Agents retain ownership of Dockerfiles and declared inputs. For useful reuse,
Go recipes must copy go.mod/go.sum and download modules before copying source;
Next.js recipes must copy package manifests/lockfiles and install dependencies
before copying source. Source changes then invalidate application layers while
lockfile, toolchain/base-image and relevant build-argument changes invalidate
consuming dependency layers. Cache mounts are not exported by registry layer
caching: required dependency outputs must be in ordinary filesystem layers.

`mode` is `min` (default, final-image layers) or `max` (including intermediate
stages). Only `registry` is supported. Unknown backends/modes fail before
building. Empty exports means read-only; absent cache options retain existing
behavior. A missing registry cache is a BuildKit cache miss. BuildKit validates
content-addressed blobs; import failures can produce a warning or fail the
build depending on the registry/BuildKit error. There is no blind retry that
conceals corruption or publication failure. A caller can explicitly remove
cache options and rebuild cold after an import error. Export errors fail the
build instead of claiming publication succeeded.

## Integration through Codefly

The Go and Rust shared in-agent builders forward cache policy to Buildx.
`SingleImageBuildResponse` carries it into each conventional recipe and
acknowledges `registry-v1` in `BuildResponse.cache_contract_version`.
`BuilderAgent.Build` rejects successful responses lacking that acknowledgement
when cache was requested, including responses from older agents. Callers using
the generated gRPC client directly must perform the same check.

CLI recipe executors should verify the recipe tree as usual, then append
`docker.CacheArguments(callerCache, recipe.Platforms)` to their existing Buildx
invocation, preserving platform, target, build arguments and digest/provenance
collection. Use the caller's policy as authority: an agent-returned recipe must
not broaden import/export permissions. Cache transport is intentionally outside
the recipe file digest. No provider workflow should replace Codefly builds with
service-specific Docker commands.

The CLI flag/CI plumbing is tracked in codefly-dev/cli#611 and is not implemented
in this Core repository. Next.js agent recipe ordering must be verified in its
own repository. Consequently this change alone does not enable an end-to-end
cached CLI workflow on hosted runners.

## Permissions and build inputs

Authenticate Docker outside the build request using registry credentials scoped
to the cache repository. Do not place credentials in refs, scopes, build args,
Dockerfiles or copied files. Registry authentication uses Docker's existing
credential configuration, not serialized protocol fields.

Only trusted builds may write a cache consumed by protected builds. Enforce this
with registry ACLs and CI credential issuance, not a scope naming convention:
an attacker with repository write access can overwrite any tag in that repo.
Untrusted PRs should have read-only access to an appropriate trusted cache or
use a separate repository that protected builds never import. Do not provide
protected write credentials to untrusted code, even with exports omitted.

`max` can publish source and intermediate artifacts. Cache repositories need
appropriate read restrictions. Use Docker secret mounts for secrets, keep
secret-dependent outputs out of exported layers, and declare all ordinary build
inputs. Builds stage a local context filtered by root, Dockerfile-specific and custom
ignore policies. Negations apply within each policy; a custom policy cannot
re-include root-excluded files. The Dockerfile is provided separately, so ignoring
its directory does not remove the build definition. Local BuildKit transfer
avoids exporting the intermediate full-context snapshot created by stdin tar
builds; unused files are not cache layers.
This transport introduces no additional context inputs. Secret or network state
not represented in declared inputs cannot be made reproducible by caching.

## Validation and measurements

Run the isolated registry boundary test with:

```sh
CODEFLY_TEST_REGISTRY_CACHE=1 go test ./agents/helpers/docker -run TestRegistryCacheAcrossCleanBuilders -v -timeout=900s
```

It creates a private local test registry and a fresh BuildKit container per
build, verifies imported hits after a source edit, invalidation after a dependency
input edit, and reads the produced image's source file. It removes its own
containers, builders and images afterward. This is a small layer fixture, not
a representative Go/Next.js hosted-runner benchmark.

`BuilderOutput.Duration` measures in-agent wall time including context creation,
Buildx and architecture inspection. The configured output writer receives
BuildKit's plain progress, including cache import/export vertex durations,
`CACHED` hits and transfer sizes when BuildKit emits them. Unavailable values
must remain unavailable, not be reported as zero. CLI total operation wall time
and structured telemetry aggregation remain the CLI executor's responsibility.
Separate clean hosted-runner Go/Next.js measurements, base/build-argument
invalidation and corrupt-registry tests remain outstanding integration evidence.

In a local BuildKit 0.32.2 run, the cold build took 40.62 s and exported its
cache in 2.7 s. A fresh builder after a source edit imported the manifest in
0.2 s, reported the dependency `COPY` as `CACHED`, transferred its 119-byte layer,
and exported cache in 3.8 s. That build took 117.08 s overall, including 86.5 s
bootstrapping the isolated builder. These small-fixture measurements demonstrate
remote reuse, not a speedup or hosted-runner performance claim. A third fresh
builder after a dependency edit took 38.08 s and reported no cached layers.
