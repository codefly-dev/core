package policy

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// approval.go — the approval-required protocol.
//
// Core owns the *signal* and the shape of the capability that answers it; it
// does not own the approvals engine. When policy says a call is approvable
// rather than refused, the gateway returns this error instead of a deny. The
// caller puts the request to an approver, and on approval the issuer mints a
// grant capability (workcontext.Authority.Grant) that carries exactly the
// authority the signal named. Quorum, storage, notification and UI are
// product-level.
//
// On the wire the signal is a FailedPrecondition status carrying an
// ApprovalRequiredV1 detail, so a caller across a gRPC boundary recovers the
// same information ApprovalRequiredFrom reads in-process.

// ErrApprovalRequired is the umbrella an approvable call matches. It is not
// ErrGatewayDeny: a denial ends the call, while this one says the call can
// proceed once an approver decides.
var ErrApprovalRequired = errors.New("policy: approval required")

// ApprovalRequiredError carries the typed signal. It is returned in place of
// a deny, never alongside one.
type ApprovalRequiredError struct {
	// Detail names the approval request and exactly the authority a grant
	// would have to carry: the same tuple a WorkGrantHopV1 pins.
	Detail *basev0.ApprovalRequiredV1
}

// Error implements error.
func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("%s: request %s on %s", ErrApprovalRequired, e.Detail.GetRequestId(), e.Detail.GetSubject())
}

// Is makes errors.Is(err, ErrApprovalRequired) match, so callers branch on the
// umbrella without type-asserting.
func (e *ApprovalRequiredError) Is(target error) bool {
	return target == ErrApprovalRequired
}

// GRPCStatus renders the signal as the status a caller sees across a
// transport: FailedPrecondition, because the call is well-formed and
// authorized in principle but a precondition — the approval — is unmet.
func (e *ApprovalRequiredError) GRPCStatus() *status.Status {
	st := status.New(codes.FailedPrecondition, e.Error())
	withDetail, err := st.WithDetails(e.Detail)
	if err != nil {
		return st
	}
	return withDetail
}

// ApprovalRequiredFrom recovers the signal from an error, whether it was
// returned in-process or came back as a gRPC status from another service.
func ApprovalRequiredFrom(err error) (*basev0.ApprovalRequiredV1, bool) {
	var signal *ApprovalRequiredError
	if errors.As(err, &signal) {
		return signal.Detail, true
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		return nil, false
	}
	for _, detail := range st.Details() {
		if required, isApproval := detail.(*basev0.ApprovalRequiredV1); isApproval {
			return required, true
		}
	}
	return nil, false
}
