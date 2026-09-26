package workcontext_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

func TestScopeContained_AttenuationRule(t *testing.T) {
	cases := map[string]struct {
		child     *basev0.WorkScopeV1
		parents   []*basev0.WorkScopeV1
		contained bool
	}{
		"the same scope": {
			child:     scope("repo", []string{"read"}, []string{"codefly/core"}),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
			contained: true,
		},
		"fewer actions": {
			child:     scope("repo", []string{"read"}, []string{"codefly/core"}),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read", "write"}, []string{"codefly/core"})},
			contained: true,
		},
		"a wildcard parent narrowed to explicit ids": {
			child:     scope("repo", []string{"read"}, []string{"codefly/core"}),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
			contained: true,
		},
		"an explicit parent widened to a wildcard": {
			child:     scope("repo", []string{"read"}, nil),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
			contained: false,
		},
		"an id the parent does not name": {
			child:     scope("repo", []string{"read"}, []string{"codefly/other"}),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
			contained: false,
		},
		"an action the parent does not hold": {
			child:     scope("repo", []string{"delete"}, []string{"codefly/core"}),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
			contained: false,
		},
		"a kind the parent does not hold": {
			child:     scope("secret", []string{"read"}, []string{"db"}),
			parents:   []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
			contained: false,
		},
		"no parent at all": {
			child:     scope("repo", []string{"read"}, []string{"codefly/core"}),
			contained: false,
		},
		"the matching kind among several": {
			child: scope("secret", []string{"read"}, []string{"db"}),
			parents: []*basev0.WorkScopeV1{
				scope("repo", []string{"read", "write"}, nil),
				scope("secret", []string{"read"}, nil),
			},
			contained: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.contained, workcontext.ScopeContained(tc.child, tc.parents))
			require.Equal(t, tc.contained, workcontext.ScopesAttenuate([]*basev0.WorkScopeV1{tc.child}, tc.parents))
		})
	}
}

// Holding nothing is a narrowing of anything, which is what lets a hop drop a
// kind entirely rather than having to restate it.
func TestScopesAttenuate_EmptyChildNarrowsAnything(t *testing.T) {
	require.True(t, workcontext.ScopesAttenuate(nil, nil))
	require.True(t, workcontext.ScopesAttenuate(nil, []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)}))
}

// Every scope of a multi-scope hop is held against the parent, so one legal
// scope does not carry an illegal one alongside it.
func TestScopesAttenuate_EveryScopeIsChecked(t *testing.T) {
	parents := []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)}
	child := []*basev0.WorkScopeV1{
		scope("repo", []string{"read"}, []string{"codefly/core"}),
		scope("secret", []string{"read"}, []string{"db"}),
	}
	require.False(t, workcontext.ScopesAttenuate(child, parents))
}
