package resources

import (
	"fmt"
)

// EndpointDeclaration is the export-relevant part of an endpoint's declaration,
// judged whole by ValidateEndpointDeclaration before any consumer is: who may
// reach it (Visibility), where it lives (Location), whether it is addressed
// from outside the workspace (Exposure), and what the author wrote that the
// model refuses to read (AllowModules).
type EndpointDeclaration struct {
	// Service and Name identify the endpoint in a refusal.
	Service string
	Name    string
	// Visibility is reach: private, internal or public (or none, read as
	// private).
	Visibility Visibility
	// Location is where the endpoint lives: external, or none (in-system).
	Location string
	// Exposure is addressing: public, or none (no outward address).
	Exposure string
	// AllowModules is what the declaration authored under allow-modules. It is
	// not a grant and is never read as one: the allow-list is derived from the
	// consumers' declared dependencies. A non-empty value is refused.
	AllowModules []string
}

// An endpointRule is one refusal ValidateEndpointDeclaration makes, named: a
// conformance fixture says which rule refuses it, and the kit's self-check
// deletes each rule in turn and proves at least one fixture notices. The rules
// run in a fixed order and the first refusal is returned, so a declaration is
// refused for one reason, named — the same reason whichever path judged it:
// the loader, the proto conversion, the endpoint selection, the dependency
// verdict.
type endpointRule struct {
	name  string
	check func(declaration EndpointDeclaration) error
	// witness is a declaration this rule refuses and no earlier rule does: the
	// condition stated as data, beside the check, so a rule cannot be written
	// without the input that falsifies it. The self-check proves the witness
	// is refused by this rule alone, with the message, and that a shipped
	// fixture carries the same condition for every reader.
	witness EndpointDeclaration
	// message is the text the witness's refusal carries.
	message string
}

// The rules, by name. A refused fixture names one of these; a reader that
// drops one fails the kit on that fixture.
const (
	// ruleVisibilityKnown: a visibility is one of the three the model
	// defines, or none. The former "module" and "external" are refused.
	ruleVisibilityKnown = "visibility-known"
	// ruleLocationKnown: a location is "external", or none.
	ruleLocationKnown = "location-known"
	// ruleExposureKnown: an exposure is "public", or none.
	ruleExposureKnown = "exposure-known"
	// ruleAllowModulesDerived: an allow-list is never authored on an endpoint,
	// the wildcard included — the target does not name its consumers.
	ruleAllowModulesDerived = "allow-modules-derived"
	// ruleExposureWithinReach: an address reachable from outside the
	// workspace is declared only on an endpoint reachable from outside it.
	ruleExposureWithinReach = "exposure-within-reach"
	// ruleExposureInSystem: an external endpoint is addressed where it
	// lives; the system allocates it nothing, so exposure is not declared
	// on it.
	ruleExposureInSystem = "exposure-in-system"
)

// endpointDeclarationRules is the order a declaration is held to: each value
// against its vocabulary first, then the field the model refuses to read, then
// the two axes against each other — so an unknown spelling is named before a
// contradiction between known ones.
func endpointDeclarationRules() []endpointRule {
	return []endpointRule{
		{name: ruleVisibilityKnown, check: checkVisibilityKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "grpc", Visibility: "module"},
			message: `unsupported visibility "module"`},
		{name: ruleLocationKnown, check: checkLocationKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "tcp", Location: "nowhere"},
			message: `unsupported location "nowhere"`},
		{name: ruleExposureKnown, check: checkExposureKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "http", Visibility: VisibilityPublic, Exposure: "ingress"},
			message: `unsupported exposure "ingress"`},
		{name: ruleAllowModulesDerived, check: checkAllowModulesDerived,
			witness: EndpointDeclaration{Service: "alpha", Name: "grpc", Visibility: VisibilityInternal, AllowModules: []string{"*"}},
			message: `authors allow-modules ["*"]`},
		{name: ruleExposureWithinReach, check: checkExposureWithinReach,
			witness: EndpointDeclaration{Service: "alpha", Name: "http", Visibility: VisibilityInternal, Exposure: ExposurePublic},
			message: `exposure "public" with visibility "internal"`},
		{name: ruleExposureInSystem, check: checkExposureInSystem,
			witness: EndpointDeclaration{Service: "alpha", Name: "feed", Visibility: VisibilityPublic, Location: LocationExternal, Exposure: ExposurePublic},
			message: `exposure "public" with location "external"`},
	}
}

// endpointDeclarationRuleNames lists every rule, in order.
func endpointDeclarationRuleNames() []string {
	all := endpointDeclarationRules()
	names := make([]string, 0, len(all))
	for _, rule := range all {
		names = append(names, rule.name)
	}
	return names
}

// ValidateEndpointDeclaration judges the export-relevant part of an endpoint's
// declaration on its own, before any consumer is, with every rule in
// endpointDeclarationRules. Every error wraps ErrInvalidEndpointDeclaration and
// names the endpoint and the rule's reason.
func ValidateEndpointDeclaration(declaration EndpointDeclaration) error {
	return validateEndpointDeclaration(declaration, "")
}

// validateEndpointDeclaration runs every rule but the one named, in order; ""
// deletes none. The self-check is its only caller with a name.
func validateEndpointDeclaration(declaration EndpointDeclaration, deleted string) error {
	for _, rule := range endpointDeclarationRules() {
		if rule.name == deleted {
			continue
		}
		if err := rule.check(declaration); err != nil {
			return fmt.Errorf("%w: endpoint %s/%s %w", ErrInvalidEndpointDeclaration, declaration.Service, declaration.Name, err)
		}
	}
	return nil
}

func checkVisibilityKnown(declaration EndpointDeclaration) error {
	if KnownVisibility(declaration.Visibility) {
		return nil
	}
	return fmt.Errorf("declares unsupported visibility %q (one of %q, %q, %q)",
		declaration.Visibility, VisibilityPrivate, VisibilityInternal, VisibilityPublic)
}

func checkLocationKnown(declaration EndpointDeclaration) error {
	if KnownLocation(declaration.Location) {
		return nil
	}
	return fmt.Errorf("declares unsupported location %q (only %q, or none)", declaration.Location, LocationExternal)
}

func checkExposureKnown(declaration EndpointDeclaration) error {
	if KnownExposure(declaration.Exposure) {
		return nil
	}
	return fmt.Errorf("declares unsupported exposure %q (only %q, or none)", declaration.Exposure, ExposurePublic)
}

func checkAllowModulesDerived(declaration EndpointDeclaration) error {
	// Presence, not length: an empty list is still the key written on the
	// target, and the loader decodes `allow-modules: []` as an empty list.
	if declaration.AllowModules == nil {
		return nil
	}
	return fmt.Errorf("authors allow-modules %q: an allow-list is derived from the consumers' declared service dependencies, never written on the endpoint it would grant — a module asks for what it consumes, and the target names nobody",
		declaration.AllowModules)
}

func checkExposureWithinReach(declaration EndpointDeclaration) error {
	if declaration.Exposure != ExposurePublic || declaration.Visibility == VisibilityPublic {
		return nil
	}
	return fmt.Errorf("declares exposure %q with visibility %q: an address reachable from outside the workspace belongs to an endpoint reachable from outside it (visibility %q)",
		declaration.Exposure, declaration.Visibility, VisibilityPublic)
}

func checkExposureInSystem(declaration EndpointDeclaration) error {
	if declaration.Exposure != ExposurePublic || declaration.Location != LocationExternal {
		return nil
	}
	return fmt.Errorf("declares exposure %q with location %q: an external endpoint is addressed where it lives, and the system allocates it no address",
		declaration.Exposure, declaration.Location)
}
