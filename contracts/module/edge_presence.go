package module

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// An ENVIRONMENT CLASS is the release stage of the environment a contract is
// delivered to: how far along the promotion path it sits. It is not a new
// axis. It is the one a coordinate already names — the `development` segment
// of `hosted/gcp/us-east1/US/development/blue` — and the deliverer states it
// per environment, beside the host it delivers to.
//
// It is DECLARED and never derived. An environment's name is not its class:
// `blue`, `green` and `dev` are all development cells, `local` is one too,
// and a class read off a name is the defect that `Local()` already is —
// "it starts with local, so it must be local". A class derived from a name
// would quietly make a production environment development the day someone
// named one `prod-2`.
//
// A class is NOT a protection floor. What a cell must protect follows from
// whether it holds customer data, which is a separate declaration; this axis
// decides presence, sizing and promotion.
const (
	// ClassDevelopment is a cell on the near side of the promotion path: a
	// developer's own run, and every internal cell a change is proven on.
	ClassDevelopment = "development"
	// ClassStaging is the rehearsal of production, with production's shape.
	ClassStaging = "staging"
	// ClassProduction is the far side: what people outside the team use.
	ClassProduction = "production"
)

// The two presences an edge dependency declares, in either spelling.
const (
	// PresenceRequired means the edge must resolve to an installed module.
	PresenceRequired = "required"
	// PresenceOptional means it need not.
	PresenceOptional = "optional"
)

// EnvironmentClasses is the vocabulary, in promotion order. Core owns it
// because core refuses a class outside it, and it is listed in every refusal
// so a deliverer reads the spelling rather than guessing it.
func EnvironmentClasses() []string {
	return []string{ClassDevelopment, ClassStaging, ClassProduction}
}

// KnownEnvironmentClass reports whether class is one of EnvironmentClasses.
// The empty string is not one: an environment that declares no class is a
// different case from one that declares a class core does not know, and the
// two are refused separately, each naming what it saw.
func KnownEnvironmentClass(class string) bool {
	return slices.Contains(EnvironmentClasses(), class)
}

// perClass reports whether this edge is written in the per-class spelling.
func (dependency EdgeDependency) perClass() bool { return len(dependency.PresenceByClass) > 0 }

// declaredClasses lists the classes the per-class spelling names, sorted, for
// a refusal to quote.
func (dependency EdgeDependency) declaredClasses() string {
	return strings.Join(slices.Sorted(maps.Keys(dependency.PresenceByClass)), ", ")
}

// PresenceIn is this edge's presence in an environment of class class: the one
// value a delivered manifest carries, so a host reads a presence and never an
// environment class.
//
// The plain spelling answers whatever the class is, including none: a contract
// that says `presence: required` says it everywhere, and no environment has to
// declare anything for it to resolve. The per-class spelling answers only the
// classes it names. Every other class is REFUSED, by name, rather than
// defaulted: a default would be the thing this field exists to prevent —
// shipping a production cell without a module it genuinely needs because
// someone wrote the development answer and nothing asked about the other side.
func (dependency EdgeDependency) PresenceIn(class string) (string, error) {
	if !dependency.perClass() {
		return dependency.Presence, nil
	}
	if class == "" {
		return "", fmt.Errorf("%w: edge_dependencies module %q declares presence per environment class (%s) and this environment declares no class; declare the class of the environment being delivered to (%s) — a class is declared, never read off an environment's name",
			ErrInvalid, dependency.Module, dependency.declaredClasses(), strings.Join(EnvironmentClasses(), ", "))
	}
	if presence, named := dependency.PresenceByClass[class]; named {
		return presence, nil
	}
	return "", fmt.Errorf("%w: edge_dependencies module %q declares presence for the environment classes %s and this environment's class is %q, which it does not name; declare %s in presence_by_class — an unnamed class is refused rather than resolved to a default",
		ErrInvalid, dependency.Module, dependency.declaredClasses(), class, class)
}

// ResolveEdgeDependencies is the ONE resolution of a contract's edge
// dependencies for the environment being delivered to: each entry's module
// unchanged, in declaration order, with its presence resolved to the single
// value the environment's class gives it.
//
// It is exported because TWO carriers state the same declaration to the same
// host — the signed module manifest's authority block and the signed authority
// document — and an agreement rule compares them. Resolving in one of them
// and carrying the declaration verbatim in the other would make every
// per-class contract disagree with itself at install, so both call this.
func (contract *Contract) ResolveEdgeDependencies(class string) ([]EdgeDependency, error) {
	if class != "" && !KnownEnvironmentClass(class) {
		return nil, fmt.Errorf("%w: the environment being delivered to declares class %q; an environment class is one of %s",
			ErrInvalid, class, strings.Join(EnvironmentClasses(), ", "))
	}
	resolved := slices.Clone(contract.EdgeDependencies)
	for index := range resolved {
		presence, err := resolved[index].PresenceIn(class)
		if err != nil {
			return nil, err
		}
		// Written out whole rather than patched: the resolved entry carries
		// the presence and NOTHING of the per-class declaration, so the
		// delivered manifest says one thing and a host reading it cannot find
		// a second answer beside it.
		resolved[index] = EdgeDependency{Module: resolved[index].Module, Presence: presence}
	}
	return resolved, nil
}
