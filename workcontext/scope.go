package workcontext

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ScopeContained applies the Work Context attenuation rule to one scope: a
// child may narrow a wildcard parent to explicit ids but may never widen an
// explicit parent set, and may never name an action the parent does not hold.
// The resource owner narrows the same way: a parent row naming an owner
// covers only a child naming that same owner, so a child can add an owner to
// a row that names none but never drop or change one. A reader asking about a
// resource asks with the resource's owner; an ask naming no owner is never
// covered by an owner-selected row, so an owner-blind check BUILT ON THIS
// FUNCTION fails closed. A reader that inspects WorkScopeV1 fields itself —
// flattening a scope with no ids to "kind:action", or copying kind, actions
// and ids into its own type — gets no such guarantee: it reads an
// owner-selected scope as the whole kind. Such a reader must refuse, or grant
// nothing from, a scope with a non-empty resource_owner_id until it compares
// owners (docs/work-context.md lists the known ones).
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

// InheritResourceOwner returns the scopes to mint an operation child with
// under a parent capability holding parent: the operation's own authority
// (requested, which never names an owner) narrowed to the owner the parent
// selects. It is how a runtime mints under an owner-scoped parent without
// writing an owner into any declaration, binding or selection.
//
// Each requested scope the parent covers as written is kept as written. One
// the parent covers only once an owner is named gets that owner, when exactly
// one of the owners the parent selects for its kind covers it. It fails
// closed, with ErrInvalid naming the reason, when no owner covers the scope
// (the parent does not hold it), when several do (the runtime must mint one
// child per owner rather than have one chosen for it), and when a requested
// scope already names an owner the parent does not cover it under — an owner
// someone wrote is never replaced. The result is a fresh copy; requested and
// parent are only read, and the result attenuates parent.
func InheritResourceOwner(requested, parent []*basev0.WorkScopeV1) ([]*basev0.WorkScopeV1, error) {
	inherited := make([]*basev0.WorkScopeV1, 0, len(requested))
	for _, scope := range requested {
		child := proto.CloneOf(scope)
		if ScopeContained(child, parent) {
			inherited = append(inherited, child)
			continue
		}
		if owner := child.GetResourceOwnerId(); owner != "" {
			return nil, fmt.Errorf("%w: scope %q names resource owner %q, which the parent does not cover it under", ErrInvalid, child.GetResourceKind(), owner)
		}
		var covering []string
		for _, row := range parent {
			owner := row.GetResourceOwnerId()
			if owner == "" || row.GetResourceKind() != child.GetResourceKind() || slices.Contains(covering, owner) {
				continue
			}
			child.ResourceOwnerId = owner
			if ScopeContained(child, parent) {
				covering = append(covering, owner)
			}
		}
		switch len(covering) {
		case 0:
			return nil, fmt.Errorf("%w: scope %q is not covered by the parent, with or without a resource owner", ErrInvalid, child.GetResourceKind())
		case 1:
			child.ResourceOwnerId = covering[0]
			inherited = append(inherited, child)
		default:
			return nil, fmt.Errorf("%w: owner-scoped parent: scope %q is covered under resource owners %q, so the owner is ambiguous; mint one child per owner", ErrInvalid, child.GetResourceKind(), covering)
		}
	}
	return inherited, nil
}
