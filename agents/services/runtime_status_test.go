package services

import (
	"strings"
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func TestValidateRuntimeResponses(t *testing.T) {
	if err := ValidateRuntimeLoadResponse(&runtimev0.LoadResponse{Status: &runtimev0.LoadStatus{State: runtimev0.LoadStatus_READY}}); err != nil {
		t.Fatalf("ready load rejected: %v", err)
	}
	if err := ValidateRuntimeInitResponse(&runtimev0.InitResponse{Status: &runtimev0.InitStatus{State: runtimev0.InitStatus_READY}}); err != nil {
		t.Fatalf("ready init rejected: %v", err)
	}
	if err := ValidateRuntimeStartResponse(&runtimev0.StartResponse{Status: &runtimev0.StartStatus{State: runtimev0.StartStatus_STARTED}}); err != nil {
		t.Fatalf("started response rejected: %v", err)
	}
	if err := ValidateRuntimeDestroyResponse(&runtimev0.DestroyResponse{Status: &runtimev0.DestroyStatus{State: runtimev0.DestroyStatus_SUCCESS}}); err != nil {
		t.Fatalf("successful destroy rejected: %v", err)
	}

	err := ValidateRuntimeInitResponse(&runtimev0.InitResponse{Status: &runtimev0.InitStatus{
		State: runtimev0.InitStatus_ERROR, Message: "migration failed",
	}})
	if err == nil || !strings.Contains(err.Error(), "migration failed") {
		t.Fatalf("init diagnostic was not preserved: %v", err)
	}
	if err := ValidateRuntimeInitResponse(nil); err == nil || !strings.Contains(err.Error(), "nil response") {
		t.Fatalf("nil response was not rejected clearly: %v", err)
	}
}
