# Registry build layer cache

Core supports the `registry-v1` cache contract through
`DockerBuildContext.cache`. This is optional layer reuse: every build still
executes BuildKit in the CLI and produces its own image. Cache references never substitute for artifact
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

`mode` defaults to `max` (including intermediate dependency stages). Explicit
`min` exports only final-image layers and generally cannot retain Go/Next.js
dependency-install work across source edits. Only `registry` is supported. Unknown backends/modes fail before
building. Empty exports means read-only; absent cache options retain existing
behavior. A missing registry cache is a BuildKit cache miss. BuildKit validates
content-addressed blobs; import failures can produce a warning or fail the
build depending on the registry/BuildKit error. There is no blind retry that
conceals corruption or publication failure. A caller can explicitly remove
cache options and rebuild cold after an import error. Export errors fail the
build instead of claiming publication succeeded.

## Integration through Codefly

The Go and Rust shared runners return only recipes through
`SingleImageBuildResponse`. The CLI validates their plan and applies its own
cache policy when executing Buildx.

CLI recipe executors should verify the recipe tree as usual, then append
`docker.CacheArguments(callerCache, recipe.Platforms)` to their existing Buildx
invocation, preserving platform, target, build arguments and digest/provenance
collection. Cache policy stays with the caller and is not part of agent-returned recipes;
recipe verification therefore cannot be mistaken for cache publication authority. No provider workflow should replace Codefly builds with
service-specific Docker commands.

CLI integration is implemented in codefly-dev/cli#622. `build service`,
`build module`, `ci build`, and the build phase of `ci run` accept cache-from,
cache-to, cache-scope, cache-mode and cache-backend options. The CLI owns policy,
adds service/recipe identity and preserves the executed platform and image-digest
metadata. Its dependency pin includes this Core contract.

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

Run the registry and exported-input conformance tests with:

```sh
CODEFLY_TEST_REGISTRY_CACHE=1 go test ./agents/helpers/docker -run 'TestLanguageRegistryCacheAcrossCleanBuilders|TestMaxCacheDoesNotExportHiddenOrUnusedContext' -v -timeout=45m
```

Go and Next.js multistage fixtures perform real module/npm installation and
compilation. Each build uses a fresh BuildKit instance. The tests check dependency
install hits after source edits, invalidation after lockfile/base-image changes,
retained eligible dependency work after application build-argument changes, and
produced binary/page contents. The export inspection test opens actual cache
blobs and rejects hidden or unused input markers, including a Dockerfile located
in an ignored directory.

The `Registry build cache conformance` workflow runs cold and warm phases on
separate clean hosted runners. A one-day artifact transfers only the disposable
test registry's data between jobs; the builders import/export through registry
transport. No protected publication credentials are used. This test storage
handoff is not a production workflow requirement.

`BuilderOutput.Duration` measures context preparation, Buildx and architecture
inspection. The output writer receives BuildKit cache import/export durations,
hits and transferred bytes when available. The CLI also logs image-build wall
time; CI reporting records operation wall time. Unavailable metrics are not zero.

Local clean-builder measurements (seconds, including builder startup):

| Fixture | Cold | Source edit | Lockfile edit | Base change | Build-argument change |
| --- | ---: | ---: | ---: | ---: | ---: |
| Go | 37.15 | 89.00 | 26.64 | 23.57 | 19.93 |
| Next.js | 48.76 | 30.02 | 53.64 | 47.36 | 27.43 |

Both fixtures hit the dependency-install cache on source and application-argument
edits, rebuilt it for lockfile/base changes, and produced the expected changed
outputs. These are conformance measurements, not a speedup claim; startup and
host load affect wall time.

### CLI executor selection

The CLI alone builds and publishes application images. Shared Go and Rust
runners require an absolute `output_directory` and return a DockerBuildPlan.
Missing destinations fail before template preparation. Go custom/workspace-root
contexts are rejected before preparation because the single-image recipe cannot
represent them. Managed images and runtime-only agents remain no-build.

`DockerBuildContext.buildx_builder` identifies the builder the CLI selected,
including selection for registry cache exports. The agent does not invoke it.
Before dispatching an explicit selection, `BuilderAgent.Build` requires the
read-only `BuildCapabilities` RPC to report `buildx_selection=true`. Recipe-only
agents should implement this capability after verifying their Build path:

```go
func (*Builder) BuildCapabilities(context.Context, *builderv0.BuildCapabilitiesRequest) (*builderv0.BuildCapabilitiesResponse, error) {
    return &builderv0.BuildCapabilitiesResponse{BuildxSelection: true}, nil
}
```

Recipe responses need no executor acknowledgement because the CLI executes the
plan itself. The Docker execution and registry-cache helpers remain available
for the CLI executor; service agents must not call them to build images.
