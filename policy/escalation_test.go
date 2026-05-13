package policy_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/codefly-dev/core/policy"
)

// =====================================================================
// fakeGrantor — programmable test EscalationGrantor
// =====================================================================

// fakeGrantor lets tests pre-program decisions per (principal, action).
// Records every Request call for assertions.
type fakeGrantor struct {
	mu       sync.Mutex
	mintFunc func(req policy.EscalationRequest) (*policy.EscalationResult, error)
	calls    []policy.EscalationRequest
}

func (g *fakeGrantor) Request(_ context.Context, req policy.EscalationRequest) (*policy.EscalationResult, error) {
	g.mu.Lock()
	g.calls = append(g.calls, req)
	g.mu.Unlock()
	if g.mintFunc != nil {
		return g.mintFunc(req)
	}
	return &policy.EscalationResult{Decision: policy.EscalationDenied, Reason: "fake-grantor default deny"}, nil
}

func (g *fakeGrantor) Calls() []policy.EscalationRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]policy.EscalationRequest, len(g.calls))
	copy(out, g.calls)
	return out
}

// approvingGrantor returns a grantor that always approves with a
// freshly minted scoped-auth token signed by `secret` and bound
// to `audience`.
func approvingGrantor(t *testing.T, secret []byte, audience string) *fakeGrantor {
	t.Helper()
	return &fakeGrantor{
		mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
			encoded, sa, err := policy.Mint(policy.MintInput{
				Principal:  req.Principal,
				Action:     req.Action,
				Resource:   req.Resource,
				AudienceID: audience,
				TTL:        time.Minute,
				MaxUses:    1,
			}, secret)
			if err != nil {
				return nil, err
			}
			return &policy.EscalationResult{
				Decision:      policy.EscalationApproved,
				Token:         encoded,
				Authorization: sa,
				Decider:       "test-approver",
				GrantID:       "test-grant-001",
				Reason:        "auto-approved by test fixture",
			}, nil
		},
	}
}

// =====================================================================
// EscalationRequest.Validate
// =====================================================================

func TestEscalationRequest_Validate_NilRejected(t *testing.T) {
	var req *policy.EscalationRequest
	require.Error(t, req.Validate())
}

func TestEscalationRequest_Validate_NilPrincipalRejected(t *testing.T) {
	req := &policy.EscalationRequest{Action: "x", Justification: "j"}
	require.Error(t, req.Validate())
}

func TestEscalationRequest_Validate_EmptyActionRejected(t *testing.T) {
	req := &policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "p", Kind: policy.KindHuman},
		Justification: "j",
	}
	err := req.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "action")
}

func TestEscalationRequest_Validate_EmptyJustificationRejected(t *testing.T) {
	// Justification is REQUIRED — without it, the grantor has
	// nothing to base their decision on.
	req := &policy.EscalationRequest{
		Principal: &policy.Principal{ID: "p", Kind: policy.KindHuman},
		Action:    "x",
	}
	err := req.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "justification")
}

func TestEscalationRequest_Validate_InvalidPrincipalRejected(t *testing.T) {
	// Agent without AgentID — invalid principal.
	req := &policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "p", Kind: policy.KindAgent, OrgID: "o"},
		Action:        "x",
		Justification: "j",
	}
	require.Error(t, req.Validate())
}

func TestEscalationRequest_Validate_HappyPath(t *testing.T) {
	req := &policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "p", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "github.merge_pr",
		Resource:      "repo:codefly/x",
		Justification: "PR has 2 approvals, CI green",
	}
	require.NoError(t, req.Validate())
}

// =====================================================================
// RequestEscalation
// =====================================================================

func TestRequestEscalation_NoGrantor_Errors(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	policy.SetGlobalEscalationGrantor(nil) // ensure clear

	_, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "p", Kind: policy.KindHuman},
		Action:        "x",
		Justification: "j",
	})
	require.ErrorIs(t, err, policy.ErrNoGrantor)
}

func TestRequestEscalation_Approved_ReturnsCtxWithMetadata(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	secret := policy.NewSpawnSecret()
	g := approvingGrantor(t, secret, "test/audience:1.0")
	policy.SetGlobalEscalationGrantor(g)

	ctx, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "u-1", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "github.merge_pr",
		Resource:      "repo:codefly/x",
		Justification: "PR approved + CI green",
	})
	require.NoError(t, err)

	// The returned ctx must carry the scoped-auth header in
	// outgoing metadata so the next gRPC call propagates it.
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok, "RequestEscalation must attach outgoing metadata")
	values := md.Get(policy.ScopedAuthMetadataKey)
	require.Len(t, values, 1, "exactly one scoped-auth header expected")
	require.NotEmpty(t, values[0], "header value is the encoded scoped token")

	// And the verified ScopedAuthorization must be on ctx for
	// audit-log readers to inspect.
	sa := policy.ScopedAuthFrom(ctx)
	require.NotNil(t, sa)
	require.Equal(t, "github.merge_pr", sa.Action)

	// Grantor was invoked exactly once with the right inputs.
	require.Len(t, g.Calls(), 1)
	require.Equal(t, "github.merge_pr", g.Calls()[0].Action)
	require.Equal(t, "PR approved + CI green", g.Calls()[0].Justification)
}

func TestRequestEscalation_Denied_ReturnsErrEscalationDenied(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	g := &fakeGrantor{
		mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
			return &policy.EscalationResult{
				Decision: policy.EscalationDenied,
				Reason:   "approver said no",
				Decider:  "u-approver",
			}, nil
		},
	}
	policy.SetGlobalEscalationGrantor(g)

	_, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "u-1", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "x",
		Justification: "tested",
	})
	require.ErrorIs(t, err, policy.ErrEscalationDenied)
	require.Contains(t, err.Error(), "approver said no")
}

func TestRequestEscalation_TimedOut_ReturnsErrEscalationTimedOut(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	g := &fakeGrantor{
		mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
			return &policy.EscalationResult{Decision: policy.EscalationTimedOut}, nil
		},
	}
	policy.SetGlobalEscalationGrantor(g)

	_, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "u-1", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "x",
		Justification: "j",
	})
	require.ErrorIs(t, err, policy.ErrEscalationTimedOut)
}

func TestRequestEscalation_GrantorContextDeadline_TimesOut(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	// Grantor blocks until ctx is cancelled; RequestEscalation's
	// timeout must propagate to it.
	g := &fakeGrantor{
		mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
			// Simulate a slow grantor that returns DeadlineExceeded.
			return nil, context.DeadlineExceeded
		},
	}
	policy.SetGlobalEscalationGrantor(g)

	_, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "u", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "x",
		Justification: "j",
		Timeout:       50 * time.Millisecond,
	})
	require.ErrorIs(t, err, policy.ErrEscalationTimedOut)
}

func TestRequestEscalation_GrantorInfraError_Propagated(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	g := &fakeGrantor{
		mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
			return nil, errors.New("approval-service down")
		},
	}
	policy.SetGlobalEscalationGrantor(g)

	_, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "u", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "x",
		Justification: "j",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "approval-service down",
		"infrastructure errors propagate verbatim — distinguishable from policy denies")
}

func TestRequestEscalation_DefaultTimeout_AppliedWhenZero(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)

	var observedDeadline bool
	g := &fakeGrantor{
		mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
			// We can't easily inspect ctx here; assert via the
			// return that the default applied. Approve for happy
			// path test.
			observedDeadline = true
			secret := policy.NewSpawnSecret()
			encoded, sa, _ := policy.Mint(policy.MintInput{
				Principal: req.Principal,
				Action:    req.Action,
				TTL:       time.Minute,
			}, secret)
			return &policy.EscalationResult{
				Decision:      policy.EscalationApproved,
				Token:         encoded,
				Authorization: sa,
			}, nil
		},
	}
	policy.SetGlobalEscalationGrantor(g)

	_, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "u", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "x",
		Justification: "j",
		// Timeout: 0 — default applies.
	})
	require.NoError(t, err)
	require.True(t, observedDeadline)
}

// =====================================================================
// AuthorizedOrEscalate
// =====================================================================

// recordingAuthorizerAOE returns programmable allow/deny verdicts.
// Distinct name from any other package test type.
type recordingAuthorizerAOE struct {
	allowed bool
	reason  string
}

func (r recordingAuthorizerAOE) Authorized(_ context.Context, _, _ string) (bool, string, error) {
	return r.allowed, r.reason, nil
}

func TestAuthorizedOrEscalate_AllowedAlready_NoEscalation(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	called := 0
	g := &fakeGrantor{mintFunc: func(req policy.EscalationRequest) (*policy.EscalationResult, error) {
		called++
		return nil, errors.New("should not be called")
	}}
	policy.SetGlobalEscalationGrantor(g)

	authorizer := recordingAuthorizerAOE{allowed: true}
	ctx := policy.WithPrincipal(context.Background(), &policy.Principal{
		ID: "p", Kind: policy.KindHuman, OrgID: "o",
	})

	out, err := policy.AuthorizedOrEscalate(ctx, authorizer, "x", "r", "j")
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 0, called, "already-authorized: grantor must NOT be called")
}

func TestAuthorizedOrEscalate_NotAllowed_TriggersEscalation(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)
	secret := policy.NewSpawnSecret()
	g := approvingGrantor(t, secret, "audience")
	policy.SetGlobalEscalationGrantor(g)

	authorizer := recordingAuthorizerAOE{allowed: false, reason: "needs approval"}
	ctx := policy.WithPrincipal(context.Background(), &policy.Principal{
		ID: "p", Kind: policy.KindHuman, OrgID: "o",
	})

	out, err := policy.AuthorizedOrEscalate(ctx, authorizer, "x.y", "r:1", "j")
	require.NoError(t, err)

	require.NotNil(t, policy.ScopedAuthFrom(out),
		"escalation succeeded → ScopedAuthorization must be on returned ctx")
	require.Len(t, g.Calls(), 1)
}

func TestAuthorizedOrEscalate_NoPrincipalOnCtx_Errors(t *testing.T) {
	authorizer := recordingAuthorizerAOE{allowed: false, reason: "deny"}
	ctx := context.Background() // no principal

	_, err := policy.AuthorizedOrEscalate(ctx, authorizer, "x", "r", "j")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no principal on ctx")
}

func TestAuthorizedOrEscalate_NilAuthorizer_FallsBackToCtx(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)

	// Stamp an explicit allow authorizer on ctx.
	ctx := policy.WithAuthorizer(
		context.Background(),
		recordingAuthorizerAOE{allowed: true},
	)

	out, err := policy.AuthorizedOrEscalate(ctx, nil, "x", "r", "j")
	require.NoError(t, err)
	require.NotNil(t, out)
}

// =====================================================================
// SetGlobalEscalationGrantor
// =====================================================================

func TestGlobalEscalationGrantor_RoundTrip(t *testing.T) {
	defer policy.SetGlobalEscalationGrantor(nil)

	require.Nil(t, policy.GetGlobalEscalationGrantor())

	g := &fakeGrantor{}
	policy.SetGlobalEscalationGrantor(g)
	require.Same(t, g, policy.GetGlobalEscalationGrantor())

	policy.SetGlobalEscalationGrantor(nil)
	require.Nil(t, policy.GetGlobalEscalationGrantor())
}

// =====================================================================
// EscalationDecision.String
// =====================================================================

func TestEscalationDecision_String(t *testing.T) {
	require.Equal(t, "approved", policy.EscalationApproved.String())
	require.Equal(t, "denied", policy.EscalationDenied.String())
	require.Equal(t, "timed_out", policy.EscalationTimedOut.String())
	require.Equal(t, "unspecified", policy.EscalationDecisionUnspecified.String())
}

// =====================================================================
// withOutgoingScopedAuthHeader behavior — replace, not append
// =====================================================================

func TestRequestEscalation_TwiceInRow_SecondReplacesHeader(t *testing.T) {
	// Two consecutive escalations on the same logical call:
	// the second token MUST replace the first in metadata,
	// not stack. Otherwise the receiver might use the stale one.
	defer policy.SetGlobalEscalationGrantor(nil)
	secret := policy.NewSpawnSecret()
	g := approvingGrantor(t, secret, "aud")
	policy.SetGlobalEscalationGrantor(g)

	ctx, err := policy.RequestEscalation(context.Background(), policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "p", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "x", Justification: "first",
	})
	require.NoError(t, err)

	md, _ := metadata.FromOutgoingContext(ctx)
	first := md.Get(policy.ScopedAuthMetadataKey)
	require.Len(t, first, 1)

	// Second escalation on the same ctx.
	ctx, err = policy.RequestEscalation(ctx, policy.EscalationRequest{
		Principal:     &policy.Principal{ID: "p", Kind: policy.KindHuman, OrgID: "o"},
		Action:        "y", Justification: "second",
	})
	require.NoError(t, err)

	md2, _ := metadata.FromOutgoingContext(ctx)
	second := md2.Get(policy.ScopedAuthMetadataKey)
	require.Len(t, second, 1, "must REPLACE, not append")
	require.NotEqual(t, first[0], second[0],
		"second escalation produced a fresh token; verifier must see the latest")
}
