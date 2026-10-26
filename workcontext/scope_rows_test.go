package workcontext_test

import (
	"testing"

	base "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

func TestScopeContainedActionResourceRows(t *testing.T) {
	parents := []*base.WorkScopeV1{
		scope("profiles", []string{"read"}, []string{"a"}),
		scope("profiles", []string{"invoke"}, []string{"b"}),
		scope("profiles", []string{"read"}, []string{"b"}),
		scope("profiles", []string{"list"}, nil),
	}
	for _, tc := range []struct {
		name         string
		actions, ids []string
		want         bool
	}{
		{"later row", []string{"invoke"}, []string{"b"}, true},
		{"union of resources for same action", []string{"read"}, []string{"a", "b"}, true},
		{"intersection for several actions", []string{"read", "invoke"}, []string{"b"}, true},
		{"no cross product", []string{"read", "invoke"}, []string{"a", "b"}, false},
		{"wrong action resource pair", []string{"invoke"}, []string{"a"}, false},
		{"restricted cannot become whole kind", []string{"read"}, nil, false},
		{"later unrestricted action", []string{"list"}, nil, true},
		{"wildcard applies only to its action", []string{"list", "read"}, nil, false},
		{"unknown action", []string{"delete"}, []string{"a"}, false},
		{"explicit zero action cap", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := scope("profiles", tc.actions, tc.ids)
			for range 2 {
				if got := workcontext.ScopeContained(child, parents); got != tc.want {
					t.Fatalf("ScopeContained=%v, want %v", got, tc.want)
				}
				for left, right := 0, len(parents)-1; left < right; left, right = left+1, right-1 {
					parents[left], parents[right] = parents[right], parents[left]
				}
			}
		})
	}
}
