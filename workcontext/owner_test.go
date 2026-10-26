package workcontext_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

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

}

// Verify holds a presented chain to the owner rule on its own, not only
// through the minter: a genuinely signed hop that drops or changes its
// parent's owner is refused as a widening, and the sound hop it was rewritten
// from verifies.
func TestVerifyRefusesAHopThatDropsOrChangesTheOwner(t *testing.T) {
	h := newHarness(t)
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human",
		OrganizationID: organization, TaskID: taskID, Audience: audience, TTL: time.Hour,
		AuthorityScopes: []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, "principal-u")},
	})
	require.NoError(t, err)
	parent := h.mustVerify(audience, token)
	sound, signed, err := h.authority.Child(context.Background(), parent, workcontext.ChildInput{
		PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0", DelegationID: "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{owned("doc", []string{"read"}, []string{"a"}, "principal-u")},
		Audience:      audience, TTL: 30 * time.Minute,
	})
	require.NoError(t, err)
	_, err = h.verify(audience, sound)
	require.NoError(t, err)

	for name, owner := range map[string]string{"dropped": "", "changed": "principal-v"} {
		t.Run(name, func(t *testing.T) {
			altered := proto.CloneOf(signed)
			hop := altered.GetActorChain()[len(altered.GetActorChain())-1]
			hop.GrantedScopes = []*basev0.WorkScopeV1{owned("doc", []string{"read"}, []string{"a"}, owner)}
			_, err := h.verify(audience, h.resign(altered))
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.ErrorContains(t, err, "widens authority")
		})
	}
}

// The owner is bounded in bytes and carries nothing that prints as nothing or
// as something else: no ASCII or Unicode whitespace, no control, format
// (zero-width) or private-use character. Two owners that look alike in an
// audit line are then the same bytes, and the schema, not each reader, says so.
func TestResourceOwnerSchema(t *testing.T) {
	h := newHarness(t)
	start := func(owner string) error {
		_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
			InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human",
			OrganizationID: organization, TaskID: taskID, Audience: audience, TTL: time.Hour,
			AuthorityScopes: []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, owner)},
		})
		return err
	}
	for name, owner := range map[string]string{
		"512 ASCII bytes":              strings.Repeat("u", 512),
		"512 bytes of four-byte runes": strings.Repeat("\U0001F600", 128),
		"an accented principal":        "principal-é",
		"a path-like principal":        "solutions/org-1/wiki:principal",
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			require.NoError(t, start(owner))
		})
	}
	for name, owner := range map[string]string{
		"513 ASCII bytes":            strings.Repeat("u", 513),
		"2048 bytes in 512 runes":    strings.Repeat("\U0001F600", 512),
		"a leading space":            " principal-u",
		"an inner space":             "principal u",
		"a trailing newline":         "principal-u\n",
		"a tab":                      "principal\tu",
		"a vertical tab":             "principal\vu",
		"a form feed":                "principal\fu",
		"a non-breaking space":       "principal-u\u00a0",
		"an ideographic space":       "principal\u3000u",
		"a line separator":           "principal\u2028u",
		"a zero-width space":         "principal-u\u200b",
		"a zero-width joiner":        "principal\u200du",
		"a byte-order mark":          "\ufeffprincipal-u",
		"a NUL":                      "principal\x00u",
		"a start-of-heading control": "principal\x01u",
		"DEL":                        "principal\x7fu",
		"a C1 control":               "principal\u0085u",
		"a private-use character":    "principal\ue000u",
		"the wildcard":               "*",
		"a wildcard inside":          "principal-*",
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			err := start(owner)
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.Contains(t, err.Error(), "resource_owner_id")
		})
	}
}

// A runtime mints an operation child from the operation's owner-less authority
// under an owner-scoped parent by inheriting the parent's owner; it never
// guesses one, never replaces one someone wrote, and never widens.
func TestInheritResourceOwner(t *testing.T) {
	rows := func(scopes ...*basev0.WorkScopeV1) []*basev0.WorkScopeV1 { return scopes }
	for name, tc := range map[string]struct {
		requested, parent []*basev0.WorkScopeV1
		want              []*basev0.WorkScopeV1
		refusal           string
	}{
		"an owner-less parent keeps the scope as written": {
			requested: rows(scope("doc", []string{"read"}, []string{"a"})),
			parent:    rows(scope("doc", []string{"read"}, nil)),
			want:      rows(scope("doc", []string{"read"}, []string{"a"})),
		},
		"the parent's one owner is inherited": {
			requested: rows(scope("doc", []string{"read"}, nil)),
			parent:    rows(owned("doc", []string{"read", "write"}, nil, "u")),
			want:      rows(owned("doc", []string{"read"}, nil, "u")),
		},
		"each scope is answered on its own": {
			requested: rows(scope("doc", []string{"read"}, []string{"a"}), scope("note", []string{"write"}, nil)),
			parent:    rows(owned("doc", []string{"read"}, nil, "u"), scope("note", []string{"write"}, nil)),
			want:      rows(owned("doc", []string{"read"}, []string{"a"}, "u"), scope("note", []string{"write"}, nil)),
		},
		"an owner the scope already names and the parent covers is kept": {
			requested: rows(owned("doc", []string{"read"}, nil, "u")),
			parent:    rows(owned("doc", []string{"read"}, nil, "u")),
			want:      rows(owned("doc", []string{"read"}, nil, "u")),
		},
		"no owner covers an action the parent does not hold": {
			requested: rows(scope("doc", []string{"write"}, nil)),
			parent:    rows(owned("doc", []string{"read"}, nil, "u")),
			refusal:   "not covered by the parent",
		},
		"no owner covers another kind": {
			requested: rows(scope("note", []string{"read"}, nil)),
			parent:    rows(owned("doc", []string{"read"}, nil, "u")),
			refusal:   "not covered by the parent",
		},
		"no owner covers the whole kind under an owner's ids": {
			requested: rows(scope("doc", []string{"read"}, nil)),
			parent:    rows(owned("doc", []string{"read"}, []string{"a"}, "u")),
			refusal:   "not covered by the parent",
		},
		"two owners covering one scope are ambiguous": {
			requested: rows(scope("doc", []string{"read"}, []string{"a"})),
			parent:    rows(owned("doc", []string{"read"}, nil, "u"), owned("doc", []string{"read"}, nil, "v")),
			refusal:   "owner-scoped parent",
		},
		"an owner someone wrote is never replaced": {
			requested: rows(owned("doc", []string{"read"}, nil, "v")),
			parent:    rows(owned("doc", []string{"read"}, nil, "u")),
			refusal:   `names resource owner "v"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			requested := cloneScopes(tc.requested)
			parent := cloneScopes(tc.parent)
			got, err := workcontext.InheritResourceOwner(requested, parent)
			require.Equal(t, tc.requested, requested, "the request is only read")
			require.Equal(t, tc.parent, parent, "the parent is only read")
			if tc.refusal != "" {
				require.ErrorIs(t, err, workcontext.ErrInvalid)
				require.ErrorContains(t, err, tc.refusal)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Len(t, got, len(tc.want))
			for i := range got {
				require.True(t, proto.Equal(tc.want[i], got[i]), "scope %d: got %v", i, got[i])
			}
			require.True(t, workcontext.ScopesAttenuate(got, parent))
		})
	}
}

// The inherited scopes mint: an operation child under an owner-scoped parent
// carries the owner and verifies, where the operation's own owner-less scopes
// are refused as a widening.
func TestInheritResourceOwnerMintsUnderAnOwnerScopedParent(t *testing.T) {
	h := newHarness(t)
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human",
		OrganizationID: organization, TaskID: taskID, Audience: audience, TTL: time.Hour,
		AuthorityScopes: []*basev0.WorkScopeV1{owned("doc", []string{"read", "write"}, nil, "principal-u")},
	})
	require.NoError(t, err)
	parent := h.mustVerify(audience, token)
	operation := []*basev0.WorkScopeV1{scope("doc", []string{"read"}, nil)}
	mint := func(scopes []*basev0.WorkScopeV1) (string, error) {
		child, _, err := h.authority.Child(context.Background(), parent, workcontext.ChildInput{
			PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0", DelegationID: "d-1",
			GrantedScopes: scopes, Audience: audience, TTL: 30 * time.Minute,
		})
		return child, err
	}
	_, err = mint(operation)
	require.ErrorIs(t, err, workcontext.ErrInvalid, "the operation's owner-less scope widens an owner-scoped parent")

	inherited, err := workcontext.InheritResourceOwner(operation, parent.EffectiveScopes())
	require.NoError(t, err)
	child, err := mint(inherited)
	require.NoError(t, err)
	verified := h.mustVerify(audience, child)
	require.Equal(t, "principal-u", verified.EffectiveScopes()[0].GetResourceOwnerId())
}

func cloneScopes(scopes []*basev0.WorkScopeV1) []*basev0.WorkScopeV1 {
	cloned := make([]*basev0.WorkScopeV1, len(scopes))
	for i, s := range scopes {
		cloned[i] = proto.CloneOf(s)
	}
	return cloned
}
