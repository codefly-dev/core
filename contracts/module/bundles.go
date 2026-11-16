package module

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrUnresolvedInclude means a role includes a bundle that cannot be granted
// as declared: the contract was resolved with no bundle declarations to read
// it from, the module publishes no such bundle, or the unit declares no single
// binding a person causes to the module the bundle reaches kinds through.
var ErrUnresolvedInclude = errors.New("module contract include cannot be resolved")

// Bundle is one named set of scopes a module publishes (Contract.Bundles):
// what one kind of screen calls, across every module it reaches. Each scope
// names a kind a screen reaches directly (no through), or one it reaches
// downstream of a module (through: that module's principal, the publisher's
// own or another the screen calls), which a unit including the bundle reaches
// through its own binding to that module. A bundle may include other bundles
// of the same module.
type Bundle struct {
	Name     string         `yaml:"name"`
	Includes []string       `yaml:"includes,omitempty"`
	Scopes   []CeilingScope `yaml:"scopes,omitempty"`
}

// RoleInclude names one bundle another module publishes, by that module's
// principal and the bundle's name.
type RoleInclude struct {
	Module string `yaml:"module"`
	Bundle string `yaml:"bundle"`
}

// Bundles answers a bundle a composed module publishes, read from that
// module's own declarations (its Contract.Bundle): its scopes, its includes
// expanded. An unknown module or bundle is an error naming it.
type Bundles interface {
	Bundle(module, name string) ([]CeilingScope, error)
}

// Bundle is the scopes the named bundle grants with every bundle it includes
// expanded, in one deterministic order: each kind once per place it is
// reached (through), its actions sorted, the scopes sorted by kind then
// through. A bundle the module does not publish, or one that includes itself
// through any chain, is refused by name.
func (contract *Contract) Bundle(name string) ([]CeilingScope, error) {
	byName := map[string]Bundle{}
	for _, bundle := range contract.Bundles {
		byName[bundle.Name] = bundle
	}
	if _, ok := byName[name]; !ok {
		return nil, fmt.Errorf("%w: %s publishes no bundle %q", ErrUnresolvedInclude, contract.Principal, name)
	}
	if err := errors.Join(checkBundleIncludes(contract), checkBundlesAcyclic(contract)); err != nil {
		return nil, err
	}
	var scopes []CeilingScope
	var visit func(name string)
	visit = func(name string) {
		scopes = append(scopes, byName[name].Scopes...)
		for _, included := range byName[name].Includes {
			visit(included)
		}
	}
	visit(name)
	return mergeScopes(nil, scopes), nil
}

// bundleCycle is the chain by which name includes itself, or nil.
func (contract *Contract) bundleCycle(name string, path []string) []string {
	if slices.Contains(path, name) {
		return append(path[slices.Index(path, name):], name)
	}
	for _, bundle := range contract.Bundles {
		if bundle.Name != name {
			continue
		}
		for _, included := range bundle.Includes {
			if cycle := contract.bundleCycle(included, append(slices.Clone(path), name)); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

// mergeScopes adds scopes to into, one entry per kind and place (through),
// actions unioned and sorted, entries sorted by kind then through.
func mergeScopes(into, scopes []CeilingScope) []CeilingScope {
	out := make([]CeilingScope, 0, len(into)+len(scopes))
	at := map[[2]string]int{}
	for _, scope := range append(slices.Clone(into), scopes...) {
		key := [2]string{scope.ResourceKind, scope.Through}
		index, held := at[key]
		if !held {
			at[key] = len(out)
			out = append(out, CeilingScope{ResourceKind: scope.ResourceKind, Through: scope.Through})
			index = len(out) - 1
		}
		out[index].Actions = append(out[index].Actions, scope.Actions...)
	}
	for index := range out {
		slices.Sort(out[index].Actions)
		out[index].Actions = slices.Compact(out[index].Actions)
	}
	slices.SortStableFunc(out, func(a, b CeilingScope) int {
		if c := strings.Compare(a.ResourceKind, b.ResourceKind); c != 0 {
			return c
		}
		return strings.Compare(a.Through, b.Through)
	})
	return out
}

// includedRoles are the contract's roles with every included bundle granted as
// the role's own scopes: a scope the bundle reaches directly as a literal
// scope, one it reaches downstream of a module through the unit's one binding
// a person causes to that module. A role including nothing is unchanged. Every
// include that cannot be granted is reported, in declaration order.
func (contract *Contract) includedRoles(resolved *Resolved, delivered map[string][]int, bundles Bundles) ([]Role, []string) {
	roles := make([]Role, 0, len(contract.Roles))
	var problems []string
	for _, role := range contract.Roles {
		if len(role.Includes) == 0 {
			roles = append(roles, role)
			continue
		}
		var granted []CeilingScope
		for _, include := range role.Includes {
			label := fmt.Sprintf("role %s includes %s/%s", role.Name, include.Module, include.Bundle)
			if bundles == nil {
				problems = append(problems, label+", and the contract was resolved with no bundle declarations to read it from")
				continue
			}
			scopes, err := bundles.Bundle(include.Module, include.Bundle)
			if err != nil {
				problems = append(problems, label+": "+err.Error())
				continue
			}
			for _, scope := range scopes {
				if scope.Through != "" {
					binding, reason := contract.personBindingTo(scope.Through, resolved, delivered)
					if reason != "" {
						problems = append(problems, label+", which reaches "+scope.ResourceKind+" through "+scope.Through+": "+reason)
						continue
					}
					scope.Through = binding
				}
				// One source per permission: a role grants what an include
				// grants it once, so the role's own scopes are its page's own
				// calls alone, never a copy of a bundle that drifts from it.
				if twice := grantedBy(role.Scopes, scope); len(twice) > 0 {
					problems = append(problems, fmt.Sprintf("%s, which grants %s:%s that the role also grants itself; a role grants each permission once, so it names only its own page's calls beside what it includes",
						label, scope.ResourceKind, strings.Join(twice, ",")))
					continue
				}
				granted = append(granted, scope)
			}
		}
		role.Scopes = mergeScopes(role.Scopes, granted)
		roles = append(roles, role)
	}
	return roles, problems
}

// grantedBy are the actions of scope that scopes already grant at the same
// kind and place.
func grantedBy(scopes []CeilingScope, scope CeilingScope) []string {
	var twice []string
	for _, held := range scopes {
		if held.ResourceKind == scope.ResourceKind && held.Through == scope.Through {
			for _, action := range scope.Actions {
				if slices.Contains(held.Actions, action) {
					twice = append(twice, action)
				}
			}
		}
	}
	return twice
}

// personBindingTo is the id of the one binding of the unit, for an operation
// a person causes, delivered to audience; or why there is not exactly one.
func (contract *Contract) personBindingTo(audience string, resolved *Resolved, delivered map[string][]int) (string, string) {
	var found []string
	for _, binding := range contract.Bindings {
		if !slices.Contains(binding.Operations, OperationInvoke) && !slices.Contains(binding.Operations, OperationLookup) {
			continue
		}
		for _, index := range delivered[binding.ID] {
			if resolved.Bindings[index].Audience == audience {
				found = append(found, binding.ID)
				break
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], ""
	case 0:
		return "", "the unit declares no binding a person causes to " + audience
	}
	return "", "the unit declares " + strings.Join(found, " and ") + " to " + audience + "; a bundle reaches through one"
}

func includesError(problems []string) error {
	return fmt.Errorf("%w: %s; a bundle is read from the module that publishes it", ErrUnresolvedInclude, strings.Join(problems, "; "))
}
