package services

import (
	"context"
	"errors"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func TestStartGenerationSeparatesLifecyclesAndDropsStaleHealth(t *testing.T) {
	wrapper := &RuntimeWrapper{}

	first, _ := wrapper.StartResponse()
	if first.GetStatus().GetGeneration() != 1 {
		t.Fatalf("first start generation = %d, want 1", first.GetStatus().GetGeneration())
	}

	wrapper.ReportHealth(&basev0.HealthReport{Ready: true})
	information, err := wrapper.InformationResponse(context.Background(), &runtimev0.InformationRequest{})
	if err != nil {
		t.Fatalf("InformationResponse: %v", err)
	}
	if !information.GetHealth().GetReady() || information.GetHealth().GetGeneration() != 1 {
		t.Fatalf("health = %+v, want ready at generation 1", information.GetHealth())
	}

	// A restart invalidates the ready verdict the previous process earned: the
	// generation moves and the stale report is gone rather than carried over.
	second, _ := wrapper.StartResponse()
	if second.GetStatus().GetGeneration() != 2 {
		t.Fatalf("second start generation = %d, want 2", second.GetStatus().GetGeneration())
	}
	information, _ = wrapper.InformationResponse(context.Background(), &runtimev0.InformationRequest{})
	if information.GetHealth() != nil {
		t.Fatalf("health survived a restart: %+v", information.GetHealth())
	}

	// A death after Start keeps the generation so a consumer can tell which
	// process failed, while the state flips to ERROR.
	wrapper.MarkRunnerExited(errors.New("runner exited"))
	information, _ = wrapper.InformationResponse(context.Background(), &runtimev0.InformationRequest{})
	if information.GetStartStatus().GetState() != runtimev0.StartStatus_ERROR ||
		information.GetStartStatus().GetGeneration() != 2 {
		t.Fatalf("start status = %+v, want ERROR at generation 2", information.GetStartStatus())
	}
}
