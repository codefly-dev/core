package workcontext_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// The session a capability opens is the ISSUER's to assign when it has one to
// bind — the session of the caller it authenticated — and fresh otherwise.
// Every test here verifies what was minted, so "the minter wrote the field" and
// "a verifier reads it back" are the same assertion.

const boundSession = "019f6bf7-2222-7222-8222-222222222271"

func (h *harness) startIn(in workcontext.StartInput) (string, error) {
	in.InstallationID = installation
	in.TenantID = tenant
	in.OwnerPrincipalID = ownerID
	in.OwnerPrincipalKind = "human"
	in.OrganizationID = organization
	in.TaskID = taskID
	in.Audience = audience
	in.AuthorityScopes = []*basev0.WorkScopeV1{scope("repo", []string{"read", "write"}, nil)}
	in.TTL = time.Hour
	token, _, err := h.authority.Start(context.Background(), in)
	return token, err
}

func (h *harness) childIn(parent *workcontext.Verified, session string) (string, error) {
	token, _, err := h.authority.Child(context.Background(), parent, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "fixture.test/agent:1.0.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           30 * time.Minute,
		SessionID:     session,
	})
	return token, err
}

func TestStart_OpensTheSessionTheIssuerBound(t *testing.T) {
	h := newHarness(t)
	token, err := h.startIn(workcontext.StartInput{SessionID: boundSession})
	require.NoError(t, err)

	require.Equal(t, boundSession, h.mustVerify(audience, token).Context().GetSessionId())
}

// The default is unchanged, and fresh means fresh: two mints never share one,
// so a capability that names no bound session cannot pair with anything.
func TestStart_OpensAFreshSessionWhenTheIssuerBoundNone(t *testing.T) {
	h := newHarness(t)
	first, err := h.startIn(workcontext.StartInput{})
	require.NoError(t, err)
	second, err := h.startIn(workcontext.StartInput{})
	require.NoError(t, err)

	a := h.mustVerify(audience, first).Context().GetSessionId()
	b := h.mustVerify(audience, second).Context().GetSessionId()
	require.NotEmpty(t, a)
	require.NotEqual(t, a, b)
}

// A supplied session is a field like any other: the schema bounds it, so an id
// no verifier would accept is refused at the mint rather than in another process.
func TestStart_RefusesASessionTheSchemaRefuses(t *testing.T) {
	h := newHarness(t)
	_, err := h.startIn(workcontext.StartInput{SessionID: strings.Repeat("s", 513)})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
}

func TestChild_OpensTheSessionTheIssuerBoundUnderItsParent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	token, err := h.childIn(owner, boundSession)
	require.NoError(t, err)

	child := h.mustVerify(audience, token).Context()
	require.Equal(t, boundSession, child.GetSessionId())
	require.Equal(t, owner.Context().GetSessionId(), child.GetParentSessionId(),
		"the lineage is the parent's session whatever the child's own is")
}

func TestChild_OpensAFreshSessionWhenTheIssuerBoundNone(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	token, err := h.childIn(owner, "")
	require.NoError(t, err)

	child := h.mustVerify(audience, token).Context()
	require.NotEmpty(t, child.GetSessionId())
	require.NotEqual(t, owner.Context().GetSessionId(), child.GetSessionId())
}

// A hop is a new session. Reusing the parent's would make the child
// indistinguishable from it by session, and parent_session_id would name the
// capability itself.
func TestChild_RefusesItsParentsOwnSession(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, err := h.childIn(owner, owner.Context().GetSessionId())
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "new session")
}
