# Publishing companion images

The six companion images (`codefly`, `execution`, `go`, `node`, `proto`,
`python`) live at `ghcr.io/codefly-dev/<name>`. **This repository does not
build or publish them.** It owns the Dockerfile, the build context and the
version; the codefly CLI builds, tags and pushes — the same split agents
already have, where an agent repository checks in a Dockerfile and the CLI is
what turns it into a published image.

## Publishing

From a checkout with `core/` and `cli/` as siblings (the CLI cross-compiles the
linux binary the `codefly` and `execution` images bake):

```sh
codefly companion publish --all --platform linux/amd64,linux/arm64 \
  --force-docker --pull --core-dir ./core
codefly companion verify --core-dir ./core
```

`--force-docker` is not optional on a machine with nix installed: `proto` ships
a `flake.nix`, the publisher prefers the flake when nix is on `PATH`, and the
nix path cannot emit a multi-platform manifest — so the run aborts on `proto`
after the earlier companions have already been pushed. `--pull` re-resolves the
`codefly` base by tag, so a dependent cannot bake a cached older base instead of
the one the same run just pushed.

In CI this is **codefly-dev/cli → Actions → Companions → Run workflow**, whose
`core_ref` input selects the core commit to publish from. Publishing is an
explicit operator action, not a side effect of merging: bumping a companion
version and merging to `main` here publishes nothing until that workflow runs.
The same repository runs `codefly companion verify` daily and on every CLI
release, so a pinned tag that is missing from the registry surfaces within a
day.

## Where the build inputs come from

`companions.BuildSpecs()` is the single source of truth for what each companion
builds from: its version (from `companions/<name>/info.codefly.yaml`), the
repository-relative Dockerfile and build-context paths, the target platforms,
the companion image it builds on, and where it expects a cross-compiled linux
`codefly` binary staged. The specs name **no registry** — they carry `<name>`
and `<version>`, and the publisher qualifies them.

`.github/workflows/companions-build.yml` builds every image from these specs on
any change under `companions/`, without publishing, so a Dockerfile that no
longer builds fails on the pull request rather than when someone dispatches a
publish.

Every companion builds from the **repository root** for
`linux/amd64,linux/arm64`, so one publisher invocation is correct for the whole
set. Two properties keep that true, and both are test-enforced:

- A Dockerfile's `COPY` sources are written relative to the repository root
  (`companions/proto/facades/go`, not `facades/go`).
- A companion that bakes the CLI (`CLIBinary`) copies it through
  `${TARGETARCH}`, which BuildKit expands per target platform, so an arm64
  image can never carry the amd64 binary.

Build order matters: `go`, `node`, `proto` and `python` build on the `codefly`
image, so the publisher builds `codefly` first and the rest only after it
succeeds. Those four take their base as the `CODEFLY_BASE_IMAGE` build
argument; a test keeps the argument's default tracking
`companions/codefly/info.codefly.yaml`, so a dependent published without an
explicit override still resolves the base the same run just pushed.

## Publishing a change

1. Edit the companion (Dockerfile, entrypoint, pinned tool versions).
2. **Bump `companions/<name>/info.codefly.yaml`.** `publish` skips a companion
   whose tag is already in the registry and tells you to bump, so a change that
   keeps its version publishes nothing. That skip is what stops a rebuild from
   swapping the image under every consumer that already resolved the tag.
3. Merge to `main`, then run the CLI's Companions workflow against that commit.

To replace an image already published under its current tag — repairing a bad
push rather than shipping a change — pass `--force`. It overwrites the tag for
everyone who has not pulled it yet, so prefer a version bump wherever one works.

## proto: Docker in CI, Nix locally

`companions/proto` has two build definitions. The publisher builds the
**Dockerfile**: the Nix flake produces a `linux/*` OCI tarball that needs a
Linux builder and a `docker load` + retag round-trip, which buys nothing on a
Linux runner that already has BuildKit. The flake stays the reproducible local
path and targets the same `ghcr.io/codefly-dev/proto:<version>` tag, and
`companion_plugins_test.go` keeps the two definitions pinned to the same tool
versions.

## One-time package setup

A ghcr package is **private** by default on its first push, which breaks every
anonymous pull. After the first push of each package:

1. Open `https://github.com/orgs/codefly-dev/packages/container/<name>/settings`.
2. **Danger Zone → Change visibility → Public.**
3. **Manage Actions access → Add repository → `codefly-dev/cli`**, role
   `Write`, so later pushes from the publishing workflow keep the package.

Verify without credentials:

```sh
token=$(curl -s "https://ghcr.io/token?scope=repository:codefly-dev/proto:pull" | jq -r .token)
curl -sI -H "Authorization: Bearer $token" \
  -H "Accept: application/vnd.oci.image.index.v1+json" \
  "https://ghcr.io/v2/codefly-dev/proto/manifests/0.0.13"
```

Checklist — do this once per package: `codefly`, `execution`, `go`, `node`,
`proto`, `python`.
