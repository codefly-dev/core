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
the `service` it belongs to, and the `source` the image has to be reached
through.

Every shipped platform of a multi-architecture image is its own subject.
Evidence for `linux/amd64` does not cover `linux/arm64`.

A subject must be pinned to a `sha256` digest, either in its `digest` field or
in its reference. A subject naming only a tag asks for a scan of whatever the
registry serves at that moment, so its evidence says nothing about the image
that was built — a tag left behind by an earlier push scans clean. Both the
shared agent implementation and `ValidateCoverage` refuse an unpinned subject.

Which of the two carries the pin is not a style choice. A **pushed** image is
pinned in its *reference*, so the scan resolves out of the image the build
produced and the evidence digest is derived from that identity rather than
compared against it — resolving a pushed image yields the digest of one
platform's child manifest, which is never the index digest the caller holds, so
comparing them would reject honest evidence. An image that was only **loaded
into the daemon** has no registry manifest to reference, so it keeps the tag the
daemon knows and pins its `digest` field to the local image ID, which is the
identity such a scan binds to. For a local subject that pin is required, not
a convention: the daemon resolves whatever reference it is handed to its own
image ID, so a local subject pinned only in its reference leaves no field
holding the identity that was asked for — the scan succeeds and binds
evidence to the ID the daemon returned. `RequirePinned` refuses one, which
rejects it both in the shared agent implementation and in `ValidateCoverage`.

`source` says which of those two cases applies, and nothing else can: a local
image ID and a registry manifest digest are both `sha256:<hex>`, and an image
loaded with `--load` commonly carries exactly the tag a pushed one would.
Probing the daemon first is not a substitute — a stale local image answering to
the same tag would bind evidence to an image nothing deployed, which is the
failure the digest pin exists to prevent. Only the party that produced the image
knows, so that party states it: `ExpectedFromBuildPlan` stamps each subject with
what the build resolved for that recipe.

It is per subject, not per request. A service that pushes its runtime image and
only loads its migration image owes evidence for both, and one request carries
both. `BuilderWrapper.SBOMImages(ctx, subjects)` reads each subject's own
selector; an agent no longer picks one source on every caller's behalf.

`IMAGE_SOURCE_KIND_UNSPECIFIED` means registry, so subjects written before the
selector existed resolve the way they always did.

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
| `COMPLETE` with `no_image_reason` and no images | The service legitimately ships no image: a passive toolbox (`NO_IMAGE`) or a runtime an external provider owns (`EXTERNALLY_MANAGED`, which must name that runtime in its status message). |
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

## Stock images

A service that deploys a vendor image it did not build still ships that image.
No recipe describes such an image, so a caller that does not know its
reference asks with an empty expectation — which is not evidence that the
service ships nothing. A stock-image agent knows its image reference before any
build, so it enumerates that image and returns real evidence for it. Evidence
carried against an empty expectation is validated like any other and counts as
coverage on its own, provided each inventory names a subject belonging to the
service being validated. Nothing derived anchors that evidence, so the service
identity is passed to `ValidateCoverage`, and an inventory naming only some
other service is refused rather than counted.

`EXTERNALLY_MANAGED` is not the escape hatch for that case. It means an external
provider owns the runtime the service points at — a hosted database, a SaaS
endpoint — so there is no image this service selects. A vendor image the service
pins and deploys is still its own shipped image. A response claiming
`EXTERNALLY_MANAGED` must name the external runtime in its status message, and
both `BuilderWrapper.SBOMNoImage` and `ValidateCoverage` refuse one that does
not.

A hybrid service — a vendor image plus its own init or migration recipe — owes
evidence for both. The recipe-derived subjects cover only the images it builds,
so the stock image has to be enumerated alongside them, or named with
`ExpectedFromImageReference` and appended to them.

Pin the reference, not the subject's `digest` field. `sbom.Image` resolves a
pinned `repo@sha256:...` reference for the requested platform and binds evidence
to the child manifest it selects, so identity follows from the pin itself. A
vendor image's per-platform child digests — which a stock-image agent usually
cannot know — never have to be enumerated, and an index digest copied into
`digest` would match none of the evidence it asks for. Set `digest` only when it
names the exact manifest a scan binds to: a true single manifest, or the local
image ID of an image that was built and never pushed.

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

`ImageRequest.Source` is the scanner's own argument, one image at a time; it is
not a second way to choose a source for a request. An agent serving image scope
takes it from the subject through `sbom.SourceOf`, which is what
`BuilderWrapper.SBOMImages` does.

A tag is resolved to a digest and the scan re-pins to that digest, so evidence
cannot drift between resolution and inventory. Passing a manifest-list
reference with a platform resolves the child manifest and binds the evidence to
the child's digest.

A multi-platform image with no requested platform is an error rather than an
arbitrary default pick. Buildx attestation manifests declare `unknown/unknown`
and are not scan subjects, so a single-platform buildx image still resolves
without an explicit platform; an index that carries nothing but attestations is
rejected rather than scanned as an index.

A manifest descriptor for a single image carries no platform field, so when one
is requested and the registry does not state it the platform is confirmed
against the image config. It is never taken from the request itself: evidence
must not assert a platform nothing verified.

Resolving a tag, or selecting one platform's child manifest, queries the
registry through `docker`. A reference that is already digest-pinned and asks
for no particular platform needs no resolution and is scanned with `syft`
alone, so a host without `docker` still works for fully pinned references;
anything else fails with an explicit, actionable error.

`SourceDockerDaemon` scans an image held by the local Docker daemon. It is the
supported path for an image a build produced with `--load` and never pushed,
whose only immutable identity is its local image ID. It requires an installed
`syft`: the managed containerized scanner runs without the Docker socket by
design and cannot reach the daemon. `sbom.SourceOf` maps a subject's selector
to it.

`sbom.Container` predates this contract and is unchanged. It scans a registry
image without resolving or binding a digest, so it does not satisfy image
coverage on its own.

## Coverage conformance

Coverage is derived from what the service itself declares, never from a list of
known services that would drift as the fleet changes:

```go
expected, err := sbom.ExpectedFromBuildPlan(service, plan, resolved)
err = sbom.ValidateCoverage(service, expected, resp)
```

`ExpectedFromBuildPlan` turns each recipe into one subject per shipped platform,
pinned to the digest the caller's build resolved for that platform. A recipe
names a tag, so those digests are the only thing binding a subject to the image
that was actually built; a recipe whose build reported none is an error rather
than a subject nothing can verify. `ExpectedFromBuildResult` does the same for
an agent-owned build, whose result already names its images.

`ExpectedFromImageReference` derives the expectation for an image the agent
names itself: one a service publishes from its own release pipeline, or a vendor
image it pins and deploys. Neither is described by a recipe or named by a build
result, so the reference the service ships is what the subjects come from, and
each shipped platform becomes one subject of the given role:

```go
expected, err := sbom.ExpectedFromImageReference(sbom.PublishedImage{
    Service:   service,
    Role:      "runtime",
    Reference: "ghcr.io/codefly-dev/gateway@sha256:...",
    Platforms: []string{"linux/amd64", "linux/arm64"},
})
```

Unlike the two build-derived expectations, this one is asserted by the caller
rather than read off something the service declared: nothing in the resource
model names a published image or the platforms it ships, so a caller that adds
`linux/arm64` without updating its expectation measures the new platform against
nothing. The service must be named — `ValidateCoverage` consults the service it
is given only for enumerated evidence, trusting an expectation to carry its own
identity, so a subject naming none matches evidence belonging to any service.

The reference has to be digest-pinned, and it is what carries the pin: each
platform's scan resolves a child manifest out of it, exactly as for a pushed
image a recipe built. Send those subjects as the request's `subjects` and
validate against the same list. Asked with empty subjects an agent enumerates
its own, whose role it picks for itself, and a subject matches evidence by
service, role, platform and repository together — so deriving here while
enumerating there describes one image under two keys and reports real coverage
as missing.

Deriving this expectation is worth more than letting the agent enumerate. An
empty expectation accepts enumerated evidence one inventory at a time, so a
multi-architecture image that answers for a single platform reads as covered;
measured against the platforms the service ships, the missing one is named.

`ValidateCoverage` is the single check every agent is measured against. It
rejects a source inventory, a non-complete response, evidence that is not bound
to a `sha256` digest, an empty inventory, a subject pinned to no digest, a
digest that differs from the deployed one, an omitted platform, enumerated
evidence that names no subject of the service being validated, an externally
managed claim that names no runtime, a no-image reason this contract does not
define, and a no-image claim that contradicts the declared build. An empty
expectation is not itself a pass: the response still has to carry either valid
enumerated evidence or an honest no-image reason.

## Advertising the capability

`ValidationCapabilities.image_sbom` is a separate advertisement from `sbom`, and
advertising it is a different act from serving image-scope requests.

Serving `SBOM_SCOPE_IMAGE` makes an agent *able* to answer. Advertising
`image_sbom` is what gets the work *scheduled*: `ciinputs.Required` emits a
`TASK_PHASE_IMAGE_SBOM` key only for an agent whose capability reports
`supported: true`, and that key is what a consumer plans, discovers effective
inputs for, and caches against. An agent may serve image scope while leaving the
capability unadvertised, which is the state to sit in until the gate below holds.

Advertising happens through `agentservices.Advertisement.Validation`: setting
`ImageSbom` to `supported: true` there is the whole act. A nil `Validation`
denotes a legacy agent whose RPCs must be compatibility-probed, while an explicit
empty message advertises that no validation operations are supported.

The bar for setting it is that the agent **serves image scope**, not merely that
`Builder.SBOM` exists — it already does for every agent serving source scope, so
its existence proves nothing here. An agent that advertises the capability and
then answers image-scope requests with `UNSUPPORTED` has made a false
advertisement: `UNSUPPORTED` means no implementation, so it and this
advertisement cannot both be true, and the phase is scheduled on every run
without ever being satisfied.

`sbom` never stands in for `image_sbom`. A source inventory proves nothing about
the contents of a shipped image, so an agent advertising `sbom` alone is
correctly read as having no image coverage.

### The rollout gate

The version floor binds on the **evaluating** binary, not on the agent. The
agent only advertises over the wire; the consumer that calls
`Agent.GetEffectiveInputs` and runs `ciinputs.Evaluate` is what has to
understand the phase, and it can be running an older core than the agent does.

A consumer below the floor rejects the phase because its own `validKey` bound
sits below `TASK_PHASE_IMAGE_SBOM`. A core reaches that state two ways — it
predates the `image_sbom` capability field, or it carries the field but predates
the raised bound — and the difference does not change the outcome. `validKey`
guards both the required-task inventory and every returned declaration, so the
rejection lands on whichever side reaches it first. Neither is fixable from the
request side: such a consumer cannot represent the phase at all.

For those consumers the outcome is not "image SBOM is skipped". `Evaluate`
returns an error and no tasks, so the agent's whole response is discarded and
every other phase — lint, compile, test — loses effective-input discovery with
it. That is a hard error, distinct from the conservative fallback used for an
`Unimplemented` RPC or an unrecognized response: there, tasks come back marked
conservative and uncacheable; here there is nothing to come back.

Current core no longer fails that way, and the difference is prospective only.
Since the fix for unrepresentable phases, a consumer drops a declaration whose
phase sits above every phase it can name and keeps discovery for the rest. That
protects consumers from phases added *after* `TASK_PHASE_IMAGE_SBOM`; it does
nothing for this one, because every core that mishandles phase 9 predates that
fix. A core that knows the phase but not the raised bound does not skip it
either — it can name the phase, so it takes the loud rejection instead.

An agent that serves image scope may therefore advertise `image_sbom` only once
every consumer that evaluates it runs a core whose `validKey` accepts the phase:
core released after `v0.3.31` (the fix in codefly-dev/core#501). Until that floor
holds across the fleet, serve the scope, satisfy `ValidateCoverage`, and leave
the capability unadvertised.
