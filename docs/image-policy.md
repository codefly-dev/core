# Image policy

`imagepolicy.Check(dockerfile, policy)` checks Dockerfile defaults against a
policy supplied by a deployment target. Requirements are data: Core contains
no customer registry or compliance-label roster. A zero `Policy` requires
nothing and performs no parsing.

## Rules

The checker returns all base-reference refusals in source order, followed by
missing final-stage labels in policy order. Each `Refusal` carries a line
(zero for file-wide findings), detail and a named error usable with `errors.Is`.

| Policy | Refusal | Meaning |
| --- | --- | --- |
| `RequireDigest` | `ErrUnpinnedBase` | a FROM base has no valid content digest |
| `AllowedRegistries` | `ErrRegistryNotAllowed` | a FROM base is outside the allowed registry hosts/repository paths |
| `RequiredLabels` | `ErrMissingLabel` | the final stage lacks a required label with a known, non-empty value |
| either reference rule | `ErrUnresolvedBase` | a base is invalid or depends on an argument without a known default |
| any active policy | `ErrInvalidDockerfile` | BuildKit cannot parse the Dockerfile, or it has no stage |

Registry matching uses normalized image names and complete path components:
`registry.example/team` accepts `registry.example/team/app`, but neither
`registry.example/team-evil/app` nor `registry.example.evil/team/app`.
`alpine` normalizes to `docker.io/library/alpine`. An empty registry list permits
any registry; an empty entry in a non-empty list permits none.

The checker reuses BuildKit's Dockerfile parser, instruction model and shell
expander, plus `distribution/reference` for image names and digests. This keeps
heredoc contents out of the instruction stream and handles continuation lines,
escape directives and quoted or legacy label syntax without another parser.

`scratch` needs neither a digest nor a registry. A reference to an earlier stage
inherits that stage's labels and variables; naming a stage does not exempt its
own external base. Only global ARG defaults resolve FROM instructions. Stage
ARG/ENV values resolve labels, with ENV taking precedence. Unrelated builder
labels do not satisfy the final stage, and an empty or unknown overwrite removes
a label's known value. Inherited stage maps are copied so sibling stages cannot
change one another's labels.

## Source-check boundary

This is a check of **Dockerfile defaults**, not a certificate for a built image.
It checks all FROM instructions and the last stage's labels. It does not read
external base-image labels, build argument overrides, automatic platform
arguments, named build contexts, external COPY/ADD sources or an alternate
`--target`. Unknown argument values (including shell fallback expressions over
undeclared variables) are refused conservatively when a reference rule needs
them. Declare defaults explicitly when checking source.

A build integration must validate its effective inputs and selected target.
Core does not yet connect this package to CLI builds, carry target policy in
coordinate declarations, or switch vulnerability auditing to enforcement.
Those changes belong to the CLI and policy producers, once the build location
and policy delivery are decided; see [configuration-contract.md](configuration-contract.md).

Artifact scanning is complementary. The existing `builderv0.AuditRequest`
`fail_on_vuln` flag asks for `state=ERROR` on HIGH-or-above findings and defaults
to false (report-only). `agents/services.BuilderWrapper.AuditContainer` applies
that contract to Trivy image reports. A target needs both source/input checks
and artifact auditing; passing this package alone does not prove an image is
vulnerability-free or compliant.

## Core's own images

`companions/imagepolicy_test.go` runs `RequireDigest` over every companion
Dockerfile, both runner builder templates (`runners/golang` and `runners/rust`),
and the publish-service-image action's smoke-test Dockerfile. The latter three
use scratch and require no exceptions.

The companion base-image argument's tag default is exempted only from the
unpinned-base refusal: the publisher overrides it with the digest it just
published. Existing companion build-spec tests enforce that default's shape.

Six distinct floating upstream bases remain explicitly listed in
`unpinnedUpstreamBases`. This is a **temporary stopgap**, not completed pinning:
new references fail and stale exceptions fail once no companion uses them.
Pinning them requires the companion build/publish contract in
[runbooks/publish-companions.md](runbooks/publish-companions.md). No companion
images are rebuilt or published by adding this guard.
