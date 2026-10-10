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
a floating tag. An empty digest returns `ErrImageUnpublished`; malformed digests
and a different repository are separate errors. Services may embed the lock and
wrap failures with their own publication instructions.

## Image evidence

`sbom.ImageSubjects(ctx, PublishedImage{...}, local)` derives one subject per
published platform from the pinned reference. For a locally built image it
reads the daemon's immutable image ID and records it as the subject's expected
digest. Local images need Docker and a host `syft`; registry scans can use the
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
checksum. `ImageEvidenceDocument.SHA256` is the canonical inventory checksum,
not a checksum of the formatted CycloneDX file bytes.

The live regression uses locally built scratch images, the real Docker daemon,
and installed syft; it never pulls a base image:

```sh
go test -race -tags=image_evidence_required ./agents/services/sbom -timeout 3m
```

The tag requires those tools and fails when either is missing. The ordinary
suite exercises the file format, index, subject derivation, and input refusals.

## Publishing before the release

Call `.github/workflows/publish-service-image.yml` pinned to an immutable core
commit from a service's `workflow_dispatch`. Supply `image-name`, stable
`version` without `v`, and optionally `lock-file` and `platforms`; pass the
registry credential as `registry-token`. The workflow builds the caller's
Dockerfile for those platforms and pushes by digest. It returns `digest` and
uploads the lock as an artifact; the service commits that lock before tagging
its release. No tag or lock commit is created by this workflow.

The publish job refuses a triggering commit not reachable from the caller's
own default branch before registry login or build. The credential boundary and
repository dispatch restrictions are described in
[CI credentials](ci-credentials.md) and
[branch protection](runbooks/branch-protection.md).

`cmd/image-sbom` stays in the services: it chooses service images, platforms,
output paths, and CLI behavior using these APIs. Adoption follows the core
release by pinning it and removing service-local copies.
