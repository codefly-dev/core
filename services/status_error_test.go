package services

import "testing"

func TestOperationStatusErrorPreservesDiagnostic(t *testing.T) {
	if got := operationStatusError("runtime init", "  compiler unavailable  ").Error(); got != "runtime init failed: compiler unavailable" {
		t.Fatalf("error = %q", got)
	}
	if got := operationStatusError("builder build", " ").Error(); got != "builder build failed: agent returned an error status" {
		t.Fatalf("fallback error = %q", got)
	}
}
