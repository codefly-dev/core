package sbom

import (
	"fmt"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// ResolvedImage is one image a completed build produced: the recipe that built
// it, the platform it ships, and the immutable identity it resolved to. Source
// states what that identity is, because the two are not interchangeable: a
// registry digest names a manifest, while an image only loaded into the daemon
// has nothing but a local image ID.
type ResolvedImage struct {
	Recipe   string
	Platform string
	Digest   string
	Source   ImageSource
}

// ExpectedFromBuildPlan derives the image subjects a caller-owned build
// produces. Each recipe contributes one subject per shipped platform, so a
// multi-architecture recipe is not satisfied by evidence for a single platform.
//
// A recipe names a tag, so what the caller resolved from its build is what pins
// each subject. Without it a subject names a floating tag and its evidence
// describes whatever that tag happens to serve.
func ExpectedFromBuildPlan(service string, plan *builderv0.DockerBuildPlan, resolved []ResolvedImage) ([]*builderv0.ImageSubject, error) {
	built := make(map[string]ResolvedImage, len(resolved))
	for _, image := range resolved {
		built[image.Recipe+"|"+image.Platform] = image
	}
	var subjects []*builderv0.ImageSubject
	for _, recipe := range plan.GetRecipes() {
		platforms := recipe.GetPlatforms()
		if len(platforms) == 0 {
			platforms = []string{""}
		}
		for _, platform := range platforms {
			image := built[recipe.GetName()+"|"+platform]
			if !strings.HasPrefix(image.Digest, "sha256:") {
				where := fmt.Sprintf("%s image %s", recipe.GetName(), recipe.GetImage())
				if platform != "" {
					where += " on " + platform
				}
				return nil, fmt.Errorf("the build resolved no digest for %s: a subject carrying only a tag binds evidence to whatever that tag serves, not to the image that was built", where)
			}
			subject := &builderv0.ImageSubject{
				Platform: platform,
				Role:     recipe.GetName(),
				Service:  service,
			}
			if image.Source == SourceDockerDaemon {
				// A never-pushed image has no registry manifest to reference, so
				// the daemon keeps the tag it knows and the local image ID is the
				// identity a scan of it binds evidence to.
				subject.Reference = recipe.GetImage()
				subject.Digest = image.Digest
			} else {
				// Pinning the reference makes the scan resolve out of the image
				// the build produced, so evidence is derived from that identity
				// rather than compared against it. Comparing would reject honest
				// evidence: resolving a pushed image yields the digest of one
				// platform's child manifest, never the index digest the caller
				// holds.
				subject.Reference = pinnedReference(recipe.GetImage(), image.Digest)
			}
			subjects = append(subjects, subject)
		}
	}
	return subjects, nil
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

// ExpectedFromImageReference derives the image subjects of an image the agent
// names itself rather than one the caller builds: an image the service
// publishes from its own release pipeline, or a vendor image it pins and
// deploys. No recipe describes such an image and no build result names it, so
// the reference the service ships is what the expectation is derived from.
//
// Each shipped platform is its own subject, which is what an expectation states
// that evidence cannot: enumerated evidence is judged one inventory at a time,
// so a multi-architecture image answering for a single platform reads as
// covered until something says which platforms were expected.
//
// The reference carries the pin, as it does for any pushed image: each
// platform's scan resolves a child manifest out of it, so evidence is derived
// from that identity rather than compared against it.
func ExpectedFromImageReference(service, role, reference string, platforms []string) ([]*builderv0.ImageSubject, error) {
	if !strings.HasPrefix(referenceDigest(reference), "sha256:") {
		return nil, fmt.Errorf("published image %s is not pinned to a sha256 digest: a tag serves whatever was pushed to it last, so its inventory is not coverage of the image this service ships", reference)
	}
	if len(platforms) == 0 {
		platforms = []string{""}
	}
	var subjects []*builderv0.ImageSubject
	for _, platform := range platforms {
		subjects = append(subjects, &builderv0.ImageSubject{
			Reference: reference,
			Platform:  platform,
			Role:      role,
			Service:   service,
		})
	}
	return subjects, nil
}

// ValidateCoverage reports whether a response carries image evidence for every
// expected subject. It is the conformance check every agent is measured
// against: the expectation comes from the build the service itself declares, so
// no list of known services has to be kept in sync with the fleet.
//
// service is the service under evaluation. A derived expectation carries that
// identity in its own subjects, but evidence an agent enumerates for itself is
// anchored to nothing the caller derived, so the identity has to be supplied.
func ValidateCoverage(service string, expected []*builderv0.ImageSubject, resp *builderv0.SBOMResponse) error {
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
			return validateEnumeratedCoverage(service, resp.GetImages())
		}
		return ValidateNoImageReason(resp.GetNoImageReason(), resp.GetState().GetMessage())
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
	// Well-formedness of the expectation is settled before any of it is matched,
	// so a malformed subject is reported as itself rather than as whatever
	// mismatch another subject happens to produce first.
	for _, want := range expected {
		if err := RequirePinned(want); err != nil {
			return err
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

// validateEnumeratedCoverage accepts evidence an agent enumerated for itself,
// which no derived expectation names. Each inventory has to name a subject
// belonging to the service under evaluation: evidence for some other service is
// not this service's coverage, and without that anchor any well-formed
// inventory at all would read as a pass.
func validateEnumeratedCoverage(service string, images []*builderv0.ImageSBOM) error {
	if service == "" {
		return fmt.Errorf("enumerated image evidence cannot be accepted without the identity of the service it covers")
	}
	for _, evidence := range images {
		if err := validateEvidence(evidence); err != nil {
			return err
		}
		if !coversService(evidence, service) {
			return fmt.Errorf("image evidence for %s names no subject belonging to %s", evidence.GetDigest(), service)
		}
	}
	return nil
}

func coversService(evidence *builderv0.ImageSBOM, service string) bool {
	for _, subject := range evidence.GetSubjects() {
		if subject.GetService() == service {
			return true
		}
	}
	return false
}

// ValidateNoImageReason reports whether a no-image claim is one an agent may
// make. It is the single definition of that rule: the wrapper refuses to build
// such a response and coverage refuses to accept one, so the two cannot drift.
// EXTERNALLY_MANAGED means an external provider owns the runtime, so the
// response has to say which — a vendor image the service pins and deploys is
// its own shipped image and owes evidence instead. A reason this contract does
// not define is refused rather than trusted.
func ValidateNoImageReason(reason builderv0.NoImageReason, message string) error {
	switch reason {
	case builderv0.NoImageReason_NO_IMAGE_REASON_UNSPECIFIED:
		return fmt.Errorf("a complete image SBOM covering no images must declare a no-image reason")
	case builderv0.NoImageReason_NO_IMAGE_REASON_NO_IMAGE:
		return nil
	case builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED:
		if message == "" {
			return fmt.Errorf("an externally managed claim must name the runtime that owns the image: a vendor image this service pins and deploys is its own shipped image and owes evidence")
		}
		return nil
	default:
		return fmt.Errorf("no-image reason %s is not one this contract defines", reason)
	}
}

// RequirePinned rejects a subject that names no immutable image, either in its
// digest field or in its reference. A scan of a floating tag inventories
// whatever the registry serves at that moment, so reporting it as coverage
// asserts something about an image nobody checked was the one built.
func RequirePinned(subject *builderv0.ImageSubject) error {
	if strings.HasPrefix(subject.GetDigest(), "sha256:") || strings.HasPrefix(referenceDigest(subject.GetReference()), "sha256:") {
		return nil
	}
	return fmt.Errorf("%s is not pinned to a sha256 digest: evidence for a floating tag is not coverage of the image that was built", subjectLabel(subject))
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
