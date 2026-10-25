// Package module is the ONE implementation of the module contract —
// codefly/module-contract/v1, the file a module publishes beside its manifest
// as module.contract.codefly.yaml: its Go model, its strict decoder, every
// refusal rule, and the resolution of its slots against a composition. The
// CLI renders a module manifest's authority from it, the runtimes publish it, and the
// host's loader reads it: all three read it THROUGH this package, and the
// fixtures it ships (Fixtures) are what each of them is driven through, so a
// decoder that started accepting an invalid ceiling would fail here first and
// everywhere at once. Nothing outside this package may parse or validate a
// module contract. Consumers import it as modulecontract, so the identifier
// says what it is; the docs spell it that way too.
//
// It belongs to the lifecycle solutionhost owns, because the contract is the
// request a module manifest carries: the CLI resolves it into the manifest's
// authority block, which is signed and delivered to the host, and the host
// grants its intersection with the administrator's approval.
//
// The contract is a REQUEST, not a grant. It says what the module asks to be
// able to do on a host: the principal its credentials are issued to, the
// operation bindings it redeems, the queues and namespaces it declares, the
// scope ceilings it contributes, and the destinations it exposes. Records
// narrow it — the signed manifest the render derives from this file, the
// approval a platform administrator writes, and an organisation's
// installation — so
// nothing here is wider than what the module exercises, and nothing here grants
// anything by itself.
//
// Four things never appear in a contract, because they are not the module's to
// assert: tenancy and cross-tenant reach (the envelope is the only record that
// may name an organisation), a build digest (no runtime record approves a
// build), and the expected workload identity (the platform's: it issues SVIDs
// and owns the trust domain). The
// decoder is strict, so a contract that tries to carry one is refused rather
// than silently honoured.
//
// # Rules
//
// Every refusal is one named rule (rules.go), applied in a fixed order, so a
// document is refused for one reason whichever reader refused it. Each rule
// is protected by a fixture the kit ships, and the package's self-check
// deletes each rule in turn and proves a fixture notices: a rule that could
// be dropped silently is a rule the kit does not protect.
//
// # Slots
//
// A binding's audience and the resource kind its scopes name are expressed in
// ANOTHER module's vocabulary, which a module repository must not spell. They
// are declared as slots — {from: <group>/<key>} — pointing at a workspace
// configuration value the composition supplies, and the render resolves them
// when it derives the manifest's authority. A slot resolves PUBLIC configuration
// only: an audience and a resource kind are names, and a render carries secrets
// only as references, so a slot that resolves to a secret-classified value fails
// the derivation rather than inlining it into a delivered document. A bare
// string where a slot belongs is a schema error, not a value: a literal audience
// written into a module repository is a coupling across a module boundary that
// should never be accepted silently.
//
// A slot's KEY NAME carries its meaning, because no reader can check what a
// resolved value means: an audience slot pointing at a declared, supplied key
// that holds a model profile name resolves cleanly to a binding addressed to a
// profile, which the receiver refuses far from the contract that caused it. So
// the reader holds the convention the modules publish against: audience takes
// a *-audience key (or a *-prefix one, since a module's prefix is the audience
// a capability for it is addressed to), resource_kind a *-resource-kind key and
// binding_key a *-binding key, in either spelling core accepts.
//
// # The module itself
//
// One audience is not another module's vocabulary: the module's own. A module
// that admits work into itself on someone's behalf — a scheduler issued the
// capability an occurrence is admitted under — redeems a binding addressed to
// its own principal, and that principal is already the contract's (Principal),
// held by every renderer to the name the module is composed under. So the
// audience slot alone may say so, as {self: true}, and resolves to Principal:
// a configured value carrying the module's own name would be a second
// statement of an identity the composition already resolves, free to drift
// from it. Self names an audience and nothing else — a resource kind, a list
// or a binding key is never the module's principal — and a binding addressed
// to itself asks for its own vocabulary, so it names its kinds literally,
// each within the module's own scope_ceilings, and declares no resource_kind
// slot. It is not a slot the composition fills, so it neither fills nor
// leaves unfilled an optional binding: such a binding is omitted by its other
// slots, and one with none could never be left out, which is refused.
//
// # Audience lists
//
// A generic module reaches whichever modules a composition wires to it — a
// store of comments reaches the owner of each kind of thing commented on —
// so the audiences of one of its bindings are a LIST only the composition can
// write. Such a binding declares `audiences` in place of `audience`: a slot
// whose key is a *-audiences one and whose value is comma-separated names
// (robin,documents). It means "this binding may be exercised toward any of
// these audiences", and the resolution delivers it as one binding per listed
// audience, each its own unit of authority at the issuer: id
// <binding>:<audience> (AudienceBindingID), the audience, and the ceiling
// resolved for THAT audience — the calls it names read from that audience's
// own declarations, so two owners declaring one protocol's methods differently
// get two different ceilings, and a call one of them does not serve is refused
// by name. A bare-action ceiling is qualified per audience by resource_kinds
// (a *-resource-kinds key, <audience>=<kind>,...), because each audience's
// kinds are its own vocabulary: one resource_kind for many audiences would
// name one module's kind at another, and one binding_key would install N
// bindings under one key, so neither is written beside audiences.
//
// # Scope ceilings
//
// A binding's ceiling is written per operation in one of two spellings. Bare
// actions ([read, write]) are qualified by the binding's resource_kind slot,
// and a binding declaring none cannot write them: a scope names a resource
// kind, and one without a kind is a request nothing can mint. A binding whose
// acts span several kinds spells each scope out instead —
// [{resource_kind: other.things, actions: [write]}] — naming the kind
// literally, because a permission's namespace is fixed by the module that
// contributes it, exactly as its proto package is, while a service the
// composition chooses is configuration and stays a slot. The model writes a
// ceiling back in the spelling it holds (MarshalYAML), so a publisher using it
// emits a contract this reader reads.
package module

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/internal/wire"
)

const (
	// FileName is the contract's path relative to the module directory, beside
	// module.codefly.yaml and module.package.codefly.yaml. It is "module."-
	// prefixed because "contract" alone already means an API contract in this
	// ecosystem (contracts/api/catalog.codefly.json).
	FileName = "module.contract.codefly.yaml"

	// SchemaV1 is the only contract schema this package reads. It names the
	// thing, never the design note that argued for it: the note will be
	// superseded, the file outlives it, and the schema string is the one part
	// that cannot change without a version step.
	SchemaV1 = "codefly/module-contract/v1"
)

// Operations a binding may declare. invoke is a person's own capability,
// attenuated from a parent context the person caused to be minted; lookup is its
// read-only half; headless is the module's own capability with no person in it.
const (
	OperationInvoke   = "invoke"
	OperationLookup   = "lookup"
	OperationHeadless = "headless"
)

var operations = []string{OperationInvoke, OperationLookup, OperationHeadless}

// The slot fields of a binding, as the file spells them: what a refusal names
// and what the key-naming convention is keyed by.
const (
	fieldAudience      = "audience"
	fieldAudiences     = "audiences"
	fieldResourceKind  = "resource_kind"
	fieldResourceKinds = "resource_kinds"
	fieldBindingKey    = "binding_key"
	fieldActions       = "actions"
	fieldFrom          = "from"
	fieldSelf          = "self"
)

// audienceBindingSeparator joins a binding declaring an audience list to one
// audience it lists. It is outside the binding-id grammar (namePattern), so a
// derived id never collides with a binding a contract declares, and inside the
// opaque-id grammar a delivered manifest holds a binding id to.
const audienceBindingSeparator = ":"

// AudienceBindingID is the id a binding that declares an audience list is
// delivered under for one audience it lists: the operation binding a module
// names when it is issued a context toward that audience.
func AudienceBindingID(binding, audience string) string {
	return binding + audienceBindingSeparator + audience
}

// Destination kinds. The vocabulary belongs with the host's envelope table;
// these three are what is written until that table names it.
const (
	DestinationModule           = "module"
	DestinationHost             = "host"
	DestinationPlatformInternal = "platform-internal"
)

var destinationKinds = []string{DestinationModule, DestinationHost, DestinationPlatformInternal}

var (
	// ErrSchema means the file does not declare a schema this package reads: a
	// version skew, not a malformed contract.
	ErrSchema = errors.New("module contract schema is not supported")
	// ErrInvalid means the contract violates its own rules.
	ErrInvalid = errors.New("module contract is invalid")
	// ErrUnresolvedSlot means a slot points at a workspace configuration value
	// the composition does not supply for this environment.
	ErrUnresolvedSlot = errors.New("module contract slot cannot be resolved")
	// ErrSecretSlot means a slot resolved to a secret-classified value. A render
	// carries secrets only as references, so the value is refused rather than
	// written into a delivered document.
	ErrSecretSlot = errors.New("module contract slot resolves to a secret")
	// ErrAmbiguousSlot means a slot has no one value to resolve to: records
	// that are one key to core (MODEL_AUDIENCE and model-audience) supply
	// DIFFERENT values, or a value resolves to something a scope or an
	// audience cannot be built from. Two spellings agreeing are one value and
	// resolve; it is the disagreement that has no answer, and choosing between
	// them would make a delivered document depend on which record a reader
	// met first.
	ErrAmbiguousSlot = errors.New("module contract slot resolves ambiguously")
	// ErrUnresolvedCall means a binding names a call whose requirement cannot
	// be read: the contract was resolved with no callee declarations, or the
	// audience does not serve the call, or the call declares nothing.
	ErrUnresolvedCall = errors.New("module contract call cannot be resolved")
)

// Callees answers what a call to another module requires, from that module's
// own declarations: the scopes, each "<resource kind>:<action>", that the
// audience's method policy for the call names. A call the audience does not
// serve, or one that declares nothing, is an error naming it.
type Callees interface {
	Requires(audience, call string) ([]string, error)
}

// ResolveOption shapes one resolution.
type ResolveOption func(*resolveOptions)

type resolveOptions struct{ callees Callees }

// WithCallees resolves the bindings that name calls through callees: each such
// operation's ceiling is the union of what its calls require.
func WithCallees(callees Callees) ResolveOption {
	return func(o *resolveOptions) { o.callees = callees }
}

var (
	// namePattern is one lowercase dotted or dashed segment: a principal, a
	// binding ID, a queue, a namespace, a resource kind, a service or an
	// endpoint name.
	namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	// actionPattern is one action of a scope: lowercase, underscores allowed.
	actionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// slotKeyPattern is the key half of a slot: a workspace configuration key as
	// a composition writes it, in either spelling core accepts.
	slotKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// Contract is one module's published contract, as read from its file.
type Contract struct {
	// Schema is the document's version, checked first.
	Schema string `yaml:"schema"`
	// Principal is the principal every credential the module mints is issued
	// to: the module's own name.
	Principal string `yaml:"principal"`
	// Namespaces are the namespaces the module declares — the audit namespaces
	// it emits under, which an operator binds to its principal.
	Namespaces []string `yaml:"namespaces"`
	// Queues are the queues the module owns. Declared empty rather than
	// omitted: an absent list and "there are none" must not look the same.
	Queues []string `yaml:"queues"`
	// ScopeCeilings are the module's own permission vocabulary: the widest
	// authority any credential naming the module's resources may carry.
	ScopeCeilings []ScopeCeiling `yaml:"scope_ceilings"`
	// Roles are the roles a person may hold on an installation of a unit a
	// person uses — a solution — each a set of scopes: its own kinds within
	// ScopeCeilings, and the permissions at other modules its bindings ask for
	// on a person's behalf. One of them is InstallerRole. A bare module, whose
	// access is the organisation's roles, declares none.
	Roles []Role `yaml:"roles,omitempty"`
	// Bindings are the operation bindings the module redeems, outward.
	Bindings []Binding `yaml:"bindings"`
	// Destinations are the endpoints the module exposes for something else to
	// route to.
	Destinations []Destination `yaml:"destinations"`
	// ForeignMethods are the methods the module serves from a protocol whose
	// definition it does not own (an OpenTelemetry collector's Export), so no
	// option of its own can mark them, each with what a call must hold. They
	// join the methods its published contracts declare, so the hop in front of
	// the module decides them like any other.
	ForeignMethods []ForeignMethod `yaml:"foreign_methods,omitempty"`
	// RequireMethodPolicies holds every method the module publishes to an
	// explicit policy wherever the module is delivered: a method that names
	// neither requirements nor any checked caller is refused by name before
	// anything is rendered, even toward a host that does not consume method
	// policies. Absent, whether an unmarked method is refused before delivery
	// is the deliverer's call, made from what the host consumes; the hop in
	// front of the module refuses an undeclared method per call either way.
	RequireMethodPolicies bool `yaml:"require_method_policies,omitempty"`
}

// ForeignMethod is one served method of a protocol the module does not own,
// by its full gRPC name, and what a call to it must hold, in the module's own
// vocabulary.
type ForeignMethod struct {
	Method   string         `yaml:"method"`
	Requires []CeilingScope `yaml:"requires"`
}

// ScopeCeiling is one resource kind of the module's own vocabulary and the
// actions it contributes on it.
type ScopeCeiling struct {
	ResourceKind string   `yaml:"resource_kind"`
	Actions      []string `yaml:"actions"`
}

// Role is one role of a unit's own: its name and the scopes it grants, each a
// resource kind named literally with the actions granted on it.
type Role struct {
	Name   string         `yaml:"name"`
	Scopes []CeilingScope `yaml:"scopes"`
}

// InstallerRole is the role every unit that declares roles declares: whoever
// installs it holds it on that installation.
const InstallerRole = "installer"

// Binding is one operation binding the module asks to redeem.
type Binding struct {
	// ID is the module's own stable id for the binding.
	ID string `yaml:"id"`
	// Revision is the binding's revision, at least 1 and 1 when omitted. A
	// credential seals it and a verifier holding another one refuses, so a
	// module bumps it when the binding's meaning changes.
	Revision uint64 `yaml:"revision,omitempty"`
	// Operations are the operations the binding needs: invoke, lookup, headless.
	Operations []string `yaml:"operations"`
	// Lookup carries the lookup method when the binding declares lookup.
	Lookup *Lookup `yaml:"lookup,omitempty"`
	// Audience is the operation audience, a slot into the composition — or
	// {self: true}, the module itself, resolved to Principal.
	// A binding declares exactly one of Audience and Audiences.
	Audience Slot `yaml:"audience,omitempty"`
	// Audiences is the operation audiences, a slot whose value lists them
	// comma-separated: the binding may be exercised toward any of them, and
	// is resolved into one binding per listed audience (AudienceBindingID),
	// each with the ceiling that audience's own declarations give it.
	Audiences *Slot `yaml:"audiences,omitempty"`
	// ResourceKind is the other module's resource kind the scopes name, a
	// slot. Required by a ceiling written as bare actions, which it qualifies;
	// absent for a binding whose every ceiling spells its scopes out. Never
	// beside Audiences: each audience's kinds are its own.
	ResourceKind *Slot `yaml:"resource_kind,omitempty"`
	// ResourceKinds is, beside Audiences only, the kind that qualifies the
	// bare actions per listed audience: a slot whose value is
	// <audience>=<kind> pairs, comma-separated, one per listed audience.
	ResourceKinds *Slot `yaml:"resource_kinds,omitempty"`
	// BindingKey is the key the host installs the binding under, a slot.
	// Never beside Audiences: one key cannot install a binding per audience.
	BindingKey *Slot `yaml:"binding_key,omitempty"`
	// ScopeCeiling is, per declared operation, the scopes the binding may at
	// most carry. Every declared operation names its ceiling here or in Calls:
	// a binding declaring neither for an operation cannot be minted that way.
	ScopeCeiling map[string]Ceiling `yaml:"scope_ceiling,omitempty"`
	// Calls is, per declared operation, the audience's methods the binding
	// calls, each spelled as the audience's own method policy names it: a
	// gRPC method ("/<package>.<Service>/<Method>") or an HTTP route ("<VERB>
	// <path>"). The operation's ceiling is then what those calls require,
	// read from the audience's declarations when the contract is resolved
	// (WithCallees), so a module never names another module's resource kinds.
	// An operation takes its ceiling from ScopeCeiling or from Calls, never
	// both.
	Calls map[string][]string `yaml:"calls,omitempty"`
	// Optional marks a binding a composition may leave out. A composition
	// that fills NONE of its slots — every one absent, or supplied empty —
	// gets a resolution without it (Resolved.Omitted says so and why), so no
	// credential can ever be issued through it. A composition that fills any
	// slot resolves it whole, exactly as a mandatory binding: a slot left
	// unfilled beside a filled one is refused by name, because a misspelt key
	// must not silently drop a binding. Mandatory is the default.
	Optional bool `yaml:"optional,omitempty"`
}

// Ceiling is one operation's scope ceiling, in exactly one of two spellings:
// bare actions qualified by the binding's resource_kind slot, or explicit
// scopes each naming its resource kind literally. A sequence mixing the two is
// refused, so a reader never has to guess which kind an action was meant for.
type Ceiling struct {
	// Actions is the bare spelling: [read, write].
	Actions []string
	// Scopes is the explicit spelling: [{resource_kind: k, actions: [...]}].
	Scopes []CeilingScope

	// What the decoder saw and could not represent, recorded for the rule
	// that refuses it rather than refused while decoding, so every refusal is
	// one named rule the kit protects: a ceiling that is not a list, an entry
	// carrying a field of its own, an entry that does not decode.
	notAList        bool
	hasUnknownField bool
	unknownField    string
	malformed       error
}

// CeilingScope is one explicit scope of a ceiling: a resource kind, named
// literally, and the actions permitted on it.
type CeilingScope struct {
	ResourceKind string   `yaml:"resource_kind"`
	Actions      []string `yaml:"actions"`
}

// UnmarshalYAML reads a ceiling in either spelling. A bare item lands in
// Actions and a mapping in Scopes, so a list mixing the two decodes into both
// and the one-spelling rule refuses it by name, exactly as it refuses a
// Ceiling value constructed that way. What cannot be represented at all is
// recorded for its rule.
func (ceiling *Ceiling) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		ceiling.notAList = true
		return nil
	}
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			// Decoded with its type, for the reason a slot's reference is:
			// [!!int read] is not a list of actions, and reading Value made
			// it one.
			var action string
			if err := item.Decode(&action); err != nil {
				if ceiling.malformed == nil {
					ceiling.malformed = err
				}
				continue
			}
			ceiling.Actions = append(ceiling.Actions, action)
		case yaml.MappingNode:
			mapping := wire.ReadMapping(item, func(key string, _ *yaml.Node) (bool, error) {
				return key == fieldResourceKind || key == fieldActions, nil
			})
			if mapping.HasUnknown && !ceiling.hasUnknownField {
				ceiling.hasUnknownField, ceiling.unknownField = true, mapping.Unknown
			}
			if mapping.Err != nil && ceiling.malformed == nil {
				ceiling.malformed = mapping.Err
			}
			var scope CeilingScope
			if err := item.Decode(&scope); err != nil && ceiling.malformed == nil {
				ceiling.malformed = err
			}
			ceiling.Scopes = append(ceiling.Scopes, scope)
		default:
			if ceiling.malformed == nil {
				ceiling.malformed = fmt.Errorf("an entry is neither an action nor a {resource_kind, actions} entry")
			}
		}
	}
	return nil
}

// MarshalYAML writes a ceiling back in the spelling it holds: the bare
// actions, or the explicit scopes, or an empty list. A ceiling holding both
// has no spelling and is refused, as Validate refuses it.
func (ceiling Ceiling) MarshalYAML() (any, error) {
	switch {
	case len(ceiling.Actions) > 0 && len(ceiling.Scopes) > 0:
		return nil, fmt.Errorf("%w: a scope ceiling mixes bare actions with {resource_kind, actions} entries; a scope ceiling is written in one spelling", ErrInvalid)
	case len(ceiling.Scopes) > 0:
		return ceiling.Scopes, nil
	case len(ceiling.Actions) > 0:
		return ceiling.Actions, nil
	}
	return []string{}, nil
}

// empty reports a ceiling that permits nothing.
func (ceiling Ceiling) empty() bool { return len(ceiling.Actions) == 0 && len(ceiling.Scopes) == 0 }

// Lookup is how a binding's lookup operation discovers what it looks up.
type Lookup struct {
	Method string `yaml:"method"`
}

// Slot is a reference to a workspace configuration value the composition
// supplies: {from: <group>/<key>}. It is a struct and not a string so that a
// literal value where a slot belongs is a schema error at the reader. An
// audience slot may instead be {self: true}: the module itself, resolved to
// the contract's own principal (see "The module itself").
type Slot struct {
	From string `yaml:"from"`
	// Self addresses the binding to the module itself. Only an audience may
	// be, and never beside From.
	Self bool `yaml:"self,omitempty"`

	// present records that the document wrote the slot at all, so a binding
	// writing audience: {} beside audiences is refused rather than read as
	// declaring only the list.
	present bool

	// What the decoder saw where a slot belongs and the rule that refuses it:
	// a literal, or a mapping carrying a field a slot does not, or one that
	// names a key twice. Presence is a bool and not the name itself: a field
	// named "" is an unknown field, and a sentinel of "" cannot say so.
	literal   *string
	hasExtra  bool
	extra     string
	malformed error
	// selfWritten records that the document wrote self at all, so self:
	// false — a slot that says what it is not — is refused rather than read
	// as a slot naming nothing.
	selfWritten bool
}

// SlotGroups lists the workspace configuration groups the contract's slots
// resolve from, each once, sorted: what a render of the module bakes into its
// delivered manifest's authority beside what its services consume.
func (contract *Contract) SlotGroups() []string {
	seen := map[string]struct{}{}
	for index := range contract.Bindings {
		for _, declared := range contract.Bindings[index].fieldSlots() {
			if declared.slot.From == "" {
				continue
			}
			seen[declared.slot.Group()] = struct{}{}
		}
	}
	groups := make([]string, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

// UnmarshalYAML reads a slot: a mapping with exactly the key "from". A bare
// scalar is a literal written where another module's vocabulary belongs, and
// an extra key is a slot trying to carry a default; each is recorded for the
// rule that refuses it.
func (slot *Slot) UnmarshalYAML(node *yaml.Node) error {
	slot.present = true
	if node.Kind != yaml.MappingNode {
		literal := node.Value
		slot.literal = &literal
		return nil
	}
	mapping := wire.ReadMapping(node, func(key string, value *yaml.Node) (bool, error) {
		switch key {
		case fieldFrom:
			// Decoded with its type, not copied off the node: a tagged scalar —
			// {from: !!int assistant/model-audience} — is refused by the typed
			// decoder and was silently accepted as a string by reading Value.
			return true, value.Decode(&slot.From)
		case fieldSelf:
			slot.selfWritten = true
			return true, value.Decode(&slot.Self)
		}
		return false, nil
	})
	slot.hasExtra, slot.extra, slot.malformed = mapping.HasUnknown, mapping.Unknown, mapping.Err
	return nil
}

// MarshalYAML writes the slot as {from: <group>/<key>}, or {self: true}.
func (slot Slot) MarshalYAML() (any, error) { return slot.wire(), nil }

// MarshalJSON writes a slot in the spelling the file uses, {"from": ...} or
// {"self": true}, so a document that carries one as JSON — a solution
// manifest's canonical bytes — spells it as its YAML does.
func (slot Slot) MarshalJSON() ([]byte, error) { return json.Marshal(slot.wire()) }

// wire is the slot in the spelling the file uses. A slot holding both is
// refused by Validate and is written with both, so it reads back refused.
func (slot Slot) wire() map[string]any {
	if !slot.Self {
		return map[string]any{fieldFrom: slot.From}
	}
	if slot.From == "" {
		return map[string]any{fieldSelf: true}
	}
	return map[string]any{fieldFrom: slot.From, fieldSelf: true}
}

// declared reports whether the slot was written: read from a document, or
// constructed with a reference or as the module itself.
func (slot Slot) declared() bool {
	return slot.present || slot.From != "" || slot.Self || slot.literal != nil
}

// composed reports a slot the composition fills: every declared slot but the
// module itself, which the contract's own principal answers.
func (slot Slot) composed() bool { return !slot.Self }

// fieldSlot is one declared slot of a binding and the field it fills.
type fieldSlot struct {
	field string
	slot  *Slot
}

// fieldSlots lists the binding's declared slots in file order: its audience
// (unless it declares only an audience list), the list, the resource kind or
// the per-audience kinds, and the binding key.
func (binding *Binding) fieldSlots() []fieldSlot {
	var all []fieldSlot
	if binding.Audiences == nil || binding.Audience.declared() {
		all = append(all, fieldSlot{fieldAudience, &binding.Audience})
	}
	for _, declared := range []fieldSlot{
		{fieldAudiences, binding.Audiences}, {fieldResourceKind, binding.ResourceKind},
		{fieldResourceKinds, binding.ResourceKinds}, {fieldBindingKey, binding.BindingKey},
	} {
		if declared.slot != nil {
			all = append(all, declared)
		}
	}
	return all
}

// composedSlots is fieldSlots without the module itself: the slots the
// composition fills.
func (binding *Binding) composedSlots() []fieldSlot {
	var composed []fieldSlot
	for _, declared := range binding.fieldSlots() {
		if declared.slot.composed() {
			composed = append(composed, declared)
		}
	}
	return composed
}

// Group and Key split the slot's reference into the workspace configuration
// group and the key within it.
func (slot Slot) Group() string { group, _, _ := strings.Cut(slot.From, "/"); return group }

// Key is the key half of the slot's reference.
func (slot Slot) Key() string { _, key, _ := strings.Cut(slot.From, "/"); return key }

// slotSuffixes is the naming convention a slot's key carries, per slot: the
// key's meaning, since no reader can check what the value it resolves to means.
// Compared in core's normalized spelling, so model-audience and MODEL_AUDIENCE
// both pass.
var slotSuffixes = map[string][]string{
	fieldAudience:      {"_AUDIENCE", "_PREFIX"},
	fieldAudiences:     {"_AUDIENCES"},
	fieldResourceKind:  {"_RESOURCE_KIND"},
	fieldResourceKinds: {"_RESOURCE_KINDS"},
	fieldBindingKey:    {"_BINDING"},
}

// Destination is one endpoint the module exposes for a caller outside it.
type Destination struct {
	ID       string `yaml:"id"`
	Service  string `yaml:"service"`
	Endpoint string `yaml:"endpoint"`
	Kind     string `yaml:"kind"`
}

// Load reads the contract of the module at moduleDir. A module that publishes
// no contract returns os.ErrNotExist, which a caller distinguishes from a
// contract that cannot be read: the first is a module with nothing to ask, the
// second is a module whose request is unreadable.
func Load(moduleDir string) (*Contract, error) {
	// The module directory is one the composition resolved; the file is read
	// through it rather than by a path, so a FileName that resolved outside it
	// would be refused.
	root, err := os.OpenRoot(moduleDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(FileName)
	if err != nil {
		return nil, err
	}
	contract, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.ToSlash(filepath.Join(moduleDir, FileName)), err)
	}
	return contract, nil
}

// Parse decodes and validates one contract. Decoding is strict: an unknown
// field is an error rather than a silently ignored intention, which is what
// refuses a contract carrying a tenancy, a build digest or an identity it is not
// entitled to assert. The schema is checked first and leniently, so a contract
// of another version is reported as a version skew rather than as malformed.
func Parse(data []byte) (*Contract, error) { return parse(data, "") }

// parse is Parse with one rule deleted — the self-check's entrypoint, which
// proves every rule is protected by a fixture. "" deletes none.
func parse(data []byte, without string) (*Contract, error) {
	var header struct {
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("%w: the module contract is not a YAML document: %v", ErrInvalid, err)
	}
	if header.Schema != SchemaV1 && without != ruleSchema {
		return nil, fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, header.Schema, SchemaV1)
	}
	// One mapping, one key — at EVERY mapping boundary, including the
	// ordinary maps a typed decoder cannot guard: a per-operation ceiling and
	// a selector are dynamic maps, and yaml's own duplicate check compares
	// raw key nodes before resolving an alias, so an alias key silently
	// replaced an earlier entry and changed the requested scopes or the pods
	// selected.
	var tree yaml.Node
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("%w: the module contract is not a YAML document: %v", ErrInvalid, err)
	}
	switch defect := wire.Check(&tree); {
	case defect.Kind == wire.DuplicateKey && without != ruleMappingKeysOnce:
		return nil, fmt.Errorf("%w: the mapping at %s names the key %q twice; a repeated key — including one written through an alias — makes the decoded document depend on order",
			ErrInvalid, defect.Path, defect.Detail)
	case defect.Kind == wire.MergeKey && without != ruleNoMergeKeys:
		return nil, fmt.Errorf("%w: the mapping at %s uses the merge key <<; yaml applies a merge inside its typed decoder, after every rule over the tree has run, so a field carried in through one reaches the contract unseen — no field of a module contract is declared by merging",
			ErrInvalid, defect.Path)
	case defect.Kind == wire.NullNode && without != ruleNoNulls:
		return nil, fmt.Errorf("%w: the document carries an explicit null at %s %s; a null decodes as neither a value nor an error, so a typed decoder DISCARDS it — an absent field is absent, and an empty list is written []",
			ErrInvalid, defect.Path, defect.Detail)
	case defect.Kind == wire.FractionalNumber && without != ruleWholeNumbers:
		return nil, fmt.Errorf("%w: the number %s at %s is not a whole number; yaml CONVERTS a fraction to an integer before any rule sees it, so this would have been validated as %s truncated — no field of a module contract takes a fraction",
			ErrInvalid, defect.Detail, defect.Path, defect.Detail)
	case defect.Kind == wire.KeyNotAName && without != ruleKeyIsAName:
		return nil, fmt.Errorf("%w: the mapping at %s carries a key that is not a name (%q); these mappings are keyed by name",
			ErrInvalid, defect.Path, defect.Detail)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(without != ruleKnownFields)
	var contract Contract
	if err := decoder.Decode(&contract); err != nil {
		return nil, fmt.Errorf("%w: the module contract carries a field no contract declares, or a value of the wrong shape: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) && without != ruleOneDocument {
		return nil, fmt.Errorf("%w: the file holds more than one document", ErrInvalid)
	}
	if err := contract.validate(without); err != nil {
		return nil, err
	}
	return &contract, nil
}

// Validate checks everything a contract can be checked against on its own:
// every rule, in order, the first refusal named. Resolve applies no rule of
// its own beyond these but the ones that need the composition's values.
func (contract *Contract) Validate() error { return contract.validate("") }

// Resolved is a contract with every slot resolved to the value the composition
// supplies, ready to be derived into a manifest's authority.
type Resolved struct {
	Principal      string
	Namespaces     []string
	Queues         []string
	ScopeCeilings  []ScopeCeiling
	Bindings       []ResolvedBinding
	Destinations   []Destination
	ForeignMethods []ForeignMethod
	// Omitted are the optional bindings the composition fills none of the
	// slots of, which the resolution leaves out: nothing can be issued
	// through them. In declaration order.
	Omitted []OmittedBinding
}

// OmittedBinding is one optional binding a resolution left out, and why.
type OmittedBinding struct {
	ID string
	// Reason says why, naming every slot the composition left unfilled.
	Reason string
}

// ResolvedBinding is one binding with its audience, resource kind and binding
// key resolved.
type ResolvedBinding struct {
	ID         string
	Revision   uint64
	Operations []string
	Lookup     *Lookup
	Audience   string
	// ResourceKind is the resolved resource kind, or empty when the binding
	// declared none.
	ResourceKind string
	// BindingKey is the resolved key, or empty when the binding declared none.
	BindingKey string
	// Scopes are, per operation, the scope strings the binding may at most
	// carry, every one "<resource kind>:<action>": the binding's resolved
	// resource kind qualifies its bare actions, an explicit scope names its
	// own, and an operation that names calls carries what they require.
	// Sorted, each once.
	Scopes map[string][]string
	// Calls are, per operation that names them, the calls its ceiling was
	// read from. Sorted.
	Calls map[string][]string
}

// Resolve resolves every slot of the contract against values. Every unresolved,
// secret or ambiguous slot is reported in one error carrying every sentinel
// that applies, so a composition is fixed in one pass rather than one slot per
// render; a secret's value is never part of the message.
func (contract *Contract) Resolve(values Values, options ...ResolveOption) (*Resolved, error) {
	return contract.resolve(values, "", options...)
}

// resolve is Resolve with one resolution rule removed, for the kit's
// completeness self-check.
func (contract *Contract) resolve(values Values, without string, options ...ResolveOption) (*Resolved, error) {
	var opts resolveOptions
	for _, option := range options {
		option(&opts)
	}
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	resolved := &Resolved{
		Principal:      contract.Principal,
		Namespaces:     slices.Clone(contract.Namespaces),
		Queues:         slices.Clone(contract.Queues),
		ScopeCeilings:  slices.Clone(contract.ScopeCeilings),
		Destinations:   slices.Clone(contract.Destinations),
		ForeignMethods: slices.Clone(contract.ForeignMethods),
	}
	var unresolved, secret, ambiguous, calls []string
	// One Records call per group, so a provider is asked for a group's records
	// once and every slot of that group is resolved from the same answer.
	groups := map[string][]Record{}
	recordsOf := func(group string) ([]Record, error) {
		if records, held := groups[group]; held {
			return records, nil
		}
		supplied, err := values.Records(group)
		if err != nil {
			return nil, err
		}
		groups[group] = supplied
		return supplied, nil
	}
	resolve := func(label string, slot *Slot, kind bool) string {
		if slot == nil {
			return ""
		}
		if slot.Self {
			// The module itself: the contract's principal, which every
			// renderer holds to the name the module is composed under.
			return contract.Principal
		}
		records, err := recordsOf(slot.Group())
		if err != nil {
			ambiguous = append(ambiguous, label+" ← "+slot.From+" (the composition's values cannot be read: "+err.Error()+")")
			return ""
		}
		answer := resolveSlot(records, slot.Key(), kind, without)
		switch {
		case answer.reason != "":
			ambiguous = append(ambiguous, label+" ← "+slot.From+" "+answer.reason)
			return ""
		case !answer.found:
			unresolved = append(unresolved, label+" ← "+slot.From)
			return ""
		case answer.secret:
			secret = append(secret, label+" ← "+slot.From)
			return ""
		}
		return answer.value
	}
	// targetsOf resolves where a binding is delivered: its one audience and
	// resource kind under its own id, or — for a binding declaring an audience
	// list — each listed audience under AudienceBindingID with that audience's
	// kind. A list that does not resolve is reported and delivers nothing.
	targetsOf := func(binding Binding, label string) []bindingTarget {
		if binding.Audiences == nil {
			return []bindingTarget{{
				id:       binding.ID,
				audience: resolve(label+" audience", &binding.Audience, false),
				kind:     resolve(label+" resource_kind", binding.ResourceKind, true),
			}}
		}
		listed := resolve(label+" audiences", binding.Audiences, false)
		kinds := resolve(label+" resource_kinds", binding.ResourceKinds, false)
		if listed == "" || (binding.ResourceKinds != nil && kinds == "") {
			return nil
		}
		audiences, reason := resolveAudienceList(listed, without)
		if reason != "" {
			ambiguous = append(ambiguous, label+" audiences ← "+binding.Audiences.From+" "+reason)
			return nil
		}
		var kindOf map[string]string
		if binding.ResourceKinds != nil {
			var missing []string
			kindOf, missing, reason = resolveKindsPerAudience(kinds, audiences, without)
			switch {
			case reason != "":
				ambiguous = append(ambiguous, label+" resource_kinds ← "+binding.ResourceKinds.From+" "+reason)
				return nil
			case len(missing) > 0:
				unresolved = append(unresolved, label+" resource_kinds ← "+binding.ResourceKinds.From+" names no kind for the listed audience "+strings.Join(missing, ", "))
				return nil
			}
		}
		targets := make([]bindingTarget, 0, len(audiences))
		for _, audience := range audiences {
			targets = append(targets, bindingTarget{id: AudienceBindingID(binding.ID, audience), audience: audience, kind: kindOf[audience]})
		}
		return targets
	}
	for _, binding := range contract.Bindings {
		label := "binding " + binding.ID
		if binding.Optional {
			unfilled, none := binding.unfilledSlots(recordsOf)
			switch {
			case none:
				resolved.Omitted = append(resolved.Omitted, OmittedBinding{
					ID:     binding.ID,
					Reason: "it is optional and the composition fills none of its slots (" + strings.Join(unfilled, ", ") + "), so nothing can be issued through it",
				})
				continue
			case len(unfilled) > 0:
				// Filled in part is resolved whole: each slot left unfilled
				// is refused below by name, with the reason it was not
				// omitted. Filled whole, it resolves as a mandatory binding.
				label = "optional binding " + binding.ID + " (filled in part, so resolved whole)"
			}
		}
		revision := binding.Revision
		if revision == 0 {
			revision = 1
		}
		targets := targetsOf(binding, label)
		bindingKey := resolve(label+" binding_key", binding.BindingKey, false)
		for _, target := range targets {
			entry := ResolvedBinding{
				ID: target.id, Revision: revision, Operations: slices.Clone(binding.Operations), Lookup: binding.Lookup,
				Audience: target.audience, ResourceKind: target.kind, BindingKey: bindingKey,
				Scopes: make(map[string][]string, len(binding.Operations)),
			}
			for _, operation := range binding.Operations {
				ceiling := binding.ScopeCeiling[operation]
				scopes := make([]string, 0, len(ceiling.Actions)+len(ceiling.Scopes))
				// Bare actions are qualified by the binding's resolved resource
				// kind; explicit scopes carry their own, already held to the
				// grammar by Validate.
				for _, action := range ceiling.Actions {
					scopes = append(scopes, entry.ResourceKind+":"+action)
				}
				for _, scope := range ceiling.Scopes {
					for _, action := range scope.Actions {
						scopes = append(scopes, scope.ResourceKind+":"+action)
					}
				}
				if named := binding.Calls[operation]; len(named) > 0 {
					if entry.Calls == nil {
						entry.Calls = map[string][]string{}
					}
					entry.Calls[operation] = slices.Sorted(slices.Values(named))
					operationLabel := label + " " + operation
					switch {
					case opts.callees == nil:
						calls = append(calls, operationLabel+" names calls, and the contract was resolved with no callee declarations to read them from")
					case entry.Audience != "":
						// Read from THIS audience's declarations: one binding
						// listing two audiences gets each one's ceiling, and a
						// call one of them does not serve is refused naming it.
						for _, call := range entry.Calls[operation] {
							required, err := opts.callees.Requires(entry.Audience, call)
							if err != nil {
								calls = append(calls, operationLabel+" call "+call+" at "+entry.Audience+": "+err.Error())
								continue
							}
							scopes = append(scopes, required...)
						}
					}
				}
				sort.Strings(scopes)
				entry.Scopes[operation] = slices.Compact(scopes)
			}
			resolved.Bindings = append(resolved.Bindings, entry)
		}
	}
	if err := resolutionError(secret, unresolved, ambiguous); err != nil {
		if len(calls) > 0 {
			err = errors.Join(err, callsError(calls))
		}
		return nil, err
	}
	if len(calls) > 0 {
		return nil, callsError(calls)
	}
	return resolved, nil
}

// bindingTarget is one place a binding is delivered: the id it is delivered
// under, its audience, and the resource kind its bare actions are qualified by.
type bindingTarget struct{ id, audience, kind string }

// resolveAudienceList reads an audience list's resolved value: comma-separated
// names, each once, returned sorted so two compositions listing one set in two
// orders deliver one document. A value that is not such a list has no
// audiences to deliver to, and the reason says why. `without` removes the
// list-grammar rule, for the kit's completeness self-check.
func resolveAudienceList(value, without string) ([]string, string) {
	var audiences []string
	// The value is one line with no space in it (ruleResolvedName), so an
	// entry is exactly what lies between two commas.
	for _, audience := range strings.Split(value, ",") {
		if without != ruleResolvedList {
			switch {
			case !namePattern.MatchString(audience):
				return nil, fmt.Sprintf("lists %q, which is not an audience; an audience list is comma-separated lowercase names", audience)
			case slices.Contains(audiences, audience):
				return nil, fmt.Sprintf("lists the audience %q twice", audience)
			}
		}
		audiences = append(audiences, audience)
	}
	sort.Strings(audiences)
	return audiences, ""
}

// resolveKindsPerAudience reads a per-audience kinds value — <audience>=<kind>
// pairs, comma-separated — against the listed audiences: each kind held to the
// grammar a literal kind is, each pair naming a listed audience once. The
// listed audiences it names no kind for are returned, for the caller to refuse
// by name.
func resolveKindsPerAudience(value string, audiences []string, without string) (map[string]string, []string, string) {
	kinds := map[string]string{}
	for _, entry := range strings.Split(value, ",") {
		audience, kind, paired := strings.Cut(entry, "=")
		if without != ruleResolvedList {
			switch {
			case !paired:
				return nil, nil, fmt.Sprintf("carries %q, which is not <audience>=<kind>", entry)
			case !slices.Contains(audiences, audience):
				return nil, nil, fmt.Sprintf("names a kind for %q, which the binding's audience list does not list", audience)
			case kinds[audience] != "":
				return nil, nil, fmt.Sprintf("names a kind for the audience %q twice", audience)
			}
		}
		if !namePattern.MatchString(kind) && without != ruleResolvedKind {
			return nil, nil, fmt.Sprintf("resolves the kind of %q to %q, which is not a lowercase resource kind; a resolved kind is held to the grammar a literal one is, because it is concatenated into a scope", audience, kind)
		}
		kinds[audience] = kind
	}
	var missing []string
	for _, audience := range audiences {
		if kinds[audience] == "" {
			missing = append(missing, audience)
		}
	}
	return kinds, missing, ""
}

// unfilledSlots names each slot of the binding the composition leaves
// unfilled, and reports whether that is every one of them. A slot is unfilled when no record is its key,
// or every record that is supplies an empty public value — the spelling a
// module's declared configuration gives a key nobody set. A secret record, or
// a group whose records cannot be read, counts as filled, so resolution
// reports it rather than an omission hiding it. The module itself is no slot
// the composition fills, so it is neither (optional-binding-has-a-slot keeps
// the count above zero).
func (binding Binding) unfilledSlots(recordsOf func(group string) ([]Record, error)) ([]string, bool) {
	var unfilled []string
	slots := binding.composedSlots()
	for _, slot := range slots {
		records, err := recordsOf(slot.slot.Group())
		if err != nil || slotFilled(records, slot.slot.Key()) {
			continue
		}
		unfilled = append(unfilled, slot.field+" ← "+slot.slot.From)
	}
	return unfilled, len(unfilled) == len(slots)
}

// callsError names every call a resolution could not read a requirement for.
func callsError(calls []string) error {
	return fmt.Errorf("%w: %s; a call's requirement is the audience's own method policy, read from its published contracts", ErrUnresolvedCall, strings.Join(calls, "; "))
}

// resolutionError is the one error a Resolve reports: every category that
// applies, each under its sentinel, so errors.Is answers for each and a
// composition is fixed in one pass.
func resolutionError(secret, unresolved, ambiguous []string) error {
	var parts []error
	if len(secret) > 0 {
		parts = append(parts, fmt.Errorf("%w: %s; a slot resolves public configuration only, because its value is written into a delivered document", ErrSecretSlot, strings.Join(secret, "; ")))
	}
	if len(unresolved) > 0 {
		parts = append(parts, fmt.Errorf("%w: %s; the composition supplies each as a workspace configuration value for this environment", ErrUnresolvedSlot, strings.Join(unresolved, "; ")))
	}
	if len(ambiguous) > 0 {
		parts = append(parts, fmt.Errorf("%w: %s; records that are one key supply one value, and a resolved value is one a scope or an audience can be built from", ErrAmbiguousSlot, strings.Join(ambiguous, "; ")))
	}
	switch len(parts) {
	case 0:
		return nil
	case 1:
		return parts[0]
	}
	return errors.Join(parts...)
}
