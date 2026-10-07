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
Missing destinations fail before template preparation. Go custom context roots
and workspace-root contexts are both rejected before preparation because the
single-image recipe cannot represent them — `Workspace` carries the same
requirement as `ContextRoot`, so rejecting only one would defer the failure to
an opaque `COPY` error inside the executor. Managed images and runtime-only
agents remain no-build.

`output_directory` is the service's committed `builder/` directory, so it holds
whatever the repository and the developer's OS put there. A plan from the shared
runners therefore inventories only the files the emitting build wrote — the set
`PrepareRecipeDestination` returns — and declares
`RECIPE_INVENTORY_SCOPE_EMITTED`. `VerifyDockerBuildPlan` re-hashes exactly those
paths: unrelated content cannot perturb the digest or fail the build, while drift
in a declared file (content, mode, replacement by a symlink, removal) is still
rejected.

An agent that assembles the whole destination itself — copying its build context
there and building `"."`, as the Go agent does — calls `BuildDockerBuildPlan` and
declares `RECIPE_INVENTORY_SCOPE_TREE` instead. There the inventory covers every
file, and verification re-walks the destination, because a file *added* after
emission is an injected build input that buildx would copy into the image. The
scope is part of the aggregate digest, so a plan cannot be rewritten to the
weaker check, and a plan declaring no scope is rejected rather than verified
under a default. Because that directory is committed, every emitted
path is unlinked before rendering: the template writer opens destinations with
`O_CREATE|O_TRUNC` and would otherwise write *through* a symlink to a file
outside the caller-owned directory.

Recipe path bases differ and the distinction is load-bearing: `dockerfile` and
`dockerignore` are relative to `output_directory`. By default `context` is
relative to the **service** directory, so existing v3 recipes keep their meaning.
An agent assembling rewritten inputs must set `context_root=OUTPUT`, making
`"."` select its emitted tree. Explicit roots use recipe contract v4, with the
root covered by the digest; older hosts reject v4 rather than silently build the
wrong sources. Unchanged/default-root plans retain their v3 contract and digest.
Inventory scope never selects a context root. Core checks paths lexically; the
executor resolves them and rejects symlink escapes from the selected root.

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

## Go runner binary reuse

The shared Go runner keeps no executable cache of its own. Every
`GoRunnerEnvironment.BuildBinary` runs `go build -o` on the one executable the
runner keeps per environment (`<cache>/{native,nix,container}/main`), in the
same native, Nix or companion environment and with the same race/debug/CGO/
workspace settings every time. The Go toolchain decides what that build redoes:
it identifies the complete action graph — every compiled input (sources,
embedded files, cgo and assembly inputs, transitive and locally replaced
packages) and the link action with its linker flags — reads the build ID the
existing executable carries, and leaves it in place when that graph still
produces it. Nothing changed means no compile and no link; a changed embedded
YAML recompiles its package and relinks; a changed `-ldflags` relinks.

A key of the runner's own cannot do this. The previous one — compiled package
build IDs from `go list -deps -export` plus a hash of `*.go` and module files —
missed a changed `-ldflags` under `-trimpath`: Go records no linker flags in
package metadata there ([go.dev/issue/52372](https://go.dev/issue/52372)), so
every package ID stayed the same while the link action changed, and the runner
returned the executable that still carried the old value. Enumerating more
flags into such a key is the weaker form; the toolchain's own check is the only
one that covers the whole graph, and it is the same check whether the build
runs natively, in Nix or in a companion.

`Runner` offers an executable only if the build that produced it completed.
`BuildBinary` forgets the previous executable before its first fallible step
and publishes the new one only once `go build` has exited 0. (It does not
check that the file exists afterwards: `go build -o` reuses the executable in
place, so after any earlier success it is always there, and such a check would
protect the first build only.) Three ways a build ends without completing are
each refused rather than served: module preparation failing (a malformed
`go.mod` fails `go mod download` before `go build` runs); `GOFLAGS=-n`,
inherited or injected, which would make `go build` a dry run that prints its
plan, exits 0 and writes nothing — the invocation pins `-n=false`, which Go's
own parser lets override `GOFLAGS`; and a signal terminating the build. That
last one is the process contract of `runners/base`: `Proc.Run` returns nil
only for an exit status of 0 or a termination the Proc's own `Stop` requested,
and a SIGTERM from anywhere else — a timeout, a parent dying, a deploy — is the
failure it is. It used to be read as success, for native and Nix processes
alike. The runner does not emulate Go's embed patterns, parse `go.mod` for
local replacements, discover container inputs with the host's toolchain, or
read `gomod.hash` as a source digest; that file tracks dependency-download
state only. `UsedCache` went with the cache it
reported on: an agent that logged it reports the build's elapsed time instead.
A binary built by an earlier Core keeps its hash name beside the new one until
the cache directory is removed. This applies to all agents using
`GoRunnerEnvironment.BuildBinary`, including Go specializations. Image builds
still use the CLI/BuildKit path described above.

`runners/golang/binary_reuse_test.go` reads the toolchain's decision from the
commands `go build -x` prints, never from a flag the runner sets.
`TestNativeBuildRelinksWhenOnlyLinkerFlagsChange` builds a real main under
`GOFLAGS="-trimpath -ldflags=-X=main.policy=first"`, changes only `first` to
`other`, and proves the executable is relinked with no package recompiled and
prints the new value; with nothing changed, neither a compile nor a link runs.
`TestNativeBuildRecompilesWhenOnlyEmbeddedYAMLChanges` does the same for a
dependency package's same-length embedded YAML and proves a missing embed fails
the build with nothing stale offered to run. The default Go runner suite covers
imported package and local replacement changes the same way.

`runners/golang/publish_test.go` covers the three incomplete builds on the
real toolchain: a malformed `go.mod` after a success leaves `Runner` refusing
and a repaired one building again; `GOFLAGS=-n` set through the inherited
environment and through the runner's own variables still produces an
executable that prints the new linker value; and a `-toolexec` wrapper that
sends SIGTERM to the `go` command driving it makes `BuildBinary` fail with
`signal: terminated`, `Runner` refuse, and the next build recover.
`runners/base/run_contract_test.go` pins the process contract itself: a
process that terminates its own group is a failure, a requested `Stop` is not,
and a cancelled context is reported as such; the Nix runner carries the same
contract.
