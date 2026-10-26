package workcontext

import (
	"slices"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ScopeContained applies the Work Context attenuation rule to one scope: a
// child may narrow a wildcard parent to explicit ids but may never widen an
// explicit parent set, and may never name an action the parent does not hold.
// The resource owner narrows the same way: a parent row naming an owner
// covers only a child naming that same owner, so a child can add an owner to
// a row that names none but never drop or change one. A reader asking about a
// resource asks with the resource's owner; an ask naming no owner is never
// covered by an owner-selected row, so an owner-blind check fails closed.
func ScopeContained(child *basev0.WorkScopeV1, parents []*basev0.WorkScopeV1) bool {
	// A kind can have several rows with different action/resource coverage.
	// Check each pair against that coverage, never their Cartesian product.
	kindPresent := false
	for _, parent := range parents {
		kindPresent = kindPresent || parent.GetResourceKind() == child.GetResourceKind()
	}
	if !kindPresent {
		return false
	}
	for _, action := range child.GetActions() {
		wholeKind := false
		ids := map[string]bool{}
		for _, parent := range parents {
			if parent.GetResourceKind() != child.GetResourceKind() || !slices.Contains(parent.GetActions(), action) {
				continue
			}
			if owner := parent.GetResourceOwnerId(); owner != "" && owner != child.GetResourceOwnerId() {
				continue
			}
			if len(parent.GetResourceIds()) == 0 {
				wholeKind = true
				break
			}
			for _, id := range parent.GetResourceIds() {
				ids[id] = true
			}
		}
		if wholeKind {
			continue
		}
		if len(child.GetResourceIds()) == 0 {
			return false
		}
		for _, id := range child.GetResourceIds() {
			if !ids[id] {
				return false
			}
		}
	}
	return true
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
