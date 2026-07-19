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
	if response.GetStatus().GetFailure().GetOperation() != "runtime.init" || response.GetStatus().GetFailure().GetMessage() != "compiler unavailable" {
		t.Fatalf("InitError failure = %+v", response.GetStatus().GetFailure())
	}
}

func TestRuntimeLintErrorPreservesFailureOutput(t *testing.T) {
	wrapper := &RuntimeWrapper{}
	response, err := wrapper.LintErrorf(errors.New("main.go:4:2: undefined: value"), "lint failed")
	if err != nil {
		t.Fatalf("LintErrorf returned transport error: %v", err)
	}
	if response.GetStatus().GetState() != runtimev0.LintStatus_ERROR {
		t.Fatalf("LintErrorf status = %+v", response.GetStatus())
	}
	if response.GetOutput() != "lint failed: main.go:4:2: undefined: value" {
		t.Fatalf("LintErrorf output = %q", response.GetOutput())
	}
	if response.GetStatus().GetFailure().GetOperation() != "runtime.lint" {
		t.Fatalf("LintErrorf failure = %+v", response.GetStatus().GetFailure())
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
	if response.GetState().GetFailure().GetOperation() != "builder.build" || response.GetState().GetFailure().GetMessage() != "image build failed" {
		t.Fatalf("BuildError failure = %+v", response.GetState().GetFailure())
	}
}
