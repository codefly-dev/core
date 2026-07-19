// Package failures provides the shared structured error boundary used by
// Codefly plugins, hosts, Mind/editor integrations, and automation.
package failures

import (
	"context"
	"errors"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codepb "google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Error carries a typed Failure through ordinary Go error chains. Plugin code
// can classify a native/toolchain/domain error once; response helpers and gRPC
// boundaries recover the same generated protobuf without parsing strings.
type Error struct {
	failure *basev0.Failure
	cause   error
	message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	if e.failure == nil {
		return ""
	}
	return e.failure.GetMessage()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// CodeflyFailure exposes the generated detail to FromError.
func (e *Error) CodeflyFailure() *basev0.Failure {
	if e == nil {
		return nil
	}
	return e.failure
}

// New creates a universal Failure with transport and retry semantics derived
// from its stable Codefly code.
func New(code basev0.FailureCode, operation, message string) *basev0.Failure {
	transport, retryable := defaults(code)
	return &basev0.Failure{
		Code:          code,
		Message:       strings.TrimSpace(message),
		TransportCode: codepb.Code(transport),
		Retryable:     retryable,
		Operation:     strings.TrimSpace(operation),
	}
}

// Clone returns an independent copy suitable for forwarding across another
// response boundary.
func Clone(failure *basev0.Failure) *basev0.Failure {
	if failure == nil {
		return nil
	}
	return proto.Clone(failure).(*basev0.Failure)
}

// Ensure forwards an existing failure or creates the specified fallback when
// a producer violated the structured-failure contract.
func Ensure(existing *basev0.Failure, code basev0.FailureCode, operation, message string) *basev0.Failure {
	if cloned := Clone(existing); cloned != nil {
		return cloned
	}
	return New(code, operation, message)
}

// ForOutcome returns nil for success and an ensured failure for an
// unsuccessful result.
func ForOutcome(success bool, existing *basev0.Failure, code basev0.FailureCode, operation, message string) *basev0.Failure {
	if success {
		return nil
	}
	return Ensure(existing, code, operation, message)
}

// Wrap returns an ordinary Go error classified with the universal Codefly
// taxonomy. The optional cause remains available to errors.Is/errors.As.
func Wrap(code basev0.FailureCode, operation, message string, cause error) error {
	if strings.TrimSpace(message) == "" && cause != nil {
		message = cause.Error()
	}
	return &Error{failure: New(code, operation, message), cause: cause, message: message}
}

// FromFailure converts an existing generated detail into an ordinary Go error
// without dropping diagnostics, resource identity, process evidence, or
// causes. presentation is only the local Error() text; the protobuf remains
// unchanged for downstream extraction.
func FromFailure(failure *basev0.Failure, presentation string, cause error) error {
	if failure == nil {
		if cause != nil {
			return cause
		}
		return errors.New(strings.TrimSpace(presentation))
	}
	return &Error{
		failure: Clone(failure),
		cause:   cause,
		message: strings.TrimSpace(presentation),
	}
}

// FromError maps context errors and otherwise retains a safe internal failure.
func FromError(operation string, err error) *basev0.Failure {
	if err == nil {
		return nil
	}
	var carrier interface {
		CodeflyFailure() *basev0.Failure
	}
	if errors.As(err, &carrier) {
		if failure := carrier.CodeflyFailure(); failure != nil {
			cloned := Clone(failure)
			if strings.TrimSpace(cloned.GetOperation()) == "" {
				cloned.Operation = strings.TrimSpace(operation)
			}
			return cloned
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return New(basev0.FailureCode_FAILURE_CODE_CANCELLED, operation, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return New(basev0.FailureCode_FAILURE_CODE_TIMEOUT, operation, err.Error())
	default:
		return New(basev0.FailureCode_FAILURE_CODE_INTERNAL, operation, err.Error())
	}
}

// GRPC returns a google.rpc.Status-compatible gRPC error carrying Failure as a
// typed detail. Callers can use Extract without parsing the status message.
func GRPC(failure *basev0.Failure) error {
	if failure == nil {
		return nil
	}
	transport := codes.Code(failure.GetTransportCode())
	if transport == codes.OK {
		transport, _ = defaults(failure.GetCode())
	}
	message := strings.TrimSpace(failure.GetMessage())
	if message == "" {
		message = failure.GetCode().String()
	}
	detailed, err := status.New(transport, message).WithDetails(failure)
	if err != nil {
		return status.Error(transport, message)
	}
	return detailed.Err()
}

// Extract returns the first Codefly Failure packed into a gRPC status.
func Extract(err error) (*basev0.Failure, bool) {
	if err == nil {
		return nil, false
	}
	for _, detail := range status.Convert(err).Details() {
		if failure, ok := detail.(*basev0.Failure); ok {
			return failure, true
		}
	}
	return nil, false
}

func defaults(code basev0.FailureCode) (codes.Code, bool) {
	switch code {
	case basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT,
		basev0.FailureCode_FAILURE_CODE_INVALID_CONFIGURATION,
		basev0.FailureCode_FAILURE_CODE_VALIDATION_FAILED:
		return codes.InvalidArgument, false
	case basev0.FailureCode_FAILURE_CODE_NOT_FOUND:
		return codes.NotFound, false
	case basev0.FailureCode_FAILURE_CODE_ALREADY_EXISTS:
		return codes.AlreadyExists, false
	case basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION,
		basev0.FailureCode_FAILURE_CODE_UNIMPLEMENTED:
		return codes.Unimplemented, false
	case basev0.FailureCode_FAILURE_CODE_UNAUTHENTICATED:
		return codes.Unauthenticated, false
	case basev0.FailureCode_FAILURE_CODE_PERMISSION_DENIED:
		return codes.PermissionDenied, false
	case basev0.FailureCode_FAILURE_CODE_CONFLICT,
		basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED,
		basev0.FailureCode_FAILURE_CODE_INTEGRITY_FAILED,
		basev0.FailureCode_FAILURE_CODE_GENERATED_DRIFT,
		basev0.FailureCode_FAILURE_CODE_COMPATIBILITY_FAILED,
		basev0.FailureCode_FAILURE_CODE_SECURITY_POLICY_FAILED:
		return codes.FailedPrecondition, false
	case basev0.FailureCode_FAILURE_CODE_DEPENDENCY_FAILED,
		basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED:
		return codes.Aborted, false
	case basev0.FailureCode_FAILURE_CODE_DEPENDENCY_UNAVAILABLE,
		basev0.FailureCode_FAILURE_CODE_TOOLCHAIN_UNAVAILABLE,
		basev0.FailureCode_FAILURE_CODE_RUNTIME_UNAVAILABLE,
		basev0.FailureCode_FAILURE_CODE_NETWORK_FAILED,
		basev0.FailureCode_FAILURE_CODE_TEMPORARILY_UNAVAILABLE:
		return codes.Unavailable, true
	case basev0.FailureCode_FAILURE_CODE_TIMEOUT:
		return codes.DeadlineExceeded, true
	case basev0.FailureCode_FAILURE_CODE_CANCELLED:
		return codes.Canceled, false
	case basev0.FailureCode_FAILURE_CODE_RATE_LIMITED,
		basev0.FailureCode_FAILURE_CODE_RESOURCE_EXHAUSTED:
		return codes.ResourceExhausted, true
	case basev0.FailureCode_FAILURE_CODE_IO_FAILED:
		return codes.DataLoss, false
	default:
		return codes.Internal, false
	}
}
