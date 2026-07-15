package services

import (
	"errors"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func TestRuntimeErrorHelperReturnsStructuredResponseWithoutTransportError(t *testing.T) {
	wrapper := &RuntimeWrapper{}
	response, err := wrapper.InitError(errors.New("compiler unavailable"))
	if err != nil {
		t.Fatalf("InitError returned transport error: %v", err)
	}
	if response.GetStatus().GetState() != runtimev0.InitStatus_ERROR || response.GetStatus().GetMessage() != "compiler unavailable" {
		t.Fatalf("InitError response = %+v", response)
	}
}

func TestBuilderErrorHelperReturnsStructuredResponseWithoutTransportError(t *testing.T) {
	wrapper := &BuilderWrapper{}
	response, err := wrapper.BuildError(errors.New("image build failed"))
	if err != nil {
		t.Fatalf("BuildError returned transport error: %v", err)
	}
	if response.GetState().GetState() != builderv0.BuildStatus_ERROR || response.GetState().GetMessage() != "image build failed" {
		t.Fatalf("BuildError response = %+v", response)
	}
}
