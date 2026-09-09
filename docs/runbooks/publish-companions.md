# Publishing companion images

The six companion images (`codefly`, `execution`, `go`, `node`, `proto`,
`python`) live at `ghcr.io/codefly-dev/<name>` — the canonical registry for
everything codefly publishes, exposed in code as `resources.ImageRegistry`.
They are pushed by `.github/workflows/companions-publish.yml` on every push to
`main` that touches `companions/**`, and on demand via **Actions →
companions-publish → Run workflow** (the `companion` input publishes a single
image; an unknown name fails the run).

## Where the build inputs come from

`companions.BuildSpecs()` is the single source of truth for what each companion
builds from: its version (from `companions/<name>/info.codefly.yaml`), the
repository-relative Dockerfile and build-context paths, the target platforms,
the companion image it builds on, and where it expects a cross-compiled linux
`codefly` binary staged. The workflow reads it through
`internal/buildspecs`, so no per-companion knowledge lives in shell and the
workflow cannot drift from the specs.

The specs name **no registry**: they carry `<name>` and `<version>`, and the
publisher qualifies them. That is the seam the CLI takes over when it owns
building and publishing (#408) — at which point the two workflows here are
deleted and the same specs drive `codefly companion publish`.

## Publishing a change

1. Edit the companion (Dockerfile, entrypoint, pinned tool versions).
2. **Bump `companions/<name>/info.codefly.yaml`.** Tags are immutable: the
   workflow skips a companion whose ghcr tag already exists, so a change
   without a version bump publishes nothing.
3. Merge to `main`. The workflow builds the spec's platforms, pushes
   `ghcr.io/codefly-dev/<name>:<version>`, attaches a signed build-provenance
   attestation, and then verifies the tag is pullable with no credentials.

Build order matters: `go`, `node`, `proto` and `python` build on the `codefly`
image, so the workflow publishes `codefly` first and the rest only after it
succeeds. Those four take their base as the `CODEFLY_BASE_IMAGE` build
argument, and the workflow passes **the digest it just published** — a
dependent never builds against a mutable tag. When `codefly` is not part of the
run, the digest of its pinned version is resolved from the registry instead;
the tag pinned as the argument's default in the Dockerfile is the last resort,
and a test keeps it tracking `companions/codefly/info.codefly.yaml`.

Two companions are special, and both facts are declared in the spec rather than
special-cased in the workflow:

- `codefly` and `execution` bake the codefly CLI (`CLIBinary`), so their build
  context is the repository root. CI downloads the latest released
  `codefly-dev/cli` linux binaries rather than cross-building the
  private-to-this-repo toolchain, so the CLI baked into an image is whatever
  was released when that companion version was published.
- `execution` builds `linux/amd64` only. Its `COPY bin/linux/codefly` does not
  consult `TARGETARCH`, so a multi-platform build would put the amd64 binary
  in the arm64 image.

## proto: Docker in CI, Nix locally

`companions/proto` has two build definitions. CI publishes the **Dockerfile**
build: the Nix flake produces a `linux/*` OCI tarball that needs a Linux
builder and a `docker load` + retag round-trip, which buys nothing on a Linux
runner that already has BuildKit. The flake stays the reproducible local path
and targets the same `ghcr.io/codefly-dev/proto:<version>` tag, and
`companion_plugins_test.go` keeps the two definitions pinned to the same tool
versions.

## One-time package setup

A ghcr package is **private** by default on its first push, which breaks every
anonymous pull. The workflow's last step catches this and fails with the fix
printed, but the setup itself is manual. After the first push of each package:

1. Open `https://github.com/orgs/codefly-dev/packages/container/<name>/settings`.
2. **Danger Zone → Change visibility → Public.**
3. **Manage Actions access → Add repository → `codefly-dev/core`**, role
   `Write`, so later pushes from this repo keep the package.
4. Re-run the failed job; the anonymous-pull check should now pass.

Verify without credentials:

```sh
token=$(curl -s "https://ghcr.io/token?scope=repository:codefly-dev/proto:pull" | jq -r .token)
curl -sI -H "Authorization: Bearer $token" \
  -H "Accept: application/vnd.oci.image.index.v1+json" \
  "https://ghcr.io/v2/codefly-dev/proto/manifests/0.0.13"
```

Checklist — do this once per package: `codefly`, `execution`, `go`, `node`,
`proto`, `python`.
