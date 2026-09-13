# SBOM contract

`Builder.SBOM` returns two kinds of evidence, and they are not interchangeable.
A **source** inventory describes what the service's lockfiles resolve to. An
**image** inventory describes what is actually inside a shipped image: its OS
packages as well as its installed application dependencies. A source SBOM says
nothing about the base image a service runs on, so it never counts as image
coverage.

`SBOMRequest.scope` selects which one. An unset scope means source, so callers
written before image scope existed keep working unchanged.

## Image subjects

An image-scope request carries `subjects`: the images the caller requires
evidence for. Each `ImageSubject` names a `reference`, an immutable `digest`, a
`platform` in `os/arch` form, the `role` the image plays in the service
(`runtime`, `init`, `migration`, `sidecar` — matching the build recipe name),
and the `service` it belongs to.

Every shipped platform of a multi-architecture image is its own subject.
Evidence for `linux/amd64` does not cover `linux/arm64`.

Empty `subjects` asks the agent to enumerate its own images.

## Responses

`ImageSBOM` is one inventory bound to the `digest` and `platform` that were
actually scanned. Its `subjects` list every service-owned image that one scan
satisfies, so a digest shared by several services is scanned and reported once
without losing the association to any of them.

There are four honest outcomes, and picking the wrong one is the failure this
contract exists to prevent:

| Outcome | Meaning |
| --- | --- |
| `COMPLETE` with `images` | Real coverage, each entry bound to a digest. |
| `COMPLETE` with `no_image_reason` and no images | The service legitimately ships no image: a passive toolbox (`NO_IMAGE`) or an externally managed runtime (`EXTERNALLY_MANAGED`). |
| `UNSUPPORTED` | This agent has no implementation. Never use it to mean "no image". |
| `ERROR` | A scan failed, an image was missing, or a digest did not match. |

A complete image-scope response with no inventories and no reason is invalid.
That is the shape a fabricated "everything is covered" answer takes.

Agents whose images are built by the caller (they emit a `DockerBuildPlan` and
never run buildx) cannot enumerate digests. Asked with empty subjects they
return `ERROR` with `FAILURE_CODE_PRECONDITION_FAILED` through
`BuilderWrapper.SBOMImageSubjectsRequired` — the service does ship an image, so
neither `UNSUPPORTED` nor a no-image reason would be true. Given explicit
subjects they serve evidence like any other agent.

## Scanning

`sbom.Image` inventories one image and binds the result to the digest it
scanned:

```go
result, err := sbom.Image(ctx, sbom.ImageRequest{
    Reference: "ghcr.io/codefly-dev/app@sha256:...",
    Platform:  "linux/amd64",
    Source:    sbom.SourceRegistry,
})
```

A tag is resolved to a digest and the scan re-pins to that digest, so evidence
cannot drift between resolution and inventory. Passing a manifest-list
reference with a platform resolves the child manifest and binds the evidence to
the child's digest.

A multi-platform image with no requested platform is an error rather than an
arbitrary default pick. Buildx attestation manifests declare `unknown/unknown`
and are not scan subjects, so a single-platform buildx image still resolves
without an explicit platform.

`SourceDockerDaemon` scans an image held by the local Docker daemon. It is the
supported path for an image a build produced with `--load` and never pushed,
whose only immutable identity is its local image ID. It requires an installed
`syft`: the managed containerized scanner runs without the Docker socket by
design and cannot reach the daemon.

`sbom.Container` predates this contract and is unchanged. It scans a registry
image without resolving or binding a digest, so it does not satisfy image
coverage on its own.

## Coverage conformance

Coverage is derived from what the service itself declares, never from a list of
known services that would drift as the fleet changes:

```go
expected := sbom.ExpectedFromBuildPlan(service, plan)
err := sbom.ValidateCoverage(expected, resp)
```

`ExpectedFromBuildPlan` turns each recipe into one subject per shipped platform;
`ExpectedFromBuildResult` does the same for an agent-owned build.
`ValidateCoverage` is the single check every agent is measured against. It
rejects a source inventory, a non-complete response, evidence that is not bound
to a `sha256` digest, an empty inventory, a digest that differs from the
deployed one, an omitted platform, and a no-image claim that contradicts the
declared build.
