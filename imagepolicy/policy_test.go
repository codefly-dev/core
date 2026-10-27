package imagepolicy_test

import (
	"errors"
	"testing"

	"github.com/codefly-dev/core/imagepolicy"
)

// Every Dockerfile body below is the shape a real image in the fleet is built
// from, reduced to the instructions the policy reads. Checking invented shapes
// would prove the regexes work; checking these proves the rules land on what we
// actually ship.

// pinned is the service-go-grpc runtime stage: a digest-pinned Alpine with no
// label of any kind. It satisfies a digest rule and fails a label rule, which
// is exactly the fleet's position today.
const pinned = `
FROM alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
RUN apk add --no-cache ca-certificates
USER 65532:65532
CMD ["./app"]
`

func rules(refusals []imagepolicy.Refusal) []error {
	out := make([]error, 0, len(refusals))
	for _, refusal := range refusals {
		out = append(out, refusal.Rule)
	}
	return out
}

func requireRules(t *testing.T, refusals []imagepolicy.Refusal, want ...error) {
	t.Helper()
	got := rules(refusals)
	if len(got) != len(want) {
		t.Fatalf("got %d refusals %v, want %d %v", len(got), got, len(want), want)
	}
	for i, rule := range want {
		if !errors.Is(refusals[i], rule) {
			t.Fatalf("refusal %d = %v, want %v", i, refusals[i], rule)
		}
	}
}

func TestDigestPinnedBasePasses(t *testing.T) {
	requireRules(t, imagepolicy.Check(pinned, imagepolicy.Policy{RequireDigest: true}))
}

// service-rust's runtime and obin service-xlsx both name a floating tag. These
// are not merely unscanned, they are unreproducible: the bytes behind the tag
// move, so a clean scan describes an image that may no longer exist.
func TestFloatingTagIsRefused(t *testing.T) {
	for _, body := range []string{"FROM debian:bookworm-slim\n", "FROM node:26-slim\n", "FROM golang:1.27\n"} {
		requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true}), imagepolicy.ErrUnpinnedBase)
	}
}

// scratch has no registry to allow and no digest to pin, so the reference
// rules cannot apply. This is the whole reason a scratch image satisfies a
// registry mandate with nothing to migrate.
func TestScratchSatisfiesEveryReferenceRule(t *testing.T) {
	body := "FROM scratch\nCOPY app /app\nUSER 65532:65532\n"
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{
		RequireDigest:     true,
		AllowedRegistries: []string{"nylartifactory.jfrog.io/devsecops-dkr-prod-virtual"},
	}))
}

// service-postgres, service-redis, minio and service-vault all write
// `ARG X=<pinned>` then `FROM ${X}`. A reader that skipped the substitution
// would pass all four without ever seeing a reference.
func TestBaseFromArgumentDefaultIsResolved(t *testing.T) {
	body := `
ARG POSTGRES_IMAGE=postgres:17.10-alpine3.24@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193
FROM ${POSTGRES_IMAGE} AS runtime
`
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true}))

	floating := "ARG REDIS_IMAGE=redis:8.8.0-alpine\nFROM ${REDIS_IMAGE}\n"
	requireRules(t, imagepolicy.Check(floating, imagepolicy.Policy{RequireDigest: true}), imagepolicy.ErrUnpinnedBase)
}

// An argument with no default in the file leaves the base genuinely unknown.
// Reporting that is not pedantry: passing it would certify a file that does
// not say what it builds on.
func TestUnresolvableBaseIsReported(t *testing.T) {
	requireRules(t, imagepolicy.Check("FROM ${SUPPLIED_ELSEWHERE}\n", imagepolicy.Policy{RequireDigest: true}),
		imagepolicy.ErrUnresolvedBase)
}

// service-nextjs builds four stages on one another (`FROM base AS deps`). A
// stage name is an edge inside the file, not a registry reference, and holding
// it to the reference rules would refuse every multi-stage build we have.
func TestStageReferencesAreNotTreatedAsRegistries(t *testing.T) {
	body := `
FROM node:24.17.0-alpine3.23@sha256:7c70d1235c0b4c2bc9eeed5393d19f1bbdde6885ba0d58ba62bb385d7b0f3ff1 AS base
FROM base AS deps
FROM base AS builder
FROM base AS runner
COPY --from=builder /app /app
`
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true}))
}

// service-vault and minio put --platform=$BUILDPLATFORM before the reference.
// Reading the flag as the base would make every one of them unresolvable.
func TestPlatformFlagIsNotTheReference(t *testing.T) {
	body := "FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine3.24@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS source\n"
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true}))
}

// The registry rule is per target and the lists conflict between targets, so
// the same file passes one policy and fails another. Nothing here is written
// for a particular customer.
func TestRegistryAllowListIsPerTarget(t *testing.T) {
	body := "FROM alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40\n"

	mirrored := imagepolicy.Policy{AllowedRegistries: []string{"example.jfrog.io/virtual"}}
	requireRules(t, imagepolicy.Check(body, mirrored), imagepolicy.ErrRegistryNotAllowed)

	remapped := "FROM example.jfrog.io/virtual/alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40\n"
	requireRules(t, imagepolicy.Check(remapped, mirrored))

	// A target that names no registries permits any, so a policy opts in.
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{}))
}

// A compliance programme supplies its label keys as data. Today every template
// that renders a deployed service image sets no label at all, so all of them
// are reported, each once, rather than the first one found.
func TestRequiredLabelsAreReportedIndividually(t *testing.T) {
	required := []string{"APPID", "base.name", "Technology"}
	refusals := imagepolicy.Check(pinned, imagepolicy.Policy{RequiredLabels: required})
	requireRules(t, refusals, imagepolicy.ErrMissingLabel, imagepolicy.ErrMissingLabel, imagepolicy.ErrMissingLabel)
	for i, refusal := range refusals {
		if refusal.Detail != required[i] {
			t.Fatalf("refusal %d names %q, want %q", i, refusal.Detail, required[i])
		}
	}
}

// Labels survive continuation lines and quoted values with spaces, which is
// how the postgres bootstrap template already writes its provenance block.
func TestLabelsAreReadAcrossContinuationsAndQuotes(t *testing.T) {
	body := `
FROM alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
LABEL APPID="APP-123" \
      description="Image description and app details" \
      Technology="Go"
LABEL base.name="example.jfrog.io/virtual/alpine:3.23.5"
`
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{
		RequiredLabels: []string{"APPID", "description", "Technology", "base.name"},
	}))
}

// A label present but empty is not set: it would satisfy a grep and tell a
// compliance reader nothing.
func TestEmptyLabelDoesNotSatisfyTheRule(t *testing.T) {
	body := "FROM scratch\nLABEL APPID=\"\"\n"
	requireRules(t, imagepolicy.Check(body, imagepolicy.Policy{RequiredLabels: []string{"APPID"}}),
		imagepolicy.ErrMissingLabel)
}

// A zero Policy requires nothing, so a target inherits no rule it did not ask
// for and adding a rule here cannot retroactively fail an existing target.
func TestZeroPolicyRequiresNothing(t *testing.T) {
	requireRules(t, imagepolicy.Check("FROM debian:bookworm-slim\n", imagepolicy.Policy{}))
}

// A refusal carries the line so the author is sent to the instruction.
func TestRefusalCarriesTheLine(t *testing.T) {
	body := "# a comment\n\nFROM debian:bookworm-slim\n"
	refusals := imagepolicy.Check(body, imagepolicy.Policy{RequireDigest: true})
	if len(refusals) != 1 || refusals[0].Line != 3 {
		t.Fatalf("refusals = %v, want one on line 3", refusals)
	}
}
