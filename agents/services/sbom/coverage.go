package sbom

import (
	"fmt"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// ExpectedFromBuildPlan derives the image subjects a caller-owned build
// produces. Each recipe contributes one subject per shipped platform, so a
// multi-architecture recipe is not satisfied by evidence for a single platform.
func ExpectedFromBuildPlan(service string, plan *builderv0.DockerBuildPlan) []*builderv0.ImageSubject {
	var subjects []*builderv0.ImageSubject
	for _, recipe := range plan.GetRecipes() {
		platforms := recipe.GetPlatforms()
		if len(platforms) == 0 {
			platforms = []string{""}
		}
		for _, platform := range platforms {
			subjects = append(subjects, &builderv0.ImageSubject{
				Reference: recipe.GetImage(),
				Digest:    referenceDigest(recipe.GetImage()),
				Platform:  platform,
				Role:      recipe.GetName(),
				Service:   service,
			})
		}
	}
	return subjects
}

// ExpectedFromBuildResult derives the image subjects an agent-owned build
// produced. A build result names images without roles, so the reference carries
// the subject's identity.
func ExpectedFromBuildResult(service string, result *builderv0.DockerBuildResult) []*builderv0.ImageSubject {
	var subjects []*builderv0.ImageSubject
	for _, image := range result.GetImages() {
		subjects = append(subjects, &builderv0.ImageSubject{
			Reference: image,
			Digest:    referenceDigest(image),
			Service:   service,
		})
	}
	return subjects
}

// ValidateCoverage reports whether a response carries image evidence for every
// expected subject. It is the conformance check every agent is measured
// against: the expectation comes from the build the service itself declares, so
// no list of known services has to be kept in sync with the fleet.
func ValidateCoverage(expected []*builderv0.ImageSubject, resp *builderv0.SBOMResponse) error {
	// State is checked before scope: a failed response carries the real cause in
	// its message, and reporting it as a scope problem would hide that.
	if state := resp.GetState().GetState(); state != builderv0.SBOMStatus_COMPLETE {
		return fmt.Errorf("image SBOM is %s: %s", state, resp.GetState().GetMessage())
	}
	if resp.GetScope() != builderv0.SBOMScope_SBOM_SCOPE_IMAGE {
		return fmt.Errorf("response scope is %s: a source inventory is not image coverage", resp.GetScope())
	}
	if len(expected) == 0 {
		// An empty expectation is not evidence that the service ships nothing:
		// subjects are derived from recipes, which cannot see a vendor image the
		// service deploys without building. Evidence such an agent enumerates
		// itself is real coverage and is validated rather than refused, because
		// refusing it leaves a no-image claim as the only answer it can give.
		if len(resp.GetImages()) > 0 {
			if reason := resp.GetNoImageReason(); reason != builderv0.NoImageReason_NO_IMAGE_REASON_UNSPECIFIED {
				return fmt.Errorf("response declares no-image reason %s but carries %d inventories", reason, len(resp.GetImages()))
			}
			for _, evidence := range resp.GetImages() {
				if err := validateEvidence(evidence); err != nil {
					return err
				}
			}
			return nil
		}
		switch resp.GetNoImageReason() {
		case builderv0.NoImageReason_NO_IMAGE_REASON_UNSPECIFIED:
			return fmt.Errorf("a complete image SBOM covering no images must declare a no-image reason")
		case builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED:
			if resp.GetState().GetMessage() == "" {
				return fmt.Errorf("an externally managed claim must name the runtime that owns the image: a vendor image this service pins and deploys is its own shipped image and owes evidence")
			}
		}
		return nil
	}
	if reason := resp.GetNoImageReason(); reason != builderv0.NoImageReason_NO_IMAGE_REASON_UNSPECIFIED {
		return fmt.Errorf("response declares no-image reason %s but %d images are expected", reason, len(expected))
	}
	covered := map[string]*builderv0.ImageSBOM{}
	for _, evidence := range resp.GetImages() {
		if err := validateEvidence(evidence); err != nil {
			return err
		}
		for _, subject := range evidence.GetSubjects() {
			key := subjectKey(subject)
			if previous, ok := covered[key]; ok && previous.GetDigest() != evidence.GetDigest() {
				return fmt.Errorf("%s has conflicting evidence: digests %s and %s", subjectLabel(subject), previous.GetDigest(), evidence.GetDigest())
			}
			covered[key] = evidence
		}
	}
	var missing []string
	for _, want := range expected {
		evidence, ok := covered[subjectKey(want)]
		if !ok {
			missing = append(missing, subjectLabel(want))
			continue
		}
		if digest := want.GetDigest(); digest != "" && digest != evidence.GetDigest() {
			return fmt.Errorf("%s carries evidence for digest %s, not the deployed digest %s", subjectLabel(want), evidence.GetDigest(), digest)
		}
		if platform := want.GetPlatform(); platform != "" && platform != evidence.GetPlatform() {
			return fmt.Errorf("%s carries evidence for platform %s", subjectLabel(want), evidence.GetPlatform())
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("no image SBOM evidence for %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateEvidence(evidence *builderv0.ImageSBOM) error {
	if !strings.HasPrefix(evidence.GetDigest(), "sha256:") {
		return fmt.Errorf("image evidence %q is not bound to a sha256 digest", evidence.GetDigest())
	}
	if len(evidence.GetSubjects()) == 0 {
		return fmt.Errorf("image evidence for %s names no service subject", evidence.GetDigest())
	}
	if len(evidence.GetBom().GetComponents()) == 0 {
		return fmt.Errorf("image evidence for %s carries an empty inventory", evidence.GetDigest())
	}
	if evidence.GetSha256() == "" {
		return fmt.Errorf("image evidence for %s carries no document checksum", evidence.GetDigest())
	}
	return nil
}

// subjectKey matches an expectation to evidence by identity rather than by the
// exact reference string, so a plan naming a tag and evidence naming the digest
// it resolved to are the same subject.
func subjectKey(subject *builderv0.ImageSubject) string {
	return strings.Join([]string{
		subject.GetService(),
		subject.GetRole(),
		subject.GetPlatform(),
		referenceName(subject.GetReference()),
	}, "|")
}

func subjectLabel(subject *builderv0.ImageSubject) string {
	label := subject.GetReference()
	if role := subject.GetRole(); role != "" {
		label = fmt.Sprintf("%s image %s", role, label)
	}
	if platform := subject.GetPlatform(); platform != "" {
		label += " on " + platform
	}
	return label
}

func referenceDigest(reference string) string {
	if _, digest, found := strings.Cut(reference, "@"); found {
		return digest
	}
	return ""
}

// referenceName strips any tag or digest, leaving the repository name. It
// accounts for a registry host that carries a port.
func referenceName(reference string) string {
	if base, _, found := strings.Cut(reference, "@"); found {
		reference = base
	}
	if slash := strings.LastIndex(reference, "/"); slash >= 0 {
		if colon := strings.LastIndex(reference[slash:], ":"); colon >= 0 {
			return reference[:slash+colon]
		}
		return reference
	}
	if colon := strings.LastIndex(reference, ":"); colon >= 0 {
		return reference[:colon]
	}
	return reference
}
