package sbom

import (
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

func imageEvidence(digest, platform string, subjects ...*builderv0.ImageSubject) *builderv0.ImageSBOM {
	return &builderv0.ImageSBOM{
		Digest:   digest,
		Platform: platform,
		Subjects: subjects,
		Bom:      &agentv0.Bom{Components: []*agentv0.Component{{Name: "openssl", Version: "3.0"}}},
		Tool:     "syft",
		Sha256:   "1f0c",
	}
}

func imageResponse(images ...*builderv0.ImageSBOM) *builderv0.SBOMResponse {
	return &builderv0.SBOMResponse{
		State:  &builderv0.SBOMStatus{State: builderv0.SBOMStatus_COMPLETE},
		Scope:  builderv0.SBOMScope_SBOM_SCOPE_IMAGE,
		Images: images,
	}
}

// The plan of a multi-architecture recipe, pinned by the digests its build
// resolved for each platform.
func multiPlatformExpectation(t *testing.T) []*builderv0.ImageSubject {
	t.Helper()
	expected, err := ExpectedFromBuildPlan("svc", &builderv0.DockerBuildPlan{Recipes: []*builderv0.DockerBuildRecipe{
		{Name: "app", Image: "ghcr.io/codefly-dev/app:1.0", Platforms: []string{"linux/amd64", "linux/arm64"}},
	}}, []ResolvedImage{
		{Recipe: "app", Platform: "linux/amd64", Digest: "sha256:index"},
		{Recipe: "app", Platform: "linux/arm64", Digest: "sha256:index"},
	})
	require.NoError(t, err)
	return expected
}

func TestExpectedFromBuildPlanCoversEveryPlatformAndRole(t *testing.T) {
	expected, err := ExpectedFromBuildPlan("svc", &builderv0.DockerBuildPlan{Recipes: []*builderv0.DockerBuildRecipe{
		{Name: "app", Image: "ghcr.io/codefly-dev/app:1.0", Platforms: []string{"linux/amd64", "linux/arm64"}},
		{Name: "migration", Image: "ghcr.io/codefly-dev/migration:1.0"},
	}}, []ResolvedImage{
		{Recipe: "app", Platform: "linux/amd64", Digest: "sha256:index"},
		{Recipe: "app", Platform: "linux/arm64", Digest: "sha256:index"},
		{Recipe: "migration", Digest: "sha256:mig"},
	})
	require.NoError(t, err)
	require.Len(t, expected, 3)
	require.Equal(t, "app", expected[0].GetRole())
	require.Equal(t, "linux/amd64", expected[0].GetPlatform())
	require.Equal(t, "ghcr.io/codefly-dev/app@sha256:index", expected[0].GetReference())
	require.Equal(t, "linux/arm64", expected[1].GetPlatform())
	require.Equal(t, "migration", expected[2].GetRole())
	require.Equal(t, "ghcr.io/codefly-dev/migration@sha256:mig", expected[2].GetReference())
	require.Equal(t, "svc", expected[2].GetService())
}

// A pushed image is pinned by its reference, so the scan resolves out of the
// image the build produced. The digest field stays empty because resolving a
// pushed image yields one platform's child manifest, which is never the index
// digest the caller holds — comparing the two rejected honest evidence.
func TestValidateCoverageAcceptsEvidenceBoundToTheChildOfAPinnedIndex(t *testing.T) {
	expected, err := ExpectedFromBuildPlan("svc", &builderv0.DockerBuildPlan{Recipes: []*builderv0.DockerBuildRecipe{
		{Name: "app", Image: "ghcr.io/codefly-dev/app:1.0"},
	}}, []ResolvedImage{{Recipe: "app", Digest: "sha256:index"}})
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/codefly-dev/app@sha256:index", expected[0].GetReference())
	require.Empty(t, expected[0].GetDigest())

	resp := imageResponse(imageEvidence("sha256:child", "linux/amd64", expected[0]))
	require.NoError(t, ValidateCoverage(expected, resp))
}

// An image only loaded into the daemon has no registry manifest to reference,
// so it keeps the tag the daemon knows and binds to the local image ID.
func TestExpectedFromBuildPlanBindsALocalImageToItsDaemonID(t *testing.T) {
	expected, err := ExpectedFromBuildPlan("svc", &builderv0.DockerBuildPlan{Recipes: []*builderv0.DockerBuildRecipe{
		{Name: "app", Image: "ghcr.io/codefly-dev/app:1.0"},
	}}, []ResolvedImage{{Recipe: "app", Digest: "sha256:localid", Source: SourceDockerDaemon}})
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/codefly-dev/app:1.0", expected[0].GetReference())
	require.Equal(t, "sha256:localid", expected[0].GetDigest())

	resp := imageResponse(imageEvidence("sha256:localid", "", expected[0]))
	require.NoError(t, ValidateCoverage(expected, resp))
}

// A recipe names a tag, so a platform the build reported no digest for cannot
// become a subject: the scan it asks for would bind evidence to whatever the
// registry currently serves under that tag.
func TestExpectedFromBuildPlanRejectsAnUnresolvedPlatform(t *testing.T) {
	_, err := ExpectedFromBuildPlan("svc", &builderv0.DockerBuildPlan{Recipes: []*builderv0.DockerBuildRecipe{
		{Name: "app", Image: "ghcr.io/codefly-dev/app:1.0", Platforms: []string{"linux/amd64", "linux/arm64"}},
	}}, []ResolvedImage{{Recipe: "app", Platform: "linux/amd64", Digest: "sha256:amd"}})
	require.ErrorContains(t, err, "resolved no digest")
	require.ErrorContains(t, err, "linux/arm64")
}

func TestExpectedFromBuildResultCarriesPinnedDigests(t *testing.T) {
	expected := ExpectedFromBuildResult("svc", &builderv0.DockerBuildResult{
		Images: []string{"ghcr.io/codefly-dev/app@sha256:abc"},
	})
	require.Len(t, expected, 1)
	require.Equal(t, "sha256:abc", expected[0].GetDigest())
}

func TestValidateCoverageAcceptsEvidenceForEveryPlatform(t *testing.T) {
	expected := multiPlatformExpectation(t)
	resp := imageResponse(
		imageEvidence("sha256:amd", "linux/amd64", expected[0]),
		imageEvidence("sha256:arm", "linux/arm64", expected[1]),
	)
	require.NoError(t, ValidateCoverage("svc", expected, resp))
}

func TestValidateCoverageRejectsAnOmittedPlatform(t *testing.T) {
	expected := multiPlatformExpectation(t)
	resp := imageResponse(imageEvidence("sha256:amd", "linux/amd64", expected[0]))
	require.ErrorContains(t, ValidateCoverage("svc", expected, resp), "linux/arm64")
}

func TestValidateCoverageRejectsASourceInventory(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_COMPLETE},
		Scope: builderv0.SBOMScope_SBOM_SCOPE_SOURCE,
		Bom:   &agentv0.Bom{Components: []*agentv0.Component{{Name: "left-pad"}}},
	}
	require.ErrorContains(t, ValidateCoverage("svc", expected, resp), "not image coverage")
}

func TestValidateCoverageRejectsAFailedScan(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_ERROR, Message: "syft exited 1"},
		Scope: builderv0.SBOMScope_SBOM_SCOPE_IMAGE,
	}
	require.ErrorContains(t, ValidateCoverage("svc", expected, resp), "syft exited 1")
}

// A failed response with no scope set must still report its own cause. Checking
// scope first turned every real scan failure into "not image coverage" and left
// the actual diagnostic unread in the message.
func TestValidateCoverageReportsTheCauseWhenScopeIsUnset(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_ERROR, Message: "syft exited 1: no space left on device"},
	}
	err := ValidateCoverage("svc", expected, resp)
	require.ErrorContains(t, err, "no space left on device")
	require.NotContains(t, err.Error(), "not image coverage")
}

func TestValidateCoverageRejectsConflictingEvidenceForOneSubject(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Platform: "linux/amd64", Role: "app", Service: "svc"}
	resp := imageResponse(
		imageEvidence("sha256:first", "linux/amd64", want),
		imageEvidence("sha256:second", "linux/amd64", want),
	)
	require.ErrorContains(t, ValidateCoverage("svc", []*builderv0.ImageSubject{want}, resp), "conflicting evidence")
}

func TestValidateCoverageRejectsEmptyCoverageWithoutAReason(t *testing.T) {
	require.ErrorContains(t, ValidateCoverage("svc", nil, imageResponse()), "must declare a no-image reason")
}

func TestValidateCoverageAcceptsADeclaredNoImageService(t *testing.T) {
	resp := imageResponse()
	resp.NoImageReason = builderv0.NoImageReason_NO_IMAGE_REASON_NO_IMAGE
	require.NoError(t, ValidateCoverage("svc", nil, resp))
}

// A stock-image service deploys a vendor image no recipe describes, so the
// caller derives no subjects for it. The evidence it enumerates itself is the
// only coverage such an image can have, and refusing it is what leaves a
// no-image claim as the agent's only answer.
func TestValidateCoverageAcceptsEnumeratedStockImageEvidence(t *testing.T) {
	deployed := &builderv0.ImageSubject{
		Reference: "docker.io/library/postgres@sha256:pg",
		Digest:    "sha256:pg",
		Platform:  "linux/amd64",
		Role:      "runtime",
		Service:   "svc",
	}
	require.NoError(t, ValidateCoverage("svc", nil, imageResponse(imageEvidence("sha256:pg", "linux/amd64", deployed))))
}

// A multi-architecture vendor image is pinned by its manifest-list reference,
// and each platform's scan binds evidence to the child manifest it resolved, so
// the subject carries no digest of its own and each platform still matches.
func TestValidateCoverageAcceptsAnIndexPinnedSubject(t *testing.T) {
	deployed := func(platform string) *builderv0.ImageSubject {
		return &builderv0.ImageSubject{
			Reference: "docker.io/library/postgres@sha256:index",
			Platform:  platform,
			Role:      "runtime",
			Service:   "svc",
		}
	}
	expected := []*builderv0.ImageSubject{deployed("linux/amd64"), deployed("linux/arm64")}
	resp := imageResponse(
		imageEvidence("sha256:amdchild", "linux/amd64", expected[0]),
		imageEvidence("sha256:armchild", "linux/arm64", expected[1]),
	)
	require.NoError(t, ValidateCoverage("svc", expected, resp))
}

// Copying the manifest-list digest into the subject is the mistake the pinning
// rule exists to prevent: a scan binds evidence to the child manifest, so the
// index digest matches none of the evidence the subject asks for.
func TestValidateCoverageRejectsAnIndexDigestCopiedIntoTheSubject(t *testing.T) {
	want := &builderv0.ImageSubject{
		Reference: "docker.io/library/postgres@sha256:index",
		Digest:    "sha256:index",
		Platform:  "linux/amd64",
		Role:      "runtime",
		Service:   "svc",
	}
	resp := imageResponse(imageEvidence("sha256:amdchild", "linux/amd64", want))
	require.ErrorContains(t, ValidateCoverage("svc", []*builderv0.ImageSubject{want}, resp), "not the deployed digest")
}

// Evidence an agent enumerated for itself is anchored by the service it names.
// A well-formed inventory for some other service covers nothing here: without
// that anchor any valid inventory at all would read as a pass, which is the
// false coverage this contract exists to refuse.
func TestValidateCoverageRejectsEnumeratedEvidenceForAnotherService(t *testing.T) {
	alien := &builderv0.ImageSubject{
		Reference: "docker.io/library/alpine:3.19",
		Digest:    "sha256:alien",
		Platform:  "linux/amd64",
		Role:      "runtime",
		Service:   "some-other-service",
	}
	resp := imageResponse(imageEvidence("sha256:alien", "linux/amd64", alien))
	require.ErrorContains(t, ValidateCoverage("svc", nil, resp), "names no subject belonging to svc")
}

// Enumerated evidence cannot be judged without knowing which service it should
// cover, so a caller that supplies no identity is refused rather than trusted.
func TestValidateCoverageRejectsEnumeratedEvidenceWithoutAServiceIdentity(t *testing.T) {
	deployed := &builderv0.ImageSubject{
		Reference: "docker.io/library/postgres@sha256:pg",
		Digest:    "sha256:pg",
		Platform:  "linux/amd64",
		Role:      "runtime",
		Service:   "svc",
	}
	resp := imageResponse(imageEvidence("sha256:pg", "linux/amd64", deployed))
	require.ErrorContains(t, ValidateCoverage("", nil, resp), "without the identity of the service")
}

// A reason this contract does not define is refused: an older validator cannot
// vouch for a claim whose meaning it does not know.
func TestValidateCoverageRejectsAnUndefinedNoImageReason(t *testing.T) {
	resp := imageResponse()
	resp.NoImageReason = builderv0.NoImageReason(99)
	require.ErrorContains(t, ValidateCoverage("svc", nil, resp), "not one this contract defines")
}

// Enumerated evidence is held to the same standard as evidence for a derived
// subject, so an empty expectation is not a way to report an unbound scan.
func TestValidateCoverageRejectsUnboundEnumeratedEvidence(t *testing.T) {
	deployed := &builderv0.ImageSubject{Reference: "docker.io/library/postgres:16", Role: "runtime", Service: "svc"}
	resp := imageResponse(imageEvidence("", "", deployed))
	require.ErrorContains(t, ValidateCoverage("svc", nil, resp), "not bound to a sha256 digest")
}

// The reason exists for a runtime an external provider owns. Naming that
// runtime is what separates the claim from a stock-image service asserting it
// ships nothing while deploying a vendor image.
func TestValidateCoverageRejectsAnExternallyManagedClaimNamingNoRuntime(t *testing.T) {
	resp := imageResponse()
	resp.NoImageReason = builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED
	require.ErrorContains(t, ValidateCoverage("svc", nil, resp), "must name the runtime")
}

func TestValidateCoverageAcceptsAnExternallyManagedRuntimeThatIsNamed(t *testing.T) {
	resp := imageResponse()
	resp.State.Message = "runs on a provider-managed RDS instance"
	resp.NoImageReason = builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED
	require.NoError(t, ValidateCoverage("svc", nil, resp))
}

func TestValidateCoverageRejectsANoImageReasonWhenImagesAreExpected(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := imageResponse()
	resp.NoImageReason = builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED
	require.ErrorContains(t, ValidateCoverage("svc", expected, resp), "1 images are expected")
}

func TestValidateCoverageRejectsAStaleDigest(t *testing.T) {
	want := &builderv0.ImageSubject{
		Reference: "ghcr.io/codefly-dev/app@sha256:deployed",
		Digest:    "sha256:deployed",
		Platform:  "linux/amd64",
		Role:      "app",
		Service:   "svc",
	}
	resp := imageResponse(imageEvidence("sha256:other", "linux/amd64", want))
	require.ErrorContains(t, ValidateCoverage("svc", []*builderv0.ImageSubject{want}, resp), "not the deployed digest")
}

// Evidence for a subject pinned to nothing cannot be coverage: the scan bound
// itself to whatever the tag served, which nothing compared to the built image.
func TestValidateCoverageRejectsAnUnpinnedSubject(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Platform: "linux/amd64", Role: "app", Service: "svc"}
	resp := imageResponse(imageEvidence("sha256:whatever", "linux/amd64", want))
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp), "not pinned to a sha256 digest")
}

// A digest that is not a sha256 pin cannot match evidence, which validateEvidence
// already requires to be sha256-bound. Accepting it as "pinned" only deferred the
// failure to a mismatch that blamed the evidence for a malformed expectation.
func TestValidateCoverageRejectsAMalformedDigestPin(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Digest: "latest", Role: "app", Service: "svc"}
	resp := imageResponse(imageEvidence("sha256:abc", "", want))
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp), "not pinned to a sha256 digest")
}

// A malformed subject is reported as itself, not as whatever mismatch another
// subject happens to produce first.
func TestValidateCoverageReportsAnUnpinnedSubjectBeforeAStaleDigest(t *testing.T) {
	stale := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/a@sha256:deployed", Digest: "sha256:deployed", Role: "a", Service: "svc"}
	unpinned := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/b:1.0", Role: "b", Service: "svc"}
	resp := imageResponse(
		imageEvidence("sha256:other", "", stale),
		imageEvidence("sha256:whatever", "", unpinned),
	)

	err := ValidateCoverage([]*builderv0.ImageSubject{stale, unpinned}, resp)
	require.ErrorContains(t, err, "not pinned to a sha256 digest")
	require.NotContains(t, err.Error(), "not the deployed digest")
}

// An agent-owned build result that names a tag yields an unpinned subject. It
// used to pass coverage with the digest guard skipped; it is now refused.
func TestValidateCoverageRejectsABuildResultThatNamesOnlyATag(t *testing.T) {
	expected := ExpectedFromBuildResult("svc", &builderv0.DockerBuildResult{
		Images: []string{"ghcr.io/codefly-dev/app:1.0"},
	})
	resp := imageResponse(imageEvidence("sha256:whatever", "", expected[0]))
	require.ErrorContains(t, ValidateCoverage(expected, resp), "not pinned to a sha256 digest")
}

func TestValidateCoverageRejectsAnEmptyInventory(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}
	evidence := imageEvidence("sha256:abc", "", want)
	evidence.Bom = nil
	require.ErrorContains(t, ValidateCoverage("svc", []*builderv0.ImageSubject{want}, imageResponse(evidence)), "empty inventory")
}

func TestValidateCoverageRejectsUnboundEvidence(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}
	resp := imageResponse(imageEvidence("", "", want))
	require.ErrorContains(t, ValidateCoverage("svc", []*builderv0.ImageSubject{want}, resp), "not bound to a sha256 digest")
}

// A digest shared by two services is scanned once, and both services must still
// read as covered.
func TestValidateCoverageDeduplicatesADigestAcrossServices(t *testing.T) {
	shared := []*builderv0.ImageSubject{
		{Reference: "ghcr.io/codefly-dev/base:1.0", Digest: "sha256:same", Platform: "linux/amd64", Role: "runtime", Service: "alpha"},
		{Reference: "ghcr.io/codefly-dev/base:1.0", Digest: "sha256:same", Platform: "linux/amd64", Role: "runtime", Service: "beta"},
	}
	resp := imageResponse(imageEvidence("sha256:same", "linux/amd64", shared...))
	require.Len(t, resp.GetImages(), 1)
	require.NoError(t, ValidateCoverage("alpha", shared, resp))
}

// A subject whose reference is a tag and evidence naming the digest that tag
// resolved to are the same subject.
func TestValidateCoverageMatchesATagToItsResolvedDigest(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Digest: "sha256:abc", Platform: "linux/amd64", Role: "app", Service: "svc"}
	evidenceSubject := &builderv0.ImageSubject{
		Reference: "ghcr.io/codefly-dev/app@sha256:abc",
		Digest:    "sha256:abc",
		Platform:  "linux/amd64",
		Role:      "app",
		Service:   "svc",
	}
	resp := imageResponse(imageEvidence("sha256:abc", "linux/amd64", evidenceSubject))
	require.NoError(t, ValidateCoverage("svc", []*builderv0.ImageSubject{want}, resp))
}
