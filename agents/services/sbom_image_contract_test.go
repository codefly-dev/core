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
	resp, err := wrapper.SBOMImages(context.Background(), nil, servicesbom.SourceRegistry)
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

	resp, err := wrapper.SBOMImages(context.Background(), subjects, servicesbom.SourceRegistry)
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
