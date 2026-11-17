package module

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/codefly-dev/core/wire"
	"gopkg.in/yaml.v3"
)

// ErrUnresolvedInclude means an include cannot be granted as declared: the
// contract was resolved with no bundle declarations to read it from, the
// module's contract was not supplied or publishes no such bundle, a
// permission is granted twice (spelled beside an include that grants it), or
// the unit declares no single binding a person causes to the module the
// bundle reaches kinds through.
var ErrUnresolvedInclude = errors.New("module contract include cannot be resolved")

// Bundle is one named set of scopes a module publishes (Contract.Bundles):
// what one kind of screen calls, across every module it reaches. Each scope
// names a kind a screen reaches directly (no through), or one it reaches
// downstream of a module (through: that module's principal, the publisher's
// own or another the screen calls), which a unit including the bundle reaches
// through its own binding to that module. A bundle may include other bundles
// of the same module, by name, and bundles other modules publish, reached
// through the publisher (BundleInclude).
type Bundle struct {
	Name     string          `yaml:"name"`
	Includes []BundleInclude `yaml:"includes,omitempty"`
	Scopes   []CeilingScope  `yaml:"scopes,omitempty"`
}

// BundleInclude is one bundle a bundle includes. Written as a name, it is a
// bundle of the same module, whose scopes are granted as that bundle spells
// them. Written as {module, bundle, through}, it is a bundle another module
// publishes, reached through the including module (through names the
// includer's own principal, as a scope it spells does): every scope of the
// included bundle is granted exactly as if the includer spelled it with that
// through, whatever place the included bundle itself reaches it at, so the
// includer names none of the other module's kinds and reaches none of them
// anywhere the spelled form would not.
type BundleInclude struct {
	// Module is the principal of the module publishing the bundle; empty for
	// a bundle of the same module.
	Module string
	// Bundle is the included bundle's name.
	Bundle string
	// Through is the module the included bundle's scopes are reached through:
	// the includer's principal for another module's bundle, empty for a
	// bundle of the same module.
	Through string
}

// String names the include as a refusal does: the bundle's name, or
// module/bundle.
func (include BundleInclude) String() string {
	if include.Module == "" {
		return include.Bundle
	}
	return include.Module + "/" + include.Bundle
}

const bundleIncludeShape = "a bundle include is a bundle's name (one of the same module) or {module, bundle, through} (another module's, reached through this one)"

// UnmarshalYAML reads an include in either spelling, strictly: a custom
// decoder is outside the document decoder's known-fields check, so an unknown
// key is refused here.
func (include *BundleInclude) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.ShortTag() != "!!str" {
			return fmt.Errorf("%s; got %q", bundleIncludeShape, node.Value)
		}
		*include = BundleInclude{Bundle: node.Value}
		return nil
	case yaml.MappingNode:
		var read BundleInclude
		named := false
		mapping := wire.ReadMapping(node, func(key string, value *yaml.Node) (bool, error) {
			switch key {
			case "module":
				named = true
				return true, value.Decode(&read.Module)
			case "bundle":
				return true, value.Decode(&read.Bundle)
			case "through":
				return true, value.Decode(&read.Through)
			}
			return false, nil
		})
		switch {
		case mapping.Err != nil:
			return fmt.Errorf("%s: %v", bundleIncludeShape, mapping.Err)
		case mapping.HasUnknown:
			return fmt.Errorf("%s; unknown field %q", bundleIncludeShape, mapping.Unknown)
		case !named:
			return fmt.Errorf("%s; a mapping names no module", bundleIncludeShape)
		}
		*include = read
		return nil
	}
	return fmt.Errorf("%s", bundleIncludeShape)
}

// MarshalYAML writes the include as it is read: a name, or the mapping.
func (include BundleInclude) MarshalYAML() (any, error) {
	if include.Module == "" && include.Through == "" {
		return include.Bundle, nil
	}
	return struct {
		Module  string `yaml:"module"`
		Bundle  string `yaml:"bundle"`
		Through string `yaml:"through,omitempty"`
	}{include.Module, include.Bundle, include.Through}, nil
}

// RoleInclude names one bundle a module publishes, by that module's principal
// and the bundle's name: another module's, or the unit's own.
type RoleInclude struct {
	Module string `yaml:"module"`
	Bundle string `yaml:"bundle"`
}

// Bundles answers a bundle a composed module publishes, read from that
// module's own declarations: its scopes, its includes expanded. An unknown
// module or bundle is an error naming it. Publishers is the implementation
// over published contracts, and the one that follows a bundle's includes of
// other modules' bundles.
type Bundles interface {
	Bundle(module, name string) ([]CeilingScope, error)
}

// Publishers reads the contract a module publishes, by the module's
// principal. It is Bundles over those contracts: the resolver every reader of
// bundles goes through, since a bundle may include another module's.
type Publishers func(module string) (*Contract, error)

// PublishedContracts are publishers over the contracts given, each read by
// its principal; a module whose contract was not given is refused naming it
// and those that were.
func PublishedContracts(contracts ...*Contract) Publishers {
	return func(module string) (*Contract, error) {
		supplied := make([]string, 0, len(contracts))
		for _, contract := range contracts {
			if contract.Principal == module {
				return contract, nil
			}
			supplied = append(supplied, contract.Principal)
		}
		slices.Sort(supplied)
		return nil, fmt.Errorf("no contract was supplied for module %s (supplied: %s)", module, strings.Join(supplied, ", "))
	}
}

// Bundle implements Bundles: the named bundle of the named module with every
// include expanded, another module's read from that module's own contract and
// granted through the includer. Includes that come back to a bundle across
// modules are refused naming the chain.
func (publishers Publishers) Bundle(module, name string) ([]CeilingScope, error) {
	return publishers.bundle(module, name, nil)
}

func (publishers Publishers) bundle(module, name string, path []string) ([]CeilingScope, error) {
	at := module + "/" + name
	if slices.Contains(path, at) {
		return nil, &bundleError{sentinel: ErrInvalid, reason: fmt.Sprintf("bundle %s includes itself across modules (%s)", at, strings.Join(append(path[slices.Index(path, at):], at), " -> "))}
	}
	contract, err := publishers(module)
	if err != nil {
		return nil, &bundleError{sentinel: ErrUnresolvedInclude, reason: err.Error()}
	}
	if contract == nil {
		return nil, &bundleError{sentinel: ErrUnresolvedInclude, reason: "no contract was supplied for module " + module}
	}
	return contract.expandBundle(name, func(include BundleInclude) ([]CeilingScope, error) {
		return publishers.bundle(include.Module, include.Bundle, append(slices.Clone(path), at))
	})
}

// bundleError is a refusal of a bundle under one sentinel, its reason kept
// apart so that a refusal met while following an include is told as one
// sentence naming the whole chain.
type bundleError struct {
	sentinel error
	reason   string
}

func (e *bundleError) Error() string { return e.sentinel.Error() + ": " + e.reason }
func (e *bundleError) Unwrap() error { return e.sentinel }

// refusal is err as a bundle refusal, so that where it was met can be named
// before its reason: a bundle refusal as it is, a rule's refusal under its
// sentinel, any other error (a publisher that cannot be read) whole.
func refusal(err error) *bundleError {
	var refused *bundleError
	if errors.As(err, &refused) {
		return refused
	}
	for _, sentinel := range []error{ErrInvalid, ErrUnresolvedInclude} {
		if errors.Is(err, sentinel) {
			return &bundleError{sentinel: sentinel, reason: strings.TrimPrefix(err.Error(), sentinel.Error()+": ")}
		}
	}
	return &bundleError{sentinel: ErrUnresolvedInclude, reason: err.Error()}
}

// Bundle is the scopes the named bundle grants with every bundle of this
// module it includes expanded, in one deterministic order: each kind once per
// place it is reached (through), its actions sorted, the scopes sorted by
// kind then through. A bundle the module does not publish, or one that
// includes itself through any chain, is refused by name. So is one that
// includes another module's bundle: that is read from the contract the other
// module publishes, which one contract alone does not hold (Publishers).
func (contract *Contract) Bundle(name string) ([]CeilingScope, error) {
	return contract.expandBundle(name, func(include BundleInclude) ([]CeilingScope, error) {
		return nil, &bundleError{sentinel: ErrUnresolvedInclude, reason: "another module's bundle is read from the contract that module publishes, and this bundle was read from " + contract.Principal + "'s contract alone; read it through the published contracts (Publishers)"}
	})
}

// expandBundle is the named bundle with its includes expanded: those of this
// module from this contract, another module's through foreign and granted
// through the includer. A bundle grants each permission once, held after
// expansion: one a bundle spells that one of its includes also grants is
// refused naming both.
func (contract *Contract) expandBundle(name string, foreign func(BundleInclude) ([]CeilingScope, error)) ([]CeilingScope, error) {
	byName := map[string]Bundle{}
	published := make([]string, 0, len(contract.Bundles))
	for _, bundle := range contract.Bundles {
		byName[bundle.Name] = bundle
		published = append(published, bundle.Name)
	}
	if _, ok := byName[name]; !ok {
		reason := fmt.Sprintf("%s publishes no bundle %q", contract.Principal, name)
		if len(published) > 0 {
			reason += " (it publishes " + strings.Join(published, ", ") + ")"
		} else {
			reason += " (it publishes none)"
		}
		return nil, &bundleError{sentinel: ErrUnresolvedInclude, reason: reason}
	}
	if err := errors.Join(checkBundleIncludeNames(contract), checkBundleIncludesAnotherModule(contract), checkBundleIncludesThroughPublisher(contract), checkBundleIncludes(contract), checkBundlesAcyclic(contract)); err != nil {
		return nil, err
	}
	var expand func(name string) ([]CeilingScope, error)
	expand = func(name string) ([]CeilingScope, error) {
		bundle := byName[name]
		var included []CeilingScope
		for _, include := range bundle.Includes {
			label := fmt.Sprintf("%s bundle %q includes %s", contract.Principal, bundle.Name, include)
			var scopes []CeilingScope
			var err error
			if include.Module == "" {
				scopes, err = expand(include.Bundle)
				if err != nil {
					return nil, err
				}
			} else {
				scopes, err = foreign(include)
				if err != nil {
					met := refusal(err)
					return nil, &bundleError{sentinel: met.sentinel, reason: label + ": " + met.reason}
				}
				// Reached through the includer and nowhere else: the one
				// place the spelled form names.
				scopes = slices.Clone(scopes)
				for i := range scopes {
					scopes[i].Through = include.Through
				}
				scopes = mergeScopes(nil, scopes)
			}
			for _, scope := range scopes {
				if twice := grantedBy(bundle.Scopes, scope); len(twice) > 0 {
					return nil, &bundleError{sentinel: ErrUnresolvedInclude, reason: fmt.Sprintf("%s, which grants %s:%s%s that the bundle also grants itself; a bundle grants each permission once, so it spells only what no bundle it includes grants",
						label, scope.ResourceKind, strings.Join(twice, ","), throughLabel(scope.Through))}
				}
			}
			included = append(included, scopes...)
		}
		return mergeScopes(bundle.Scopes, included), nil
	}
	return expand(name)
}

// throughLabel says where a scope is reached, for a refusal; nothing for one
// reached directly.
func throughLabel(through string) string {
	if through == "" {
		return ""
	}
	return " through " + through
}

// bundleCycle is the chain by which name includes itself through bundles of
// its own module, or nil.
func (contract *Contract) bundleCycle(name string, path []string) []string {
	if slices.Contains(path, name) {
		return append(path[slices.Index(path, name):], name)
	}
	for _, bundle := range contract.Bundles {
		if bundle.Name != name {
			continue
		}
		for _, included := range bundle.Includes {
			if included.Module != "" {
				continue // another module's: held across contracts (Publishers)
			}
			if cycle := contract.bundleCycle(included.Bundle, append(slices.Clone(path), name)); cycle != nil {
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
// a person causes to that module. A bundle of the unit's own is read from the
// contract itself, and what it reaches onward through the unit the unit's own
// role reaches directly. A role including nothing is unchanged. Every include
// that cannot be granted is reported, in declaration order.
func (contract *Contract) includedRoles(resolved *Resolved, delivered map[string][]int, bundles Bundles) ([]Role, []string) {
	roles := make([]Role, 0, len(contract.Roles))
	var problems []string
	const unread = "the contract was resolved with no bundle declarations to read it from"
	for _, role := range contract.Roles {
		if len(role.Includes) == 0 {
			roles = append(roles, role)
			continue
		}
		var granted []CeilingScope
		for _, include := range role.Includes {
			label := fmt.Sprintf("role %s includes %s/%s", role.Name, include.Module, include.Bundle)
			own := include.Module == contract.Principal
			var scopes []CeilingScope
			var err error
			switch {
			case own:
				// The unit publishes this bundle: it is read from the contract
				// in hand, and only what it includes of another module's is
				// read from that module.
				scopes, err = contract.expandBundle(include.Bundle, func(foreign BundleInclude) ([]CeilingScope, error) {
					if bundles == nil {
						return nil, &bundleError{sentinel: ErrUnresolvedInclude, reason: unread}
					}
					return bundles.Bundle(foreign.Module, foreign.Bundle)
				})
			case bundles == nil:
				problems = append(problems, label+", and "+unread)
				continue
			default:
				scopes, err = bundles.Bundle(include.Module, include.Bundle)
			}
			if err != nil {
				problems = append(problems, label+": "+refusal(err).reason)
				continue
			}
			for _, scope := range scopes {
				switch {
				case scope.Through == "":
				case own && scope.Through == contract.Principal:
					// The bundle reaches this kind onward through the unit,
					// for another unit's screen. The unit's own page is the
					// unit: it reaches the kind directly, as its own scope.
					scope.Through = ""
				default:
					binding, reason := contract.personBindingTo(scope.Through, resolved, delivered)
					if reason != "" {
						problems = append(problems, label+", which reaches "+scope.ResourceKind+" through "+scope.Through+": "+reason)
						continue
					}
					scope.Through = binding
				}
				// One source per permission, held after expansion: a role
				// grants what an include grants it once, so the role's own
				// scopes are its page's own calls alone, never a copy of a
				// bundle that drifts from it.
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
// a person causes, delivered to audience; or why there is not exactly one,
// naming the audiences the unit does declare such a binding to.
func (contract *Contract) personBindingTo(audience string, resolved *Resolved, delivered map[string][]int) (string, string) {
	var found, reached []string
	for _, binding := range contract.Bindings {
		if !slices.Contains(binding.Operations, OperationInvoke) && !slices.Contains(binding.Operations, OperationLookup) {
			continue
		}
		matched := false
		for _, index := range delivered[binding.ID] {
			reached = append(reached, resolved.Bindings[index].Audience)
			matched = matched || resolved.Bindings[index].Audience == audience
		}
		if matched {
			found = append(found, binding.ID)
		}
	}
	switch len(found) {
	case 1:
		return found[0], ""
	case 0:
		slices.Sort(reached)
		reached = slices.Compact(reached)
		held := "it declares none to any module"
		if len(reached) > 0 {
			held = "those it declares are to " + strings.Join(reached, ", ")
		}
		return "", "the unit declares no binding a person causes to " + audience + " (an invoke or lookup binding whose audience resolves to " + audience + "; " + held + ")"
	}
	return "", "the unit declares " + strings.Join(found, " and ") + " to " + audience + "; a bundle reaches through one"
}

func includesError(problems []string) error {
	return fmt.Errorf("%w: %s; a bundle is read from the module that publishes it", ErrUnresolvedInclude, strings.Join(problems, "; "))
}
