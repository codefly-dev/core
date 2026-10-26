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

func owned(kind string, actions, ids []string, owner string) *basev0.WorkScopeV1 {
	s := scope(kind, actions, ids)
	s.ResourceOwnerId = owner
	return s
}

// An owner narrows a scope like ids do: a child may add one, never drop or
// change it, and a question naming no owner is never answered by an
// owner-selected scope.
func TestScopeContained_ResourceOwner(t *testing.T) {
	for name, tc := range map[string]struct {
		child     *basev0.WorkScopeV1
		parents   []*basev0.WorkScopeV1
		contained bool
	}{
		"the same owner":                         {owned("doc", []string{"read"}, nil, "u"), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "u")}, true},
		"ids within the owner":                   {owned("doc", []string{"read"}, []string{"a"}, "u"), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "u")}, true},
		"an owner added to a whole kind":         {owned("doc", []string{"read"}, nil, "u"), []*basev0.WorkScopeV1{scope("doc", []string{"read"}, nil)}, true},
		"an owner added to named ids":            {owned("doc", []string{"read"}, []string{"a"}, "u"), []*basev0.WorkScopeV1{scope("doc", []string{"read"}, []string{"a"})}, true},
		"another owner":                          {owned("doc", []string{"read"}, nil, "v"), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "u")}, false},
		"the owner dropped":                      {scope("doc", []string{"read"}, nil), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "u")}, false},
		"an id asked without its owner":          {scope("doc", []string{"read"}, []string{"a"}), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "u")}, false},
		"the owner's ids widened":                {owned("doc", []string{"read"}, nil, "u"), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, []string{"a"}, "u")}, false},
		"an id outside the owner's":              {owned("doc", []string{"read"}, []string{"b"}, "u"), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, []string{"a"}, "u")}, false},
		"another owner's row beside a plain row": {owned("doc", []string{"read"}, []string{"a"}, "v"), []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "u"), scope("doc", []string{"read"}, []string{"a"})}, true},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.contained, workcontext.ScopeContained(tc.child, tc.parents))
		})
	}
}

// A capability carrying owner-selected scopes is signed, verified and
// narrowed like any other; a child cannot drop the owner, and an owner the
// schema refuses never mints.
func TestOwnerSelectedScopesMintVerifyAndAttenuate(t *testing.T) {
	h := newHarness(t)
	start := func(scopes ...*basev0.WorkScopeV1) (string, error) {
		token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
			InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human",
			OrganizationID: organization, TaskID: taskID, Audience: audience, AuthorityScopes: scopes, TTL: time.Hour,
		})
		return token, err
	}
	token, err := start(owned("doc", []string{"read", "write"}, nil, "principal-u"))
	require.NoError(t, err)
	parent := h.mustVerify(audience, token)
	require.Equal(t, "principal-u", parent.EffectiveScopes()[0].GetResourceOwnerId())

	child := func(scopes ...*basev0.WorkScopeV1) error {
		_, _, err := h.authority.Child(context.Background(), parent, workcontext.ChildInput{
			PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0", DelegationID: "d-1",
			GrantedScopes: scopes, Audience: audience, TTL: 30 * time.Minute,
		})
		return err
	}
	require.NoError(t, child(owned("doc", []string{"read"}, []string{"a"}, "principal-u")))
	require.ErrorIs(t, child(scope("doc", []string{"read"}, nil)), workcontext.ErrInvalid)
	require.ErrorIs(t, child(owned("doc", []string{"read"}, nil, "principal-v")), workcontext.ErrInvalid)

	for _, malformed := range []string{" principal-u", "principal u", "principal-u\n", "*", strings.Repeat("u", 513)} {
		_, err := start(owned("doc", []string{"read"}, nil, malformed))
		require.Error(t, err, "owner %q", malformed)
	}
}
