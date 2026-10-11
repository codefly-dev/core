# Shared service caller tokens and image evidence

Core exports the library operations shared by service gateways. Services keep
ownership of their commands, environment names, image build recipes, release
versions, and deployment templates.

## Caller tokens

`callertoken` is the gRPC-only implementation used by both `agents.Serve` and
the agent manager. Install `UnaryServerInterceptor(token)` and
`StreamServerInterceptor(token)` on a protected listener; clients use
`DialOption(token)` or `PerRPCCredentials(token)`. The metadata key is
`callertoken.MetadataKey` (`x-codefly-token`); `agents.AuthMetadataKey` aliases it.
The token authenticates possession of a shared secret; Work Context verification
and authorization are separate obligations.

Only the exact `/grpc.health.v1.Health/Check` and
`/grpc.health.v1.Health/Watch` methods are exempt. `Health/List`, unknown health
methods, and application methods require the token. Empty server tokens fail
closed. `Generate` returns 32 random bytes encoded as 64 hexadecimal characters.
The credentials permit plaintext transports for local agent sockets; a network
service must provide transport protection appropriate to its deployment.

Services resolve their own token and anonymous-listener setting, then call
`CheckListener(token, allowAnonymous, Listener{...})`. It refuses neither being
set and refuses both being set. An explicitly anonymous listener installs no
token interceptor. The library does not choose environment variable names.

## Published image locks

`resources.ParseImageLock(content, name)` reads the existing JSON lock format:
`{"name":"ghcr.io/codefly-dev/<name>","digest":"sha256:<64 hex characters>"}`.
This format is the service release lock, distinct from the protobuf image
subject/evidence contracts. `ImageLock` names that existing two-field document;
it adds no independent service version or tag.

The registry is `resources.ImageRegistry`, and the name must match the expected
service image exactly. The returned `DockerImage` uses the digest rather than
a floating tag. The digest requires 64 lowercase hex characters, and unknown JSON fields or a
second document are refused. An empty digest returns `ErrImageUnpublished`; malformed digests
and a different repository are separate errors. Services may embed the lock and
wrap failures with their own publication instructions.

## Image evidence

`sbom.ImageSubjects(ctx, PublishedImage{...}, local)` derives one subject per
published platform from the pinned reference. For a locally built image it
reads the daemon's immutable image ID and records it as the subject's expected
digest and platform. A local request must omit `Platforms`: the daemon holds
one platform, and a caller list is refused rather than silently dropped. Local images need Docker and a host `syft`; registry scans can use the
managed syft image. A requested registry platform needs Docker/buildx to resolve
the platform manifest.

`sbom.ImageForSubject` is the common scan and validation path used by the builder
and `CollectImageEvidence`. It requires a pin and refuses a result that differs
from an explicit expected digest. A reference may pin a multi-platform index;
its platform child is resolved from that index. The subject's `Digest` field,
when present, instead names the exact scanned manifest or local image ID.
Local scans use the resolved immutable ID, so moving a tag between inspection
and scanning cannot attach the old identity to new bytes.

`CollectImageEvidence(ctx, dir, subjects)` writes CycloneDX documents named by
repository, platform, and scanned digest. Any failed subject fails the call;
callers publish artifacts only after the complete call succeeds. The function
may have written earlier subjects before a later failure, so use a fresh output
directory per collection. `WriteImageEvidenceIndex` writes `index.txt` with the
platform, repository plus scanned digest, document filename, and inventory
checksum. An unstated platform occupies its column as `-`.
`ImageEvidenceDocument.SHA256` is the canonical inventory checksum,
not a checksum of the formatted CycloneDX file bytes.

The live regression uses locally built scratch images, the real Docker daemon,
and installed syft; it never pulls a base image:

```sh
go test -race -tags=image_evidence_required ./agents/services/sbom -timeout 3m
```

The tag requires those tools and fails when either is missing. The ordinary
suite exercises the file format, index, subject derivation, and input refusals.

## Publishing before the release

Call `.github/actions/publish-service-image` as a **step in the service's own
job**, pinned to an immutable core commit. The reusable publisher shipped in
v0.18.1 has been removed: GHCR checks the repository defining the job, so a
core-defined reusable job could not write a service's package. Do not grant
core write access to service packages. Keep their Actions access on the service
repository and replace its old publisher with this action.

```yaml
name: publish-image
on:
  workflow_dispatch:
    inputs:
      version:
        required: true
        type: string
permissions:
  contents: read
jobs:
  publish:
    if: github.event_name == 'workflow_dispatch'
    runs-on: ubuntu-latest
    timeout-minutes: 30
    permissions:
      contents: read
      packages: write
    outputs:
      digest: ${{ steps.publish.outputs.digest }}
      lock: ${{ steps.publish.outputs.lock }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: codefly-dev/core/.github/actions/publish-service-image@<immutable-core-commit-SHA>
        id: publish
        with:
          version: ${{ inputs.version }}
          registry-token: ${{ secrets.GITHUB_TOKEN }}
```

Use a dedicated job with no service execution before the action. Checkout must
select the triggering commit (omit `ref`) at the workspace root, never persist
credentials, and fetch full history. The caller owns the event gate, permissions
and 30-minute timeout; a composite action cannot declare job policy. The action
also refuses events other than `workflow_dispatch`. Its authenticated authority
step runs only `ls-remote --symref`, from a fresh empty temporary directory with
inherited Git configuration cleared, to resolve the caller repository's default
branch. A separate inspection step receives the resulting SHA and **no token**.
It checks ancestry and HEAD against the triggering commit before login or build.
If the remote branch advances beyond locally available history, publication
fails closed; rerun with a fresh full-history checkout.

The inspection refuses modified, staged, untracked or ignored files and tracked
paths marked `assume-unchanged` or `skip-worktree`. Commit generated inputs before
publishing. It rejects persisted credentials/indirection in effective local and
worktree config, including includes, and requires `origin` to match the GitHub
server/repository identity. System/global Git config and system attributes are
disabled. Filter drivers and `core.attributesFile` in any remaining config scope,
plus filter attributes in `.gitattributes`, Git info attributes
or default user attributes, are unsupported. Submodules are also unsupported:
`.gitmodules`, gitlinks or retained `.git/modules/` metadata cause refusal.

Fsmonitor configuration in the repository is refused. Fsmonitor and Git hooks
are disabled on every inspection call, including hooks in `.git/hooks/` or a
configured `core.hooksPath`. Config values are never logged. Do not inject tokens
through workflow/job/composite-call environment: repository inspection must
start without a credential, not merely unexport it from an already credentialed
process. The `registry-token` input goes only to `docker/login-action`, never a
shell or build argument. The built-in token retains the caller job's
`packages: write` grant; step isolation controls where its value is supplied.

Supply a stable `version` without `v`, and optionally `lock-file` and `platforms`
(defaults: `gateway-image.json` and `linux/amd64,linux/arm64`). The image name
defaults to the caller repository name in lowercase; `image-name` overrides it
within `resources.ImageRegistry`. These inputs are validated before login. `dockerfile` defaults to `Dockerfile`;
an override must name a tracked relative regular file inside the clean checkout
(no absolute paths, traversal, symlinks or untracked recipes).
The action builds the caller's selected Dockerfile with the repository root as context and pushes by digest, without
tags. It returns `digest` and the compact two-field JSON `lock`, writes
`release-evidence/<lock-file>`, and uploads that file as the artifact
`<lock-file>-<version>`. Commit the lock before tagging the service release.
The action neither commits the lock nor tags the image or repository.

### Exercising the publisher

[The manual test caller](../.github/workflows/test-publish-service-image.yml)
uses the committed `FROM scratch` fixture at
`.github/actions/publish-service-image/testdata/Dockerfile`, calls the action,
and compares its outputs with the artifact's source. It creates no build inputs
before calling the action. In core it loads the local action only
from `main`, so a dispatch cannot load an unmerged local action with a write
token. It publishes only to `core-publish-service-image-test` and requires the
workflow/action to have landed before dispatch on the current default-branch
commit (its checkout must match the triggering SHA).

For the cross-repository GHCR proof before release, put that caller in a test
repository's default branch, replace its local `uses` with the immutable core
commit under test, omit checkout's `ref: main` to select the triggering commit,
and change `image-name` to the designated scratch package. Commit the scratch
Dockerfile in that repository before dispatch, setting `dockerfile` to its
tracked path; do not generate or copy a recipe in a workflow step. Give only that test
repository Actions write access to the package. Dispatch on its default branch;
a successful build alone is insufficient: record the push run URL, returned
digest, and uploaded lock. Dispatch an unmerged branch as well and require it
to fail at the ancestry refusal before login. A local shell test or a push from
core itself does not prove cross-repository GHCR authorization.

The credential boundary and repository dispatch restrictions are described in
[CI credentials](ci-credentials.md) and
[branch protection](runbooks/branch-protection.md).

`cmd/image-sbom` stays in the services: it chooses service images, platforms,
output paths, and CLI behavior using these APIs. Adoption follows the core
release by pinning it and removing service-local copies.
