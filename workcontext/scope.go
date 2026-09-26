package workcontext

import (
	"slices"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ScopeContained applies the Work Context attenuation rule to one scope: a
// child may narrow a wildcard parent to explicit ids but may never widen an
// explicit parent set, and may never name an action the parent does not hold.
func ScopeContained(child *basev0.WorkScopeV1, parents []*basev0.WorkScopeV1) bool {
	for _, parent := range parents {
		if parent.GetResourceKind() != child.GetResourceKind() {
			continue
		}
		for _, action := range child.GetActions() {
			if !slices.Contains(parent.GetActions(), action) {
				return false
			}
		}
		if len(parent.GetResourceIds()) == 0 {
			return true
		}
		if len(child.GetResourceIds()) == 0 {
			return false
		}
		for _, id := range child.GetResourceIds() {
			if !slices.Contains(parent.GetResourceIds(), id) {
				return false
			}
		}
		return true
	}
	return false
}

// ScopesAttenuate reports whether every scope in child is contained in
// parents. An empty child attenuates anything: holding nothing is always a
// narrowing.
func ScopesAttenuate(child, parents []*basev0.WorkScopeV1) bool {
	for _, scope := range child {
		if !ScopeContained(scope, parents) {
			return false
		}
	}
	return true
}
