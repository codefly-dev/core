package resources

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"gopkg.in/yaml.v3"
)

// EndpointDeclaration is the export-relevant part of an endpoint's declaration,
// judged whole by ValidateEndpointDeclaration before any consumer is: who may
// reach it (Visibility), where it lives (Location), whether it is addressed
// from outside the workspace (Exposure), what the author wrote that the model
// refuses to read (AllowModules), and what the wire carried that the schema
// does not define (UnknownWireFields).
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
	// AllowModules is what a declaration built in memory authored. It is not
	// a grant and is never read as one: the allow-list is derived from the
	// consumers' declared dependencies. A non-nil value is refused; the YAML
	// key is refused by presence before it is decoded, and the wire field by
	// number before it is projected.
	AllowModules []string
	// UnknownWireFields are the field numbers a proto endpoint carried that
	// its schema does not define — the reserved allow_modules (9) among them.
	// Protobuf keeps such bytes as unknown fields rather than refusing them,
	// so an authored allow-list could otherwise ride in unseen; the
	// declaration carries their numbers so the rule can refuse them.
	UnknownWireFields []protowire.Number
}

// An endpointRule is one refusal the endpoint model makes, named: a
// conformance fixture says which rule refuses it, and the kit's self-check
// deletes each rule in turn and proves at least one fixture notices. The rules
// run in a fixed order and the first refusal is returned, so a declaration is
// refused for one reason, named — the same reason whichever path judged it:
// the YAML decoder, the proto ingress, the loader, the endpoint selection, the
// dependency verdict, the network allocation.
//
// A rule judges one of two inputs. A decoder rule (keys) judges the keys of an
// endpoint mapping BEFORE the mapping is decoded, because decoding loses what
// it refuses: a non-strict decoder drops an unknown key, and a null value and
// an absent key decode to the same nil. A declaration rule (check) judges the
// decoded declaration, from YAML, from the wire or built in memory.
type endpointRule struct {
	name string
	// keys judges an endpoint mapping's keys before decoding; nil for a
	// declaration rule.
	keys func(keys []string) error
	// check judges a decoded declaration; nil for a decoder rule.
	check func(declaration EndpointDeclaration) error
	// witness is the condition stated as data beside the check — a
	// declaration this rule refuses and no earlier rule does — so a rule
	// cannot be written without the input that falsifies it. A decoder rule
	// states its witness as keys instead (witnessKeys).
	witness     EndpointDeclaration
	witnessKeys []string
	// message is the text the witness's refusal carries.
	message string
}

// The rules, by name. A refused fixture names one of these; a reader that
// drops one fails the kit on that fixture.
const (
	// ruleEndpointKeysKnown: an endpoint mapping carries only the keys the
	// model defines. A misspelt key would otherwise decode to nothing, and a
	// declaration the author wrote would be read as its absence.
	ruleEndpointKeysKnown = "endpoint-keys-known"
	// ruleAllowModulesDerived: an allow-list is never authored on an endpoint,
	// in any spelling, with any value — the wildcard, the empty list and null
	// included — because the target does not name its consumers. Judged by
	// the key's presence before decoding, and by the value's presence on a
	// declaration built in memory.
	ruleAllowModulesDerived = "allow-modules-derived"
	// ruleWireFieldsKnown: an endpoint read from the wire carries only the
	// fields its schema defines; the reserved allow_modules field is refused
	// by number, not kept as unknown bytes.
	ruleWireFieldsKnown = "wire-fields-known"
	// ruleVisibilityKnown: a visibility is one of the three the model
	// defines, or none. The former "module" and "external" are refused.
	ruleVisibilityKnown = "visibility-known"
	// ruleLocationKnown: a location is "external", or none.
	ruleLocationKnown = "location-known"
	// ruleExposureKnown: an exposure is "public", "none", or omitted.
	ruleExposureKnown = "exposure-known"
	// ruleExposureDeclared: a public endpoint states its exposure — "public"
	// or "none" — rather than omitting it, so a manifest written when
	// `visibility: public` meant an outward address fails to load instead of
	// quietly losing it.
	ruleExposureDeclared = "exposure-declared"
	// ruleExposureWithinReach: an address reachable from outside the
	// workspace is declared only on an endpoint reachable from outside it.
	ruleExposureWithinReach = "exposure-within-reach"
	// ruleExposureInSystem: an external endpoint is addressed where it
	// lives; the system allocates it nothing, so exposure is not declared
	// on it.
	ruleExposureInSystem = "exposure-in-system"
)

// endpointDeclarationRules is the order a declaration is held to: the keys it
// was written with before anything is decoded, then the wire before anything
// is projected, then each value against its vocabulary, then the two axes
// against each other — so an unknown spelling is named before a contradiction
// between known ones.
func endpointDeclarationRules() []endpointRule {
	return []endpointRule{
		{name: ruleEndpointKeysKnown, keys: keysEndpointKnown,
			witnessKeys: []string{"name", "api", "visibilty"},
			message:     `declares unknown key "visibilty"`},
		{name: ruleAllowModulesDerived, keys: keysAllowModulesDerived, check: checkAllowModulesDerived,
			witnessKeys: []string{"name", "api", "allow-modules"},
			witness:     EndpointDeclaration{Service: "alpha", Name: "grpc", Visibility: VisibilityInternal, AllowModules: []string{"*"}},
			message:     "authors allow-modules"},
		{name: ruleWireFieldsKnown, check: checkWireFieldsKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "grpc", Visibility: VisibilityInternal, UnknownWireFields: []protowire.Number{9}},
			message: "carries unknown wire fields [9]"},
		{name: ruleVisibilityKnown, check: checkVisibilityKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "grpc", Visibility: "module"},
			message: `unsupported visibility "module"`},
		{name: ruleLocationKnown, check: checkLocationKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "tcp", Location: "nowhere"},
			message: `unsupported location "nowhere"`},
		{name: ruleExposureKnown, check: checkExposureKnown,
			witness: EndpointDeclaration{Service: "alpha", Name: "http", Visibility: VisibilityPublic, Exposure: "ingress"},
			message: `unsupported exposure "ingress"`},
		{name: ruleExposureDeclared, check: checkExposureDeclared,
			witness: EndpointDeclaration{Service: "alpha", Name: "http", Visibility: VisibilityPublic},
			message: `declares visibility "public" but states no exposure`},
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
// declaration on its own, before any consumer is, with every declaration rule
// in endpointDeclarationRules. Every error wraps ErrInvalidEndpointDeclaration
// and names the endpoint and the rule's reason.
func ValidateEndpointDeclaration(declaration EndpointDeclaration) error {
	return validateEndpointDeclaration(declaration, "")
}

// validateEndpointDeclaration runs every declaration rule but the one named,
// in order; "" deletes none. The self-check is its only caller with a name.
func validateEndpointDeclaration(declaration EndpointDeclaration, deleted string) error {
	for _, rule := range endpointDeclarationRules() {
		if rule.check == nil || rule.name == deleted {
			continue
		}
		if err := rule.check(declaration); err != nil {
			return fmt.Errorf("%w: endpoint %s/%s %w", ErrInvalidEndpointDeclaration, declaration.Service, declaration.Name, err)
		}
	}
	return nil
}

// validateEndpointKeys runs every decoder rule but the one named over the keys
// of an endpoint mapping, in order, before the mapping is decoded; "" deletes
// none. name is the endpoint's own name as written, for the refusal.
func validateEndpointKeys(name string, keys []string, deleted string) error {
	for _, rule := range endpointDeclarationRules() {
		if rule.keys == nil || rule.name == deleted {
			continue
		}
		if err := rule.keys(keys); err != nil {
			return fmt.Errorf("%w: endpoint %q %w", ErrInvalidEndpointDeclaration, name, err)
		}
	}
	return nil
}

// endpointKeys are the keys an endpoint mapping may carry: the YAML names of
// Endpoint's fields, read from the type so the list cannot drift from it.
var endpointKeys = yamlKeysOf(Endpoint{})

// yamlKeysOf lists the yaml keys a struct declares, in field order, read from
// its tags the way the decoder reads them (a tag of "-" is no key, an untagged
// exported field is its lowercased name); it is what makes a key vocabulary
// the type's own rather than a list beside it.
func yamlKeysOf(value any) []string {
	var keys []string
	typed := reflect.TypeOf(value)
	for i := 0; i < typed.NumField(); i++ {
		field := typed.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = strings.ToLower(field.Name)
		}
		keys = append(keys, name)
	}
	return keys
}

// canonicalKey is a key's identity across spellings — `allow-modules`,
// `allow_modules`, `allowModules`, `Allow Modules` are one key — so a
// forbidden key cannot be smuggled past the rule by respelling it into one the
// decoder ignores.
func canonicalKey(key string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(key) {
		if r >= 'a' && r <= 'z' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

const allowModulesKeyCanonical = "allowmodules"

func keysEndpointKnown(keys []string) error {
	for _, key := range keys {
		if canonicalKey(key) == allowModulesKeyCanonical || slices.Contains(endpointKeys, key) {
			continue
		}
		return fmt.Errorf("declares unknown key %q (an endpoint declares %s)", key, strings.Join(endpointKeys, ", "))
	}
	return nil
}

func keysAllowModulesDerived(keys []string) error {
	for _, key := range keys {
		if canonicalKey(key) != allowModulesKeyCanonical {
			continue
		}
		return fmt.Errorf("authors allow-modules (key %q): an allow-list is derived from the consumers' declared service dependencies, never written on the endpoint it would grant — a module asks for what it consumes, and the target names nobody", key)
	}
	return nil
}

func checkAllowModulesDerived(declaration EndpointDeclaration) error {
	// Presence, not length: an empty list is still a list the target wrote.
	if declaration.AllowModules == nil {
		return nil
	}
	return fmt.Errorf("authors allow-modules %q: an allow-list is derived from the consumers' declared service dependencies, never written on the endpoint it would grant — a module asks for what it consumes, and the target names nobody",
		declaration.AllowModules)
}

func checkWireFieldsKnown(declaration EndpointDeclaration) error {
	if len(declaration.UnknownWireFields) == 0 {
		return nil
	}
	return fmt.Errorf("carries unknown wire fields %v: the schema defines no such field, and allow_modules (9) — an authored allow-list — is reserved, not read",
		declaration.UnknownWireFields)
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
	return fmt.Errorf("declares unsupported exposure %q (one of %q, %q, or omitted)", declaration.Exposure, ExposurePublic, ExposureNone)
}

func checkExposureDeclared(declaration EndpointDeclaration) error {
	if declaration.Visibility != VisibilityPublic || declaration.Exposure != "" {
		return nil
	}
	return fmt.Errorf("declares visibility %q but states no exposure: write exposure %q for an address reachable from outside the workspace, or %q for none — a public endpoint never omits it, so a manifest written when public meant an address fails here instead of losing it",
		VisibilityPublic, ExposurePublic, ExposureNone)
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

// mappingKeys lists the keys of a YAML mapping node, in document order, and
// the value of its "name" key for a refusal to name.
func mappingKeys(node *yaml.Node) (keys []string, name string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		keys = append(keys, key)
		if key == "name" {
			name = node.Content[i+1].Value
		}
	}
	return keys, name
}

// UnknownWireFields lists the field numbers a proto message carried that its
// schema does not define, as protobuf kept them (ProtoReflect().GetUnknown()):
// in wire order, each once. Bytes that are not a field at all are reported as
// field 0, which no schema defines either.
func UnknownWireFields(raw []byte) []protowire.Number {
	var numbers []protowire.Number
	for len(raw) > 0 {
		number, kind, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return append(numbers, 0)
		}
		raw = raw[n:]
		if !slices.Contains(numbers, number) {
			numbers = append(numbers, number)
		}
		m := protowire.ConsumeFieldValue(number, kind, raw)
		if m < 0 {
			return append(numbers, 0)
		}
		raw = raw[m:]
	}
	return numbers
}
