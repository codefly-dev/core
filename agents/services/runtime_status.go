package services

import (
	"fmt"
	"strings"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// ValidateRuntimeLoadResponse converts the runtime wire status into the Go
// error contract expected by callers that invoke plugin implementations
// directly (not through the host-side service wrapper).
func ValidateRuntimeLoadResponse(resp *runtimev0.LoadResponse) error {
	if resp != nil && resp.GetStatus().GetState() == runtimev0.LoadStatus_READY {
		return nil
	}
	return runtimeResponseError("load", runtimeLoadMessage(resp))
}

// ValidateRuntimeInitResponse converts an init ERROR/empty response into an
// ordinary error. A nil gRPC transport error does not imply operation success;
// runtime plugins report domain failures in the structured status.
func ValidateRuntimeInitResponse(resp *runtimev0.InitResponse) error {
	if resp != nil && resp.GetStatus().GetState() == runtimev0.InitStatus_READY {
		return nil
	}
	return runtimeResponseError("init", runtimeInitMessage(resp))
}

// ValidateRuntimeStartResponse converts the structured start status into an
// ordinary error for direct callers and tests.
func ValidateRuntimeStartResponse(resp *runtimev0.StartResponse) error {
	if resp != nil && resp.GetStatus().GetState() == runtimev0.StartStatus_STARTED {
		return nil
	}
	return runtimeResponseError("start", runtimeStartMessage(resp))
}

// ValidateRuntimeDestroyResponse ensures cleanup failures encoded in the wire
// status are not silently ignored by direct callers.
func ValidateRuntimeDestroyResponse(resp *runtimev0.DestroyResponse) error {
	if resp != nil && resp.GetStatus().GetState() == runtimev0.DestroyStatus_SUCCESS {
		return nil
	}
	message := "plugin returned a nil response"
	if resp != nil {
		message = resp.GetStatus().GetMessage()
	}
	return runtimeResponseError("destroy", message)
}

func runtimeResponseError(operation, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "plugin returned a non-success status"
	}
	return fmt.Errorf("runtime %s failed: %s", operation, message)
}

func runtimeLoadMessage(resp *runtimev0.LoadResponse) string {
	if resp == nil {
		return "plugin returned a nil response"
	}
	return resp.GetStatus().GetMessage()
}

func runtimeInitMessage(resp *runtimev0.InitResponse) string {
	if resp == nil {
		return "plugin returned a nil response"
	}
	return resp.GetStatus().GetMessage()
}

func runtimeStartMessage(resp *runtimev0.StartResponse) string {
	if resp == nil {
		return "plugin returned a nil response"
	}
	return resp.GetStatus().GetMessage()
}
