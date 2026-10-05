package modulecontract

import (
	"fmt"
	"slices"
	"strings"
)

// A rule is one refusal this reader enforces, named: a conformance fixture
// says which rule refuses it, and the kit's self-check deletes each rule in
// turn and proves at least one fixture notices. Validate runs every rule in
// this order and returns the first refusal, so a document is refused for one
// reason, named — the same reason whichever reader refused it.
//
// Three refusals are the decoder's (ruleSchema, ruleKnownFields,
// ruleOneDocument) and one is inherent (ruleWellFormed: a document that is
// not YAML has nothing to validate); they are listed here so the fixture
// table and the self-check cover them like any other.
type rule struct {
	name  string
	check func(contract *Contract) error
	// inherent marks a rule the self-check cannot delete: YAML syntax is not
	// a rule of this package, only a precondition of reading anything.
	inherent bool
}

// The rules, by name. A refused fixture names one of these; a reader that
// drops one fails the kit on that fixture.
const (
	ruleWellFormed      = "well-formed"
	ruleSchema          = "schema"
	ruleKnownFields     = "known-fields"
	ruleOneDocument     = "one-document"
	ruleMappingKeysOnce = "mapping-keys-once"
	ruleNoNulls         = "no-explicit-nulls"
	ruleKeyIsAName      = "key-is-a-name"

	rulePrincipal       = "principal-name"
	ruleListsDeclared   = "lists-declared"
	ruleListEntryName   = "list-entry-name"
	ruleListEntryUnique = "list-entry-unique"

	ruleCeilingKindName   = "ceiling-kind-name"
	ruleCeilingKindUnique = "ceiling-kind-unique"
	ruleCeilingHasAction  = "ceiling-has-action"
	ruleActionName        = "action-name"
	ruleActionUnique      = "action-unique"

	ruleBindingIDName         = "binding-id-name"
	ruleBindingIDUnique       = "binding-id-unique"
	ruleBindingHasOperation   = "binding-has-operation"
	ruleOperationKnown        = "operation-known"
	ruleOperationUnique       = "operation-unique"
	ruleLookupNeedsOperation  = "lookup-needs-operation"
	ruleLookupMethodName      = "lookup-method-name"
	ruleSlotIsAReference      = "slot-is-a-reference"
	ruleSlotCarriesOnlyFrom   = "slot-carries-only-from"
	ruleSlotDecodes           = "slot-decodes"
	ruleSlotReference         = "slot-reference"
	ruleSlotKeyMeaning        = "slot-key-meaning"
	ruleCeilingIsAList        = "ceiling-is-a-list"
	ruleCeilingEntryFields    = "ceiling-entry-fields"
	ruleCeilingEntryShape     = "ceiling-entry-shape"
	ruleCeilingOneSpelling    = "ceiling-one-spelling"
	ruleCeilingForDeclaredOp  = "ceiling-for-declared-operation"
	ruleOperationHasCeiling   = "operation-has-ceiling"
	ruleBareActionsNeedKind   = "bare-actions-need-kind"
	ruleScopeKindName         = "scope-kind-name"
	ruleScopeKindUnique       = "scope-kind-unique"
	ruleScopeHasAction        = "scope-has-action"
	ruleDestinationIDName     = "destination-id-name"
	ruleDestinationIDUnique   = "destination-id-unique"
	ruleDestinationTargetName = "destination-target-name"
	ruleDestinationKindKnown  = "destination-kind-known"
)

// rules is the order a contract is held to: the document's shape first, then
// each section in the order the file is written — a ceiling's shape before
// its contents, and every action list last, so a structural defect is named
// before a spelling inside it.
func rules() []rule {
	return []rule{
		{name: ruleWellFormed, inherent: true, check: func(*Contract) error { return nil }},
		{name: ruleSchema, check: checkSchema},
		{name: ruleKnownFields, check: func(*Contract) error { return nil }},
		{name: ruleOneDocument, check: func(*Contract) error { return nil }},
		{name: ruleMappingKeysOnce, check: func(*Contract) error { return nil }},
		{name: ruleNoNulls, check: func(*Contract) error { return nil }},
		{name: ruleKeyIsAName, check: func(*Contract) error { return nil }},
		{name: rulePrincipal, check: checkPrincipal},
		{name: ruleListsDeclared, check: checkListsDeclared},
		{name: ruleListEntryName, check: checkListEntryNames},
		{name: ruleListEntryUnique, check: checkListEntriesUnique},
		{name: ruleCeilingKindName, check: checkCeilingKindNames},
		{name: ruleCeilingKindUnique, check: checkCeilingKindsUnique},
		{name: ruleCeilingHasAction, check: checkCeilingsHaveActions},
		{name: ruleBindingIDName, check: checkBindingIDNames},
		{name: ruleBindingIDUnique, check: checkBindingIDsUnique},
		{name: ruleBindingHasOperation, check: checkBindingsHaveOperations},
		{name: ruleOperationKnown, check: checkOperationsKnown},
		{name: ruleOperationUnique, check: checkOperationsUnique},
		{name: ruleLookupNeedsOperation, check: checkLookupNeedsOperation},
		{name: ruleLookupMethodName, check: checkLookupMethodNames},
		{name: ruleSlotIsAReference, check: checkSlotsAreReferences},
		{name: ruleSlotCarriesOnlyFrom, check: checkSlotsCarryOnlyFrom},
		{name: ruleSlotDecodes, check: checkSlotsDecode},
		{name: ruleSlotReference, check: checkSlotReferences},
		{name: ruleSlotKeyMeaning, check: checkSlotKeyMeanings},
		{name: ruleCeilingIsAList, check: checkCeilingsAreLists},
		{name: ruleCeilingEntryFields, check: checkCeilingEntryFields},
		{name: ruleCeilingEntryShape, check: checkCeilingEntryShapes},
		{name: ruleCeilingOneSpelling, check: checkCeilingsOneSpelling},
		{name: ruleCeilingForDeclaredOp, check: checkCeilingsForDeclaredOperations},
		{name: ruleOperationHasCeiling, check: checkOperationsHaveCeilings},
		{name: ruleBareActionsNeedKind, check: checkBareActionsNeedKind},
		{name: ruleScopeKindName, check: checkScopeKindNames},
		{name: ruleScopeKindUnique, check: checkScopeKindsUnique},
		{name: ruleScopeHasAction, check: checkScopesHaveActions},
		{name: ruleActionName, check: checkActionNames},
		{name: ruleActionUnique, check: checkActionsUnique},
		{name: ruleDestinationIDName, check: checkDestinationIDNames},
		{name: ruleDestinationIDUnique, check: checkDestinationIDsUnique},
		{name: ruleDestinationTargetName, check: checkDestinationTargetNames},
		{name: ruleDestinationKindKnown, check: checkDestinationKindsKnown},
	}
}

// ruleNames lists every rule, in order.
func ruleNames() []string {
	all := rules()
	names := make([]string, 0, len(all))
	for _, r := range all {
		names = append(names, r.name)
	}
	return names
}

// validate runs every rule but the one named, in order; "" deletes none.
func (contract *Contract) validate(without string) error {
	if contract == nil {
		return fmt.Errorf("%w: contract is required", ErrInvalid)
	}
	for _, r := range rules() {
		if r.name == without {
			continue
		}
		if err := r.check(contract); err != nil {
			return err
		}
	}
	return nil
}

func checkSchema(contract *Contract) error {
	if contract.Schema != SchemaV1 {
		return fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, contract.Schema, SchemaV1)
	}
	return nil
}

func checkPrincipal(contract *Contract) error {
	if !namePattern.MatchString(contract.Principal) {
		return fmt.Errorf("%w: principal %q is not a lowercase name", ErrInvalid, contract.Principal)
	}
	return nil
}

// lists are the two name lists a contract declares, empty when there are none.
func (contract *Contract) lists() []struct {
	label  string
	values []string
} {
	return []struct {
		label  string
		values []string
	}{{"namespaces", contract.Namespaces}, {"queues", contract.Queues}}
}

func checkListsDeclared(contract *Contract) error {
	for _, list := range contract.lists() {
		if list.values == nil {
			return fmt.Errorf("%w: %s must be declared, empty when there are none; an absent list and \"there are none\" must not look the same", ErrInvalid, list.label)
		}
	}
	return nil
}

func checkListEntryNames(contract *Contract) error {
	for _, list := range contract.lists() {
		for _, value := range list.values {
			if !namePattern.MatchString(value) {
				return fmt.Errorf("%w: %s entry %q is not a lowercase name", ErrInvalid, list.label, value)
			}
		}
	}
	return nil
}

func checkListEntriesUnique(contract *Contract) error {
	for _, list := range contract.lists() {
		if duplicate, found := firstDuplicate(list.values); found {
			return fmt.Errorf("%w: %s entry %q is declared twice", ErrInvalid, list.label, duplicate)
		}
	}
	return nil
}

// firstDuplicate reports the first value that recurs, in declaration order.
func firstDuplicate(values []string) (string, bool) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return value, true
		}
		seen[value] = struct{}{}
	}
	return "", false
}

func checkCeilingKindNames(contract *Contract) error {
	for _, ceiling := range contract.ScopeCeilings {
		if !namePattern.MatchString(ceiling.ResourceKind) {
			return fmt.Errorf("%w: scope ceiling resource kind %q is not a lowercase name", ErrInvalid, ceiling.ResourceKind)
		}
	}
	return nil
}

func checkCeilingKindsUnique(contract *Contract) error {
	kinds := make([]string, 0, len(contract.ScopeCeilings))
	for _, ceiling := range contract.ScopeCeilings {
		kinds = append(kinds, ceiling.ResourceKind)
	}
	if duplicate, found := firstDuplicate(kinds); found {
		return fmt.Errorf("%w: scope ceiling %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

func checkCeilingsHaveActions(contract *Contract) error {
	for _, ceiling := range contract.ScopeCeilings {
		if len(ceiling.Actions) == 0 {
			return fmt.Errorf("%w: scope ceiling %q contributes no action", ErrInvalid, ceiling.ResourceKind)
		}
	}
	return nil
}

// actionLists are every list of actions a contract carries, labelled: the
// module's own ceilings, each binding's bare ceilings and each explicit scope.
func (contract *Contract) actionLists() []struct {
	label   string
	actions []string
} {
	var lists []struct {
		label   string
		actions []string
	}
	for _, ceiling := range contract.ScopeCeilings {
		lists = append(lists, struct {
			label   string
			actions []string
		}{"scope ceiling " + ceiling.ResourceKind, ceiling.Actions})
	}
	for _, binding := range contract.Bindings {
		for _, operation := range sortedOperations(binding.ScopeCeiling) {
			ceiling := binding.ScopeCeiling[operation]
			label := "binding " + binding.ID + " " + operation
			if len(ceiling.Actions) > 0 {
				lists = append(lists, struct {
					label   string
					actions []string
				}{label, ceiling.Actions})
			}
			for _, scope := range ceiling.Scopes {
				lists = append(lists, struct {
					label   string
					actions []string
				}{label + " " + scope.ResourceKind, scope.Actions})
			}
		}
	}
	return lists
}

// sortedOperations lists a ceiling map's keys in a fixed order, so a refusal
// names the same operation on every run.
func sortedOperations(ceilings map[string]Ceiling) []string {
	keys := make([]string, 0, len(ceilings))
	for operation := range ceilings {
		keys = append(keys, operation)
	}
	slices.Sort(keys)
	return keys
}

func checkActionNames(contract *Contract) error {
	for _, list := range contract.actionLists() {
		for _, action := range list.actions {
			if !actionPattern.MatchString(action) {
				return fmt.Errorf("%w: %s action %q is not a lowercase action name", ErrInvalid, list.label, action)
			}
		}
	}
	return nil
}

func checkActionsUnique(contract *Contract) error {
	for _, list := range contract.actionLists() {
		if duplicate, found := firstDuplicate(list.actions); found {
			return fmt.Errorf("%w: %s action %q is declared twice", ErrInvalid, list.label, duplicate)
		}
	}
	return nil
}

func checkBindingIDNames(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if !namePattern.MatchString(binding.ID) {
			return fmt.Errorf("%w: binding id %q is not a lowercase name", ErrInvalid, binding.ID)
		}
	}
	return nil
}

func checkBindingIDsUnique(contract *Contract) error {
	ids := make([]string, 0, len(contract.Bindings))
	for _, binding := range contract.Bindings {
		ids = append(ids, binding.ID)
	}
	if duplicate, found := firstDuplicate(ids); found {
		return fmt.Errorf("%w: binding %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

func checkBindingsHaveOperations(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if len(binding.Operations) == 0 {
			return fmt.Errorf("%w: binding %q declares no operation", ErrInvalid, binding.ID)
		}
	}
	return nil
}

func checkOperationsKnown(contract *Contract) error {
	for _, binding := range contract.Bindings {
		for _, operation := range binding.Operations {
			if !slices.Contains(operations, operation) {
				return fmt.Errorf("%w: binding %q operation %q is not one of %s", ErrInvalid, binding.ID, operation, strings.Join(operations, ", "))
			}
		}
	}
	return nil
}

func checkOperationsUnique(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if duplicate, found := firstDuplicate(binding.Operations); found {
			return fmt.Errorf("%w: binding %q declares operation %q twice", ErrInvalid, binding.ID, duplicate)
		}
	}
	return nil
}

func checkLookupNeedsOperation(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Lookup != nil && !slices.Contains(binding.Operations, OperationLookup) {
			return fmt.Errorf("%w: binding %q declares a lookup method without the lookup operation", ErrInvalid, binding.ID)
		}
	}
	return nil
}

func checkLookupMethodNames(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Lookup != nil && !namePattern.MatchString(binding.Lookup.Method) {
			return fmt.Errorf("%w: binding %q lookup method %q is not a lowercase name", ErrInvalid, binding.ID, binding.Lookup.Method)
		}
	}
	return nil
}

// labelledSlot is one slot of a binding with the label a refusal names it by
// and the field whose key convention it is held to.
type labelledSlot struct {
	label string
	field string
	slot  *Slot
}

// slots lists every slot a contract carries, in file order.
func (contract *Contract) slots() []labelledSlot {
	var all []labelledSlot
	for index := range contract.Bindings {
		binding := &contract.Bindings[index]
		prefix := "binding " + binding.ID + " "
		all = append(all, labelledSlot{prefix + fieldAudience, fieldAudience, &binding.Audience})
		if binding.ResourceKind != nil {
			all = append(all, labelledSlot{prefix + fieldResourceKind, fieldResourceKind, binding.ResourceKind})
		}
		if binding.BindingKey != nil {
			all = append(all, labelledSlot{prefix + fieldBindingKey, fieldBindingKey, binding.BindingKey})
		}
	}
	return all
}

func checkSlotsAreReferences(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.literal != nil {
			return fmt.Errorf("%w: %s slot is {from: <group>/<key>}, not the literal %q; another module's vocabulary is supplied by the composition, never spelled in a module repository", ErrInvalid, entry.label, *entry.slot.literal)
		}
	}
	return nil
}

func checkSlotsCarryOnlyFrom(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.hasExtra {
			return fmt.Errorf("%w: %s slot carries the unknown slot field %q (a slot carries only from)", ErrInvalid, entry.label, entry.slot.extra)
		}
	}
	return nil
}

// checkSlotKeysOnce: a slot naming a key twice says two things, and a decoder
// that keeps assigning honours the last one silently — which selects a
// different audience than the document appears to request.
// checkSlotsDecode: a reference that did not decode as a string — a tagged
// scalar, a nested mapping — is refused here rather than accepted as whatever
// text the node happened to carry.
func checkSlotsDecode(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.malformed != nil {
			return fmt.Errorf("%w: %s slot reference is malformed: %v", ErrInvalid, entry.label, entry.slot.malformed)
		}
	}
	return nil
}

func checkSlotReferences(contract *Contract) error {
	for _, entry := range contract.slots() {
		group, key, found := strings.Cut(entry.slot.From, "/")
		if !found || !namePattern.MatchString(group) || !slotKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: %s slot %q is not <group>/<key>", ErrInvalid, entry.label, entry.slot.From)
		}
	}
	return nil
}

func checkSlotKeyMeanings(contract *Contract) error {
	for _, entry := range contract.slots() {
		suffixes := slotSuffixes[entry.field]
		key := entry.slot.Key()
		if slices.ContainsFunc(suffixes, func(suffix string) bool { return strings.HasSuffix(normalizeKey(key), suffix) }) {
			continue
		}
		spelled := make([]string, 0, len(suffixes))
		for _, suffix := range suffixes {
			spelled = append(spelled, "*"+strings.ToLower(strings.ReplaceAll(suffix, "_", "-")))
		}
		return fmt.Errorf("%w: %s slot %q names a key that is not %s; a slot's key carries its meaning, because no reader can check what the value it resolves to means",
			ErrInvalid, entry.label, entry.slot.From, strings.Join(spelled, " or "))
	}
	return nil
}

// labelledCeiling is one operation's ceiling of a binding, with the label a
// refusal names it by.
type labelledCeiling struct {
	label     string
	binding   *Binding
	operation string
	ceiling   Ceiling
}

// ceilings lists every binding ceiling, in a fixed order.
func (contract *Contract) ceilings() []labelledCeiling {
	var all []labelledCeiling
	for index := range contract.Bindings {
		binding := &contract.Bindings[index]
		for _, operation := range sortedOperations(binding.ScopeCeiling) {
			all = append(all, labelledCeiling{"binding " + binding.ID + " " + operation, binding, operation, binding.ScopeCeiling[operation]})
		}
	}
	return all
}

func checkCeilingsAreLists(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if entry.ceiling.notAList {
			return fmt.Errorf("%w: %s ceiling is not a list; a scope ceiling is a list of actions or of {resource_kind, actions} entries", ErrInvalid, entry.label)
		}
	}
	return nil
}

func checkCeilingEntryFields(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if entry.ceiling.hasUnknownField {
			return fmt.Errorf("%w: %s ceiling carries the unknown scope ceiling field %q (an entry carries resource_kind and actions)", ErrInvalid, entry.label, entry.ceiling.unknownField)
		}
	}
	return nil
}

func checkCeilingEntryShapes(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if entry.ceiling.malformed != nil {
			return fmt.Errorf("%w: %s ceiling entry is malformed: %v", ErrInvalid, entry.label, entry.ceiling.malformed)
		}
	}
	return nil
}

// checkCeilingsOneSpelling holds every ceiling to one spelling — bare actions
// or explicit scopes, never both — whether the mix was written in the file or
// constructed in a Contract value: Resolve emits both collections, so a
// ceiling carrying both would mint scopes no reader ever validated.
func checkCeilingsOneSpelling(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if len(entry.ceiling.Actions) > 0 && len(entry.ceiling.Scopes) > 0 {
			return fmt.Errorf("%w: %s ceiling mixes bare actions with {resource_kind, actions} entries; a scope ceiling is written in one spelling", ErrInvalid, entry.label)
		}
	}
	return nil
}

func checkCeilingsForDeclaredOperations(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if !slices.Contains(entry.binding.Operations, entry.operation) {
			return fmt.Errorf("%w: binding %q names a scope ceiling for %q, an operation it does not declare", ErrInvalid, entry.binding.ID, entry.operation)
		}
	}
	return nil
}

func checkOperationsHaveCeilings(contract *Contract) error {
	for _, binding := range contract.Bindings {
		for _, operation := range binding.Operations {
			// The ceiling is the gate: a binding carrying no scopes for an
			// operation cannot be minted that way at all, so an operation
			// declared without one is a request for nothing.
			if binding.ScopeCeiling[operation].empty() {
				return fmt.Errorf("%w: binding %q declares operation %q with no scope ceiling", ErrInvalid, binding.ID, operation)
			}
		}
	}
	return nil
}

func checkBareActionsNeedKind(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if len(entry.ceiling.Actions) > 0 && entry.binding.ResourceKind == nil {
			// A scope names a resource kind (core's WorkScopeV1 requires one),
			// so a bare action with no kind to qualify it is a request nothing
			// can mint.
			return fmt.Errorf("%w: %s ceiling lists bare actions but the binding declares no resource_kind slot to qualify them; "+
				"add the slot, or spell each scope as {resource_kind: <kind>, actions: [...]}", ErrInvalid, entry.label)
		}
	}
	return nil
}

func checkScopeKindNames(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		for _, scope := range entry.ceiling.Scopes {
			if !namePattern.MatchString(scope.ResourceKind) {
				return fmt.Errorf("%w: %s ceiling resource kind %q is not a lowercase name", ErrInvalid, entry.label, scope.ResourceKind)
			}
		}
	}
	return nil
}

func checkScopeKindsUnique(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		kinds := make([]string, 0, len(entry.ceiling.Scopes))
		for _, scope := range entry.ceiling.Scopes {
			kinds = append(kinds, scope.ResourceKind)
		}
		if duplicate, found := firstDuplicate(kinds); found {
			return fmt.Errorf("%w: %s ceiling names resource kind %q twice", ErrInvalid, entry.label, duplicate)
		}
	}
	return nil
}

func checkScopesHaveActions(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		for _, scope := range entry.ceiling.Scopes {
			if len(scope.Actions) == 0 {
				return fmt.Errorf("%w: %s ceiling resource kind %q permits no action", ErrInvalid, entry.label, scope.ResourceKind)
			}
		}
	}
	return nil
}

func checkDestinationIDNames(contract *Contract) error {
	for _, destination := range contract.Destinations {
		if !namePattern.MatchString(destination.ID) {
			return fmt.Errorf("%w: destination id %q is not a lowercase name", ErrInvalid, destination.ID)
		}
	}
	return nil
}

func checkDestinationIDsUnique(contract *Contract) error {
	ids := make([]string, 0, len(contract.Destinations))
	for _, destination := range contract.Destinations {
		ids = append(ids, destination.ID)
	}
	if duplicate, found := firstDuplicate(ids); found {
		return fmt.Errorf("%w: destination %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

func checkDestinationTargetNames(contract *Contract) error {
	for _, destination := range contract.Destinations {
		for _, part := range []struct{ label, value string }{{"service", destination.Service}, {"endpoint", destination.Endpoint}} {
			if !namePattern.MatchString(part.value) {
				return fmt.Errorf("%w: destination %q %s %q is not a lowercase name", ErrInvalid, destination.ID, part.label, part.value)
			}
		}
	}
	return nil
}

func checkDestinationKindsKnown(contract *Contract) error {
	for _, destination := range contract.Destinations {
		if !slices.Contains(destinationKinds, destination.Kind) {
			return fmt.Errorf("%w: destination %q kind %q is not one of %s", ErrInvalid, destination.ID, destination.Kind, strings.Join(destinationKinds, ", "))
		}
	}
	return nil
}
