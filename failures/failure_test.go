package failures

import (
	"errors"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCRoundTripPreservesUniversalFailure(t *testing.T) {
	failure := New(basev0.FailureCode_FAILURE_CODE_TOOLCHAIN_UNAVAILABLE, "compile", "Go toolchain unavailable")
	err := GRPC(failure)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("transport code = %s, want unavailable", status.Code(err))
	}
	extracted, ok := Extract(err)
	if !ok || extracted.GetCode() != failure.GetCode() || !extracted.GetRetryable() || extracted.GetOperation() != "compile" {
		t.Fatalf("extracted failure = %v", extracted)
	}
}

func TestPolicyFailuresAreStableNonRetryablePreconditions(t *testing.T) {
	failure := New(basev0.FailureCode_FAILURE_CODE_SECURITY_POLICY_FAILED, "audit", "release policy rejected findings")
	if failure.GetTransportCode().String() != "FAILED_PRECONDITION" || failure.GetRetryable() {
		t.Fatalf("failure = %v", failure)
	}
}

func TestWrapPreservesClassificationAndCause(t *testing.T) {
	cause := errors.New("scanner missing")
	err := Wrap(basev0.FailureCode_FAILURE_CODE_TOOLCHAIN_UNAVAILABLE, "builder.audit", "audit tool unavailable", cause)
	if !errors.Is(err, cause) {
		t.Fatal("wrapped failure did not preserve cause")
	}
	failure := FromError("ignored", err)
	if failure.GetCode() != basev0.FailureCode_FAILURE_CODE_TOOLCHAIN_UNAVAILABLE || failure.GetOperation() != "builder.audit" || !failure.GetRetryable() {
		t.Fatalf("unexpected wrapped failure: %+v", failure)
	}
	failure.Message = "mutated"
	again := FromError("ignored", err)
	if again.GetMessage() != "audit tool unavailable" {
		t.Fatalf("FromError returned shared mutable detail: %q", again.GetMessage())
	}
}

func TestEnsureAndForOutcomePreserveFailureWithoutSharingIt(t *testing.T) {
	original := New(basev0.FailureCode_FAILURE_CODE_NOT_FOUND, "code.fix", "source file missing")
	forwarded := Ensure(original, basev0.FailureCode_FAILURE_CODE_INTERNAL, "tooling.fix", "fallback")
	if forwarded.GetCode() != original.GetCode() || forwarded.GetOperation() != original.GetOperation() {
		t.Fatalf("forwarded failure = %+v, want original classification", forwarded)
	}
	forwarded.Message = "mutated"
	if original.GetMessage() != "source file missing" {
		t.Fatalf("Ensure shared mutable failure: %+v", original)
	}
	if got := ForOutcome(true, original, basev0.FailureCode_FAILURE_CODE_INTERNAL, "tooling.fix", "fallback"); got != nil {
		t.Fatalf("successful outcome returned failure: %+v", got)
	}
	fallback := ForOutcome(false, nil, basev0.FailureCode_FAILURE_CODE_INTERNAL, "tooling.fix", "missing result")
	if fallback.GetCode() != basev0.FailureCode_FAILURE_CODE_INTERNAL || fallback.GetOperation() != "tooling.fix" {
		t.Fatalf("fallback failure = %+v", fallback)
	}
}
