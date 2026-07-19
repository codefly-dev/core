package services

import (
	"testing"

	"github.com/codefly-dev/core/failures"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func TestOperationStatusErrorPreservesDiagnostic(t *testing.T) {
	if got := operationStatusError("runtime init", "  compiler unavailable  ").Error(); got != "runtime init failed: compiler unavailable" {
		t.Fatalf("error = %q", got)
	}
	if got := operationStatusError("builder build", " ").Error(); got != "builder build failed: agent returned an error status" {
		t.Fatalf("fallback error = %q", got)
	}
}

func TestOperationStatusFailurePreservesTypedDetail(t *testing.T) {
	failure := failures.New(basev0.FailureCode_FAILURE_CODE_TOOLCHAIN_UNAVAILABLE, "runtime.compile", "compiler unavailable")
	err := operationStatusFailure("runtime build", failure.GetMessage(), failure)
	if err.Error() != "runtime build failed: compiler unavailable" {
		t.Fatalf("error = %q", err)
	}
	extracted := failures.FromError("ignored", err)
	if extracted.GetCode() != failure.GetCode() || extracted.GetOperation() != failure.GetOperation() || !extracted.GetRetryable() {
		t.Fatalf("typed failure = %+v", extracted)
	}
}
