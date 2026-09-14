package services

import (
	"context"
	"fmt"
	"testing"

	servicesbom "github.com/codefly-dev/core/agents/services/sbom"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

// The conformance check is driven with responses the wrapper actually produces,
// not hand-built literals. Validating a literal hid a defect where every real
// failure was reported as a scope problem and its cause never surfaced.
func TestImageSBOMFailuresNameTheirCauseThroughValidateCoverage(t *testing.T) {
	wrapper := &BuilderWrapper{}
	subjects := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}

	failed, err := wrapper.SBOMImageError(fmt.Errorf("syft exited 1: no space left on device"))
	require.NoError(t, err)
	require.Equal(t, builderv0.SBOMScope_SBOM_SCOPE_IMAGE, failed.GetScope())
	validation := servicesbom.ValidateCoverage("svc", subjects, failed)
	require.ErrorContains(t, validation, "no space left on device")
	require.NotContains(t, validation.Error(), "not image coverage")

	unsupported, err := wrapper.SBOMUnsupported("no generator here")
	require.NoError(t, err)
	require.ErrorContains(t, servicesbom.ValidateCoverage("svc", subjects, unsupported), "no generator here")

	required, err := wrapper.SBOMImageSubjectsRequired()
	require.NoError(t, err)
	require.ErrorContains(t, servicesbom.ValidateCoverage("svc", subjects, required), "does not build its own images")
}

func TestNoImageResponseRequiresAnExplicitReason(t *testing.T) {
	wrapper := &BuilderWrapper{}

	invalid, err := wrapper.SBOMNoImage(builderv0.NoImageReason_NO_IMAGE_REASON_UNSPECIFIED, "")
	require.NoError(t, err)
	require.Equal(t, builderv0.SBOMStatus_ERROR, invalid.GetState().GetState())
	require.Equal(t, builderv0.SBOMScope_SBOM_SCOPE_IMAGE, invalid.GetScope())
	require.ErrorContains(t, servicesbom.ValidateCoverage("svc", nil, invalid), "must declare a no-image reason")

	declared, err := wrapper.SBOMNoImage(builderv0.NoImageReason_NO_IMAGE_REASON_NO_IMAGE, "passive toolbox")
	require.NoError(t, err)
	require.NoError(t, servicesbom.ValidateCoverage("svc", nil, declared))
}

// A stock-image service that pins a vendor image ships that image, so the
// escape hatch is refused unless it names the external runtime instead.
func TestExternallyManagedMustNameTheRuntime(t *testing.T) {
	wrapper := &BuilderWrapper{}

	anonymous, err := wrapper.SBOMNoImage(builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED, "")
	require.NoError(t, err)
	require.Equal(t, builderv0.SBOMStatus_ERROR, anonymous.GetState().GetState())
	require.ErrorContains(t, servicesbom.ValidateCoverage("svc", nil, anonymous), "must name the runtime")

	named, err := wrapper.SBOMNoImage(builderv0.NoImageReason_NO_IMAGE_REASON_EXTERNALLY_MANAGED, "runs on a provider-managed RDS instance")
	require.NoError(t, err)
	require.NoError(t, servicesbom.ValidateCoverage("svc", nil, named))
}

// An agent whose images the caller builds holds no digest, so it must fail the
// precondition rather than report coverage or claim to be unimplemented.
func TestSBOMImagesWithoutSubjectsIsAPreconditionFailure(t *testing.T) {
	wrapper := &BuilderWrapper{}
	resp, err := wrapper.SBOMImages(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, builderv0.SBOMStatus_ERROR, resp.GetState().GetState())
	require.Equal(t, builderv0.SBOMScope_SBOM_SCOPE_IMAGE, resp.GetScope())
	require.Equal(t, basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, resp.GetState().GetFailure().GetCode())
}

// A subject naming only a tag is refused before anything is scanned: the
// registry may still serve an image this build never produced.
func TestSBOMImagesRefusesAnUnpinnedSubject(t *testing.T) {
	wrapper := &BuilderWrapper{}
	subjects := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}

	resp, err := wrapper.SBOMImages(context.Background(), subjects)
	require.NoError(t, err)
	require.Equal(t, builderv0.SBOMStatus_ERROR, resp.GetState().GetState())
	require.Equal(t, builderv0.SBOMScope_SBOM_SCOPE_IMAGE, resp.GetScope())
	require.Contains(t, resp.GetState().GetMessage(), "not pinned to a sha256 digest")
}

// A source inventory is still rejected as image coverage; state is checked
// first, but a successful source response must not pass as image evidence.
func TestSourceInventoryIsStillRejectedAsImageCoverage(t *testing.T) {
	wrapper := &BuilderWrapper{}
	subjects := []*builderv0.ImageSubject{{Reference: "ghcr.io/codefly-dev/app:1.0", Role: "app", Service: "svc"}}

	source, err := wrapper.SBOMResponse(nil, "go-list", "GO", "abc")
	require.NoError(t, err)
	require.ErrorContains(t, servicesbom.ValidateCoverage("svc", subjects, source), "not image coverage")
}

// Nothing in a reference or a digest says whether an image was pushed or only
// loaded, so the subject carries the answer and one request may mix the two.
// Reading the selector per subject is what makes the same two subjects fail
// differently depending on which one is reached first.
func TestSBOMImagesReachesEachSubjectWhereItSaysItLives(t *testing.T) {
	wrapper := &BuilderWrapper{}
	digest := "sha256:2a1f0c8d4e6b7a9c3d5e1f0a2b4c6d8e0f1a3b5c7d9e1f0a2b4c6d8e0f1a3b5c"
	pushed := &builderv0.ImageSubject{
		Reference: "localhost:1/codefly/app@" + digest,
		Platform:  "linux/amd64",
		Role:      "app",
		Service:   "svc",
		Source:    builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_REGISTRY,
	}
	loaded := &builderv0.ImageSubject{
		Reference: "localhost:1/codefly/migration:1.0",
		Digest:    digest,
		Role:      "migration",
		Service:   "svc",
		Source:    builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_DOCKER_DAEMON,
	}

	daemonFirst, err := wrapper.SBOMImages(context.Background(), []*builderv0.ImageSubject{loaded, pushed})
	require.NoError(t, err)
	require.Contains(t, daemonFirst.GetState().GetMessage(), "resolve local image localhost:1/codefly/migration:1.0")

	registryFirst, err := wrapper.SBOMImages(context.Background(), []*builderv0.ImageSubject{pushed, loaded})
	require.NoError(t, err)
	require.Contains(t, registryFirst.GetState().GetMessage(), "registry")
	require.NotContains(t, registryFirst.GetState().GetMessage(), "resolve local image")
}

// A local subject pinned only in its reference is refused before anything is
// scanned: the daemon would resolve that reference to its own image ID and bind
// evidence to it, leaving no field holding the identity that was requested.
func TestSBOMImagesRefusesALocalSubjectPinnedOnlyInItsReference(t *testing.T) {
	wrapper := &BuilderWrapper{}
	subjects := []*builderv0.ImageSubject{{
		Reference: "localhost:1/codefly/app@sha256:2a1f0c8d4e6b7a9c3d5e1f0a2b4c6d8e0f1a3b5c7d9e1f0a2b4c6d8e0f1a3b5c",
		Role:      "app",
		Service:   "svc",
		Source:    builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_DOCKER_DAEMON,
	}}

	resp, err := wrapper.SBOMImages(context.Background(), subjects)
	require.NoError(t, err)
	require.Equal(t, builderv0.SBOMStatus_ERROR, resp.GetState().GetState())
	require.Equal(t, builderv0.SBOMScope_SBOM_SCOPE_IMAGE, resp.GetScope())
	require.Contains(t, resp.GetState().GetMessage(), "pins no sha256 digest field")
}
