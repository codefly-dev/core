package session_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/toolbox/conformance"
	"github.com/codefly-dev/core/toolbox/session"
)

// approvableDecider holds the effect tool for an approver rather than
// refusing it.
type approvableDecider struct{}

func (approvableDecider) Evaluate(_ context.Context, request *policy.PDPRequest) policy.PDPDecision {
	if request.Tool != conformance.EffectIncrementTool {
		return policy.PDPDecision{Allow: true, Reason: "fixture read grant"}
	}
	return policy.PDPDecision{
		Allow:           false,
		RequireApproval: true,
		Reason:          "effects need an approver",
		Approval: &basev0.ApprovalRequiredV1{
			RequestId:      "ar-1",
			RequestedScope: &basev0.WorkScopeV1{ResourceKind: "counter", Actions: []string{"increment"}, ResourceIds: []string{"fixture"}},
			Subject:        "counter:fixture",
			RequestDigest:  "sha256:fixture",
			Audience:       "codefly.dev/conformance",
		},
	}
}

// An approvable call is neither denied nor broken. Classifying it as
// transport hid the approval request from the caller entirely and recorded a
// policy hold in the audit trail as a network failure, with retry advice that
// said the call could never succeed.
func TestSessionSurfacesApprovalRequiredRatherThanATransportFault(t *testing.T) {
	audit := &auditRecorder{}
	opened := openFixture(t, approvableDecider{}, audit)

	_, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectIncrementTool})

	var callErr *session.CallError
	require.ErrorAs(t, err, &callErr)
	require.Equal(t, session.ErrorApprovalRequired, callErr.Code)
	require.NotEqual(t, session.ErrorTransport, callErr.Code)
	require.NotEqual(t, session.ErrorPolicyDenied, callErr.Code)
	require.Equal(t, session.RetryReconcile, callErr.Retry,
		"the call succeeds once an approver decides, so retry is not never")

	// The caller can still reach the approval request through the error.
	signal, isApproval := policy.ApprovalRequiredFrom(callErr.Err)
	require.True(t, isApproval)
	require.Equal(t, "ar-1", signal.GetRequestId())

	// The audit trail says the call is held for approval, not denied.
	var phases []session.AuditPhase
	for _, event := range audit.snapshot() {
		phases = append(phases, event.Phase)
	}
	require.Contains(t, phases, session.AuditApproval)
	require.NotContains(t, phases, session.AuditDeny)

	// And the effect really did not happen.
	count, err := opened.Call(context.Background(), session.CallRequest{Name: conformance.EffectCountTool})
	require.NoError(t, err)
	require.Equal(t, float64(0), count.Response.Content[0].GetStructured().AsMap()["count"])
}
