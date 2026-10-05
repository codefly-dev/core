// Package modulecontract is the ONE implementation of the module contract —
// codefly/module-contract/v1, the file a module publishes beside its manifest
// as module.contract.codefly.yaml: its Go model, its strict decoder, every
// refusal rule, and the resolution of its slots against a composition. The
// CLI renders an authority document from it, the runtimes publish it, and the
// host's loader reads it: all three read it THROUGH this package, and the
// fixtures it ships (Fixtures) are what each of them is driven through, so a
// decoder that started accepting an invalid ceiling would fail here first and
// everywhere at once. Nothing outside this package may parse or validate a
// module contract.
//
// It lives under solutionhost because the contract is the request the
// authority document grants: the envelope bounds it, the authority document
// the CLI derives from it is signed and delivered to the host, and the host
// activates it — the same lifecycle this directory owns.
//
// The contract is a REQUEST, not a grant. It says what the module asks to be
// able to do on a host: the principal its credentials are issued to, the
// operation bindings it redeems, the queues and namespaces it declares, the
// scope ceilings it contributes, and the destinations it exposes. Three records
// narrow it in order — the host's envelope (the ceiling a platform
// administrator writes), the authority document the render derives from this
// file and the delivery pipeline signs, and an organisation's installation — so
// nothing here is wider than what the module exercises, and nothing here grants
// anything by itself.
//
// Four things never appear in a contract, because they are not the module's to
// assert: tenancy and cross-tenant reach (the envelope is the only record that
// may name an organisation), the approved build digest (the render's: the OCI
// image manifest digest of the container it pins), and the expected workload
// identity (the platform's: it issues SVIDs and owns the trust domain). The
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
// when it derives the authority document. A slot resolves PUBLIC configuration
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
package modulecontract

import (
	"bytes"
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
	fieldAudience     = "audience"
	fieldResourceKind = "resource_kind"
	fieldBindingKey   = "binding_key"
	fieldActions      = "actions"
	fieldFrom         = "from"
)

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
)

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
	// Bindings are the operation bindings the module redeems, outward.
	Bindings []Binding `yaml:"bindings"`
	// Destinations are the endpoints the module exposes for something else to
	// route to.
	Destinations []Destination `yaml:"destinations"`
}

// ScopeCeiling is one resource kind of the module's own vocabulary and the
// actions it contributes on it.
type ScopeCeiling struct {
	ResourceKind string   `yaml:"resource_kind"`
	Actions      []string `yaml:"actions"`
}

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
	// Audience is the operation audience, a slot into the composition.
	Audience Slot `yaml:"audience"`
	// ResourceKind is the other module's resource kind the scopes name, a
	// slot. Required by a ceiling written as bare actions, which it qualifies;
	// absent for a binding whose every ceiling spells its scopes out.
	ResourceKind *Slot `yaml:"resource_kind,omitempty"`
	// BindingKey is the key the host installs the binding under, a slot.
	BindingKey *Slot `yaml:"binding_key,omitempty"`
	// ScopeCeiling is, per declared operation, the scopes the binding may at
	// most carry. Every declared operation names its ceiling: a binding
	// declaring no scopes for an operation cannot be minted that way at all.
	ScopeCeiling map[string]Ceiling `yaml:"scope_ceiling"`
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
	repeatedField   string
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
			seen := make(map[string]bool, len(item.Content)/2)
			for index := 0; index+1 < len(item.Content); index += 2 {
				key := item.Content[index].Value
				if seen[key] && ceiling.repeatedField == "" {
					ceiling.repeatedField = key
				}
				seen[key] = true
				if key != fieldResourceKind && key != fieldActions && !ceiling.hasUnknownField {
					ceiling.hasUnknownField, ceiling.unknownField = true, key
				}
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
// literal value where a slot belongs is a schema error at the reader.
type Slot struct {
	From string `yaml:"from"`

	// What the decoder saw where a slot belongs and the rule that refuses it:
	// a literal, or a mapping carrying a field a slot does not, or one that
	// names a key twice. Presence is a bool and not the name itself: a field
	// named "" is an unknown field, and a sentinel of "" cannot say so.
	literal   *string
	hasExtra  bool
	extra     string
	repeated  string
	malformed error
}

// SlotGroups lists the workspace configuration groups the contract's slots
// resolve from, each once, sorted: what a render of the module bakes into its
// delivered authority document beside what its services consume.
func (contract *Contract) SlotGroups() []string {
	seen := map[string]struct{}{}
	for _, binding := range contract.Bindings {
		for _, slot := range []*Slot{&binding.Audience, binding.ResourceKind, binding.BindingKey} {
			if slot == nil || slot.From == "" {
				continue
			}
			seen[slot.Group()] = struct{}{}
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
	if node.Kind != yaml.MappingNode {
		literal := node.Value
		slot.literal = &literal
		return nil
	}
	seen := make(map[string]bool, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index].Value
		switch {
		case seen[key]:
			// A repeated key means the document says two things and a
			// decoder that keeps assigning silently honours the last.
			if slot.repeated == "" {
				slot.repeated = key
			}
			continue
		case key != fieldFrom:
			if !slot.hasExtra {
				slot.hasExtra, slot.extra = true, key
			}
		default:
			// Decoded with its type, not copied off the node: a tagged
			// scalar — {from: !!int assistant/model-audience} — is refused by
			// the typed decoder and was silently accepted as a string by
			// reading Value, which also gave the two ceiling spellings
			// different scalar validation.
			if err := node.Content[index+1].Decode(&slot.From); err != nil && slot.malformed == nil {
				slot.malformed = err
			}
		}
		seen[key] = true
	}
	return nil
}

// MarshalYAML writes the slot as {from: <group>/<key>}.
func (slot Slot) MarshalYAML() (any, error) {
	return map[string]string{fieldFrom: slot.From}, nil
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
	fieldAudience:     {"_AUDIENCE", "_PREFIX"},
	fieldResourceKind: {"_RESOURCE_KIND"},
	fieldBindingKey:   {"_BINDING"},
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
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if header.Schema != SchemaV1 && without != ruleSchema {
		return nil, fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, header.Schema, SchemaV1)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(without != ruleKnownFields)
	var contract Contract
	if err := decoder.Decode(&contract); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
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
// supplies, ready to be derived into an authority document.
type Resolved struct {
	Principal     string
	Namespaces    []string
	Queues        []string
	ScopeCeilings []ScopeCeiling
	Bindings      []ResolvedBinding
	Destinations  []Destination
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
	// resource kind qualifies its bare actions, and an explicit scope names
	// its own. Sorted.
	Scopes map[string][]string
}

// Resolve resolves every slot of the contract against values. Every unresolved,
// secret or ambiguous slot is reported in one error carrying every sentinel
// that applies, so a composition is fixed in one pass rather than one slot per
// render; a secret's value is never part of the message.
func (contract *Contract) Resolve(values Values) (*Resolved, error) {
	return contract.resolve(values, "")
}

// resolve is Resolve with one resolution rule removed, for the kit's
// completeness self-check.
func (contract *Contract) resolve(values Values, without string) (*Resolved, error) {
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	resolved := &Resolved{
		Principal:     contract.Principal,
		Namespaces:    slices.Clone(contract.Namespaces),
		Queues:        slices.Clone(contract.Queues),
		ScopeCeilings: slices.Clone(contract.ScopeCeilings),
		Destinations:  slices.Clone(contract.Destinations),
	}
	var unresolved, secret, ambiguous []string
	// One Records call per group, so a provider is asked for a group's records
	// once and every slot of that group is resolved from the same answer.
	groups := map[string][]Record{}
	resolve := func(label string, slot *Slot, kind bool) string {
		if slot == nil {
			return ""
		}
		records, held := groups[slot.Group()]
		if !held {
			supplied, err := values.Records(slot.Group())
			if err != nil {
				ambiguous = append(ambiguous, label+" ← "+slot.From+" (the composition's values cannot be read: "+err.Error()+")")
				return ""
			}
			records, groups[slot.Group()] = supplied, supplied
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
	for _, binding := range contract.Bindings {
		revision := binding.Revision
		if revision == 0 {
			revision = 1
		}
		entry := ResolvedBinding{
			ID: binding.ID, Revision: revision, Operations: slices.Clone(binding.Operations), Lookup: binding.Lookup,
			Audience:     resolve("binding "+binding.ID+" audience", &binding.Audience, false),
			ResourceKind: resolve("binding "+binding.ID+" resource_kind", binding.ResourceKind, true),
			BindingKey:   resolve("binding "+binding.ID+" binding_key", binding.BindingKey, false),
			Scopes:       make(map[string][]string, len(binding.Operations)),
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
			sort.Strings(scopes)
			entry.Scopes[operation] = scopes
		}
		resolved.Bindings = append(resolved.Bindings, entry)
	}
	if err := resolutionError(secret, unresolved, ambiguous); err != nil {
		return nil, err
	}
	return resolved, nil
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
