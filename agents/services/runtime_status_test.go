package services

import (
	"strings"
	"testing"

	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
)

func TestValidateRuntimeResponses(t *testing.T) {
	if err := ValidateRuntimeLoadResponse(&runtimev1.LoadResponse{Status: &runtimev1.LoadStatus{State: runtimev1.LoadStatus_READY}}); err != nil {
		t.Fatalf("ready load rejected: %v", err)
	}
	if err := ValidateRuntimeInitResponse(&runtimev1.InitResponse{Status: &runtimev1.InitStatus{State: runtimev1.InitStatus_READY}}); err != nil {
		t.Fatalf("ready init rejected: %v", err)
	}
	if err := ValidateRuntimeStartResponse(&runtimev1.StartResponse{Status: &runtimev1.StartStatus{State: runtimev1.StartStatus_STARTED}}); err != nil {
		t.Fatalf("started response rejected: %v", err)
	}
	if err := ValidateRuntimeDestroyResponse(&runtimev1.DestroyResponse{Status: &runtimev1.DestroyStatus{State: runtimev1.DestroyStatus_SUCCESS}}); err != nil {
		t.Fatalf("successful destroy rejected: %v", err)
	}

	err := ValidateRuntimeInitResponse(&runtimev1.InitResponse{Status: &runtimev1.InitStatus{
		State: runtimev1.InitStatus_ERROR, Message: "migration failed",
	}})
	if err == nil || !strings.Contains(err.Error(), "migration failed") {
		t.Fatalf("init diagnostic was not preserved: %v", err)
	}
	if err := ValidateRuntimeInitResponse(nil); err == nil || !strings.Contains(err.Error(), "nil response") {
		t.Fatalf("nil response was not rejected clearly: %v", err)
	}
}
