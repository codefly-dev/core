package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/policy"
)

func approvalSignal() *basev0.ApprovalRequiredV1 {
	return &basev0.ApprovalRequiredV1{
		RequestId:      approvalRequestID,
		RequestedScope: workScope("repo", "merge", "codefly/core"),
		Subject:        mergeSubject,
		RequestDigest:  callDigest,
		Audience:       toolboxID,
	}
}

func TestApprovalRequiredError_IsNotADeny(t *testing.T) {
	err := error(&policy.ApprovalRequiredError{Detail: approvalSignal()})

	require.ErrorIs(t, err, policy.ErrApprovalRequired)
	require.NotErrorIs(t, err, policy.ErrGatewayDeny)
	require.Contains(t, err.Error(), approvalRequestID)
}

// A caller on the far side of a transport must recover the same signal, so the
// protocol is the status and its detail rather than a Go type.
func TestApprovalRequiredFrom_SurvivesAGRPCRoundTrip(t *testing.T) {
	signal := &policy.ApprovalRequiredError{Detail: approvalSignal()}
	st := status.Convert(signal)
	require.Equal(t, codes.FailedPrecondition, st.Code())

	recovered, isApproval := policy.ApprovalRequiredFrom(st.Err())
	require.True(t, isApproval)
	require.Equal(t, approvalRequestID, recovered.GetRequestId())
	require.Equal(t, mergeSubject, recovered.GetSubject())
	require.Equal(t, "merge", recovered.GetRequestedScope().GetActions()[0])
}

func TestApprovalRequiredFrom_IgnoresOtherErrors(t *testing.T) {
	for name, err := range map[string]error{
		"a plain error":       errors.New("boom"),
		"a gateway deny":      policy.ErrGatewayDeny,
		"another status":      status.Error(codes.PermissionDenied, "no"),
		"a bare precondition": status.Error(codes.FailedPrecondition, "something else"),
	} {
		t.Run(name, func(t *testing.T) {
			_, isApproval := policy.ApprovalRequiredFrom(err)
			require.False(t, isApproval)
		})
	}
}

type staticPDP struct{ decision policy.PDPDecision }

func (p staticPDP) Evaluate(context.Context, *policy.PDPRequest) policy.PDPDecision {
	return p.decision
}

func approvableGateway(decision policy.PDPDecision) *policy.GatewayEvaluator {
	return &policy.GatewayEvaluator{
		Decider:    staticPDP{decision: decision},
		Secret:     []byte("0123456789abcdef0123456789abcdef"),
		DefaultTTL: time.Minute,
	}
}

func TestGateway_ApprovableReturnsTheSignalRatherThanADeny(t *testing.T) {
	gateway := approvableGateway(policy.PDPDecision{
		RequireApproval: true,
		Reason:          "merge needs an approver",
		Approval:        approvalSignal(),
	})

	_, err := gateway.EvaluateAndMint(context.Background(), policy.EvaluationInput{
		Principal: &policy.Principal{ID: "a-mind", Kind: policy.KindAgent, OrgID: "t-acme", AgentID: "codefly.dev/mind:1.2.0"},
		Toolbox:   toolboxID,
		Tool:      mergeToolName,
		Resource:  mergeSubject,
	})

	require.ErrorIs(t, err, policy.ErrApprovalRequired)
	require.NotErrorIs(t, err, policy.ErrGatewayDeny)
	signal, isApproval := policy.ApprovalRequiredFrom(err)
	require.True(t, isApproval)
	require.Equal(t, approvalRequestID, signal.GetRequestId())
}

// A PDP that says "approvable" without saying what would be approved has told
// nobody what to decide. That is a misconfiguration, not a policy outcome, so
// it must not be reported as either an approval or a deny.
func TestGateway_ApprovableWithoutASignalIsAMisconfiguration(t *testing.T) {
	gateway := approvableGateway(policy.PDPDecision{RequireApproval: true, Reason: "merge needs an approver"})

	_, err := gateway.EvaluateAndMint(context.Background(), policy.EvaluationInput{
		Principal: &policy.Principal{ID: "a-mind", Kind: policy.KindAgent, OrgID: "t-acme", AgentID: "codefly.dev/mind:1.2.0"},
		Toolbox:   toolboxID,
		Tool:      mergeToolName,
	})

	require.Error(t, err)
	require.NotErrorIs(t, err, policy.ErrApprovalRequired)
	require.NotErrorIs(t, err, policy.ErrGatewayDeny)
	require.Contains(t, err.Error(), "no approval_required signal")
}
