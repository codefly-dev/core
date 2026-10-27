package workcontext_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// The owner is at most 512 bytes of printable ASCII other than space and "*".
// An allowlist rather than a list of excluded categories: Unicode keeps
// characters that print as nothing or as another letter outside any category
// a pattern can name, and engines disagree on the categories themselves (Go's
// unicode.C includes unassigned code points; Python protovalidate's does not).
// The vectors live in testdata so a second implementation's validator is
// checked against the same list.
func TestResourceOwnerSchema(t *testing.T) {
	type vector struct {
		Name   string `json:"name"`
		Owner  string `json:"owner"`
		Repeat int    `json:"repeat"`
	}
	var vectors struct {
		Accepted []vector `json:"accepted"`
		Refused  []vector `json:"refused"`
	}
	data, err := os.ReadFile(filepath.Join("testdata", "resource_owner_vectors.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &vectors))
	require.NotEmpty(t, vectors.Accepted)
	require.NotEmpty(t, vectors.Refused)
	owner := func(v vector) string {
		if v.Repeat > 0 {
			return strings.Repeat(v.Owner, v.Repeat)
		}
		return v.Owner
	}

	h := newHarness(t)
	start := func(owner string) error {
		_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
			InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human",
			OrganizationID: organization, TaskID: taskID, Audience: audience, TTL: time.Hour,
			AuthorityScopes: []*basev0.WorkScopeV1{owned("doc", []string{"read"}, nil, owner)},
		})
		return err
	}
	for _, v := range vectors.Accepted {
		t.Run("accepts "+v.Name, func(t *testing.T) {
			require.NoError(t, start(owner(v)))
		})
	}
	for _, v := range vectors.Refused {
		t.Run("refuses "+v.Name, func(t *testing.T) {
			err := start(owner(v))
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
		// Intended, and fail-closed: the owner applies to the whole scope, so
		// the read the parent holds on every document is confined to P's.
		"an owner applies to the whole scope, read included": {
			requested: rows(scope("doc", []string{"read", "write"}, nil)),
			parent:    rows(scope("doc", []string{"read"}, nil), owned("doc", []string{"write"}, nil, "P")),
			want:      rows(owned("doc", []string{"read", "write"}, nil, "P")),
		},
		"ids covered only by two owners together": {
			requested: rows(scope("doc", []string{"read"}, []string{"a", "b"})),
			parent:    rows(owned("doc", []string{"read"}, []string{"a"}, "P"), owned("doc", []string{"read"}, []string{"b"}, "Q")),
			refusal:   `owner-scoped parent: scope "doc" is covered only across resource owners ["P" "Q"] together, never under one; each of its resources is covered under one owner, so invoke it once per owner`,
		},
		"actions covered only by two owners together": {
			requested: rows(scope("doc", []string{"read", "write"}, nil)),
			parent:    rows(owned("doc", []string{"read"}, nil, "P"), owned("doc", []string{"write"}, nil, "Q")),
			refusal:   `covered only across resource owners ["P" "Q"] together, never under one; they split its actions, so no child can hold the whole scope`,
		},
		// Ids named, but the owners split the actions on each: no per-owner
		// invocation could hold the operation's whole scope.
		"named ids whose actions two owners split": {
			requested: rows(scope("doc", []string{"read", "write"}, []string{"a"})),
			parent:    rows(owned("doc", []string{"read"}, []string{"a"}, "P"), owned("doc", []string{"write"}, []string{"a"}, "Q")),
			refusal:   "they split its actions",
		},
		"an id no owner covers beside ids two owners do": {
			requested: rows(scope("doc", []string{"read"}, []string{"a", "c"})),
			parent:    rows(owned("doc", []string{"read"}, []string{"a"}, "P"), owned("doc", []string{"read"}, []string{"b"}, "Q")),
			refusal:   "not covered by the parent",
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
