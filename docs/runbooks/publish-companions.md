# Publishing companion images

The six companion images (`codefly`, `execution`, `go`, `node`, `proto`,
`python`) are **built and published by the codefly CLI**, not by this
repository. Core owns the Dockerfile, the build context and the version; the
CLI owns registry resolution, tagging and push — the same split agents have,
where an agent repository checks in a Dockerfile and `codefly` turns it into a
published image.

## The handoff

`companions.BuildSpecs()` is what core hands the builder. Each spec carries the
companion's name, its version (from `companions/<name>/info.codefly.yaml`), the
repository-relative Dockerfile and build-context paths, the target platforms,
the companion image it builds on, and where it expects a cross-compiled linux
`codefly` binary staged. It names **no registry**: the builder resolves
`<name>:<version>` into a published reference.

Two consequences worth knowing:

- A companion whose Dockerfile builds on the `codefly` base takes that base as
  the `CODEFLY_BASE_IMAGE` build argument (`companions.BaseImageArg`). Its
  in-Dockerfile default is a pin the builder overrides; a test keeps that
  default tracking `companions/codefly/info.codefly.yaml`, so the pin cannot
  drift to a tag that was never published.
- `codefly` and `execution` bake the CLI, so their build context is the
  repository root and the builder stages the binary before the build. The
  `execution` spec targets `linux/amd64` only: its `COPY bin/linux/codefly`
  does not consult `TARGETARCH`, so a multi-platform build would put the amd64
  binary in the arm64 image.

## Publishing a change

1. Edit the companion (Dockerfile, entrypoint, pinned tool versions).
2. **Bump `companions/<name>/info.codefly.yaml`.** Tags are immutable, so a
   change without a version bump publishes nothing an agent will pull.
3. Merge to `main`, then publish from the CLI: **codefly-dev/cli → Actions →
   Companions → Run workflow**, with `core_ref` set to the core ref you want
   published (`main`, or a tag). That job checks out core, builds every
   companion in spec order, pushes, and then verifies each tag is present.

Locally, from a core checkout with a sibling `cli/`:

```sh
codefly companion publish --all       # build + push the whole set
codefly companion build proto         # build one image, no push
codefly companion verify --all        # assert the pinned tags exist
```

Build order matters and the specs encode it: `go`, `node`, `proto` and
`python` build on the `codefly` image, so it is published first and the rest
only after it succeeds.

## proto: Docker in CI, Nix locally

`companions/proto` has two build definitions. The published image is the
**Dockerfile** build. The Nix flake stays the reproducible local path, and
`companion_plugins_test.go` keeps the two definitions pinned to the same tool
versions. The flake names its own output tag, so it does not go through the
CLI's registry resolution — pass `--force-docker` when what you want is the
image the registry will actually serve.

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
