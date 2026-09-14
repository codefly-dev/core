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
		{Recipe: "app", Platform: "linux/amd64", Digest: "sha256:amd"},
		{Recipe: "app", Platform: "linux/arm64", Digest: "sha256:arm"},
	})
	require.NoError(t, err)
	return expected
}

func TestExpectedFromBuildPlanCoversEveryPlatformAndRole(t *testing.T) {
	expected, err := ExpectedFromBuildPlan("svc", &builderv0.DockerBuildPlan{Recipes: []*builderv0.DockerBuildRecipe{
		{Name: "app", Image: "ghcr.io/codefly-dev/app:1.0", Platforms: []string{"linux/amd64", "linux/arm64"}},
		{Name: "migration", Image: "ghcr.io/codefly-dev/migration:1.0"},
	}}, []ResolvedImage{
		{Recipe: "app", Platform: "linux/amd64", Digest: "sha256:amd"},
		{Recipe: "app", Platform: "linux/arm64", Digest: "sha256:arm"},
		{Recipe: "migration", Digest: "sha256:mig"},
	})
	require.NoError(t, err)
	require.Len(t, expected, 3)
	require.Equal(t, "app", expected[0].GetRole())
	require.Equal(t, "linux/amd64", expected[0].GetPlatform())
	require.Equal(t, "sha256:amd", expected[0].GetDigest())
	require.Equal(t, "linux/arm64", expected[1].GetPlatform())
	require.Equal(t, "sha256:arm", expected[1].GetDigest())
	require.Equal(t, "migration", expected[2].GetRole())
	require.Equal(t, "sha256:mig", expected[2].GetDigest())
	require.Equal(t, "svc", expected[2].GetService())
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
	require.NoError(t, ValidateCoverage(expected, resp))
}

func TestValidateCoverageRejectsAnOmittedPlatform(t *testing.T) {
	expected := multiPlatformExpectation(t)
	resp := imageResponse(imageEvidence("sha256:amd", "linux/amd64", expected[0]))
	require.ErrorContains(t, ValidateCoverage(expected, resp), "linux/arm64")
}

func TestValidateCoverageRejectsASourceInventory(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_COMPLETE},
		Scope: builderv0.SBOMScope_SBOM_SCOPE_SOURCE,
		Bom:   &agentv0.Bom{Components: []*agentv0.Component{{Name: "left-pad"}}},
	}
	require.ErrorContains(t, ValidateCoverage(expected, resp), "not image coverage")
}

func TestValidateCoverageRejectsAFailedScan(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_ERROR, Message: "syft exited 1"},
		Scope: builderv0.SBOMScope_SBOM_SCOPE_IMAGE,
	}
	require.ErrorContains(t, ValidateCoverage(expected, resp), "syft exited 1")
}

// A failed response with no scope set must still report its own cause. Checking
// scope first turned every real scan failure into "not image coverage" and left
// the actual diagnostic unread in the message.
func TestValidateCoverageReportsTheCauseWhenScopeIsUnset(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := &builderv0.SBOMResponse{
		State: &builderv0.SBOMStatus{State: builderv0.SBOMStatus_ERROR, Message: "syft exited 1: no space left on device"},
	}
	err := ValidateCoverage(expected, resp)
	require.ErrorContains(t, err, "no space left on device")
	require.NotContains(t, err.Error(), "not image coverage")
}

func TestValidateCoverageRejectsConflictingEvidenceForOneSubject(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Platform: "linux/amd64", Role: "app", Service: "svc"}
	resp := imageResponse(
		imageEvidence("sha256:first", "linux/amd64", want),
		imageEvidence("sha256:second", "linux/amd64", want),
	)
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp), "conflicting evidence")
}

func TestValidateCoverageRejectsEmptyCoverageWithoutAReason(t *testing.T) {
	require.ErrorContains(t, ValidateCoverage(nil, imageResponse()), "must declare a no-image reason")
}

func TestValidateCoverageAcceptsADeclaredNoImageService(t *testing.T) {
	resp := imageResponse()
	resp.NoImageReason = builderv0.NoImageReason_NO_IMAGE_REASON_NO_IMAGE
	require.NoError(t, ValidateCoverage(nil, resp))
}

func TestValidateCoverageRejectsANoImageReasonWhenImagesAreExpected(t *testing.T) {
	expected := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}
	resp := imageResponse()
	resp.NoImageReason = builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED
	require.ErrorContains(t, ValidateCoverage(expected, resp), "1 images are expected")
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
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp), "not the deployed digest")
}

// Evidence for a subject pinned to nothing cannot be coverage: the scan bound
// itself to whatever the tag served, which nothing compared to the built image.
func TestValidateCoverageRejectsAnUnpinnedSubject(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Platform: "linux/amd64", Role: "app", Service: "svc"}
	resp := imageResponse(imageEvidence("sha256:whatever", "linux/amd64", want))
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp), "names no digest")
}

func TestValidateCoverageRejectsAnEmptyInventory(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}
	evidence := imageEvidence("sha256:abc", "", want)
	evidence.Bom = nil
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, imageResponse(evidence)), "empty inventory")
}

func TestValidateCoverageRejectsUnboundEvidence(t *testing.T) {
	want := &builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}
	resp := imageResponse(imageEvidence("", "", want))
	require.ErrorContains(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp), "not bound to a sha256 digest")
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
	require.NoError(t, ValidateCoverage(shared, resp))
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
	require.NoError(t, ValidateCoverage([]*builderv0.ImageSubject{want}, resp))
}
