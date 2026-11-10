package module

import (
	"fmt"
	"maps"
	"regexp"
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
	ruleWellFormed        = "well-formed"
	ruleSchema            = "schema"
	ruleKnownFields       = "known-fields"
	ruleOwnerAwareBoolean = "owner-aware-boolean"
	ruleSharedBoolean     = "scope-ceiling-shared-boolean"
	ruleOneDocument       = "one-document"
	ruleMappingKeysOnce   = "mapping-keys-once"
	ruleNoMergeKeys       = "no-merge-keys"
	ruleNoNulls           = "no-explicit-nulls"
	ruleKeyIsAName        = "key-is-a-name"
	ruleWholeNumbers      = "numbers-are-whole"

	ruleInstallationStrings        = "installation-declaration-strings"
	ruleInstallationRole           = "installation-role"
	ruleInstallationRoleWhitespace = "installation-role-whitespace"
	ruleRoleReserved               = "role-reserved"
	ruleEdgeModuleName             = "edge-module-name"
	ruleEdgeModuleWhitespace       = "edge-module-whitespace"
	ruleEdgeModuleUnique           = "edge-module-unique"
	ruleEdgePresence               = "edge-presence"
	ruleEdgePresenceOneSpelling    = "edge-presence-one-spelling"
	ruleEdgePresenceDeclared       = "edge-presence-declared"
	ruleEdgePresenceClass          = "edge-presence-class"
	ruleEdgePresenceClassValue     = "edge-presence-class-value"

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
	ruleOneAudienceField      = "binding-one-audience-field"
	ruleKindsNeedAudiences    = "resource-kinds-need-audiences"
	ruleAudiencesNoKind       = "audiences-without-resource-kind"
	ruleAudiencesNoKey        = "audiences-without-binding-key"
	ruleSlotIsAReference      = "slot-is-a-reference"
	ruleSlotCarriesOnlyFrom   = "slot-carries-only-from"
	ruleSlotDecodes           = "slot-decodes"
	ruleSlotSelfBesideFrom    = "slot-self-beside-from"
	ruleSlotSelfIsTrue        = "slot-self-is-true"
	ruleSelfOnlyAudience      = "self-only-audience"
	ruleSlotReference         = "slot-reference"
	ruleSlotKeyMeaning        = "slot-key-meaning"
	ruleOptionalHasASlot      = "optional-binding-has-a-slot"
	ruleSelfNoResourceKind    = "self-audience-no-resource-kind"
	ruleSelfOwnScopes         = "self-audience-own-scopes"
	ruleCeilingIsAList        = "ceiling-is-a-list"
	ruleCeilingEntryFields    = "ceiling-entry-fields"
	ruleCeilingEntryShape     = "ceiling-entry-shape"
	ruleCeilingOneSpelling    = "ceiling-one-spelling"
	ruleAudiencesNoScopes     = "audiences-without-explicit-scopes"
	ruleCeilingForDeclaredOp  = "ceiling-for-declared-operation"
	ruleOperationHasCeiling   = "operation-has-ceiling"
	ruleCallsForDeclaredOp    = "calls-for-declared-operation"
	ruleOperationOneSource    = "operation-one-ceiling-source"
	ruleCallName              = "call-name"
	ruleCallUnique            = "call-unique"
	ruleBareActionsNeedKind   = "bare-actions-need-kind"
	ruleScopeKindName         = "scope-kind-name"
	ruleScopeKindUnique       = "scope-kind-unique"
	ruleScopeHasAction        = "scope-has-action"
	ruleDestinationIDName     = "destination-id-name"
	ruleDestinationIDUnique   = "destination-id-unique"
	ruleDestinationTargetName = "destination-target-name"
	ruleDestinationKindKnown  = "destination-kind-known"
	ruleForeignMethodName     = "foreign-method-name"
	ruleForeignMethodUnique   = "foreign-method-unique"
	ruleForeignMethodRequires = "foreign-method-requires"
	ruleForeignScopeHasAction = "foreign-method-scope-has-action"
	ruleForeignMethodOwnKind  = "foreign-method-own-kind"
	ruleForeignMethodOwnAct   = "foreign-method-own-action"

	ruleRoleName          = "role-name"
	ruleRoleUnique        = "role-unique"
	ruleRoleGrants        = "role-grants"
	ruleRoleKindName      = "role-kind-name"
	ruleRoleKindUnique    = "role-kind-unique"
	ruleRoleKindHasAction = "role-kind-has-action"
	ruleRoleBindingKnown  = "role-binding-known"
	ruleRoleBindingOp     = "role-binding-person-operation"
	ruleRoleBindingDeclOp = "role-binding-declared-operation"
	ruleRoleBindingUnique = "role-binding-unique"
	ruleRoleInstaller     = "role-installer"
	ruleRoleWithinRequest = "role-within-request"
	ruleThroughOnRoles    = "through-on-role-scopes"
	ruleThroughNotOnCalls = "through-not-on-requirements"
	ruleRoleThroughKnown  = "role-through-binding-known"
	ruleRoleThroughPerson = "role-through-person-operation"
	ruleRoleThroughOwn    = "role-through-not-own-kind"
	ruleRoleThroughSelf   = "role-through-not-self"
)

// rules is the order a contract is held to: the document's shape first, then
// each section in the order the file is written — a ceiling's shape before
// its contents, and every action list last, so a structural defect is named
// before a spelling inside it.
func rules() []rule {
	return append([]rule{
		{name: ruleWellFormed, inherent: true, check: func(*Contract) error { return nil }},
		{name: ruleSchema, check: checkSchema},
		{name: ruleKnownFields, check: func(*Contract) error { return nil }},
		{name: ruleOwnerAwareBoolean, check: func(*Contract) error { return nil }},
		{name: ruleSharedBoolean, check: func(*Contract) error { return nil }},
		{name: ruleOneDocument, check: func(*Contract) error { return nil }},
		{name: ruleMappingKeysOnce, check: func(*Contract) error { return nil }},
		{name: ruleNoMergeKeys, check: func(*Contract) error { return nil }},
		{name: ruleNoNulls, check: func(*Contract) error { return nil }},
		{name: ruleKeyIsAName, check: func(*Contract) error { return nil }},
		{name: ruleWholeNumbers, check: func(*Contract) error { return nil }},
		{name: ruleInstallationStrings, check: func(*Contract) error { return nil }},
		{name: ruleInstallationRoleWhitespace, check: checkInstallationRoleWhitespace},
		{name: ruleInstallationRole, check: checkInstallationRole},
		{name: ruleRoleReserved, check: checkRoleReserved},
		{name: ruleEdgeModuleName, check: checkEdgeModuleNames},
		{name: ruleEdgeModuleWhitespace, check: checkEdgeModuleWhitespace},
		{name: ruleEdgeModuleUnique, check: checkEdgeModulesUnique},
		{name: ruleEdgePresenceOneSpelling, check: checkEdgePresenceOneSpelling},
		{name: ruleEdgePresenceDeclared, check: checkEdgePresenceDeclared},
		{name: ruleEdgePresence, check: checkEdgePresence},
		{name: ruleEdgePresenceClass, check: checkEdgePresenceClasses},
		{name: ruleEdgePresenceClassValue, check: checkEdgePresenceClassValues},
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
		{name: ruleOneAudienceField, check: checkOneAudienceField},
		{name: ruleKindsNeedAudiences, check: checkKindsNeedAudiences},
		{name: ruleAudiencesNoKind, check: checkAudiencesWithoutResourceKind},
		{name: ruleAudiencesNoKey, check: checkAudiencesWithoutBindingKey},
		{name: ruleSlotIsAReference, check: checkSlotsAreReferences},
		{name: ruleSlotCarriesOnlyFrom, check: checkSlotsCarryOnlyFrom},
		{name: ruleSlotDecodes, check: checkSlotsDecode},
		{name: ruleSlotSelfBesideFrom, check: checkSlotsSelfBesideFrom},
		{name: ruleSlotSelfIsTrue, check: checkSlotsSelfIsTrue},
		{name: ruleSelfOnlyAudience, check: checkSelfOnlyAudience},
		{name: ruleSlotReference, check: checkSlotReferences},
		{name: ruleSlotKeyMeaning, check: checkSlotKeyMeanings},
		{name: ruleOptionalHasASlot, check: checkOptionalBindingsHaveASlot},
		{name: ruleCeilingIsAList, check: checkCeilingsAreLists},
		{name: ruleCeilingEntryFields, check: checkCeilingEntryFields},
		{name: ruleCeilingEntryShape, check: checkCeilingEntryShapes},
		{name: ruleCeilingOneSpelling, check: checkCeilingsOneSpelling},
		{name: ruleAudiencesNoScopes, check: checkAudiencesWithoutExplicitScopes},
		{name: ruleCeilingForDeclaredOp, check: checkCeilingsForDeclaredOperations},
		{name: ruleCallsForDeclaredOp, check: checkCallsForDeclaredOperations},
		{name: ruleOperationOneSource, check: checkOperationsHaveOneCeilingSource},
		{name: ruleOperationHasCeiling, check: checkOperationsHaveCeilings},
		{name: ruleCallName, check: checkCallNames},
		{name: ruleCallUnique, check: checkCallsUnique},
		{name: ruleSelfNoResourceKind, check: checkSelfAudienceNoResourceKind},
		{name: ruleSelfOwnScopes, check: checkSelfAudienceOwnScopes},
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
		{name: ruleForeignMethodName, check: checkForeignMethodNames},
		{name: ruleForeignMethodUnique, check: checkForeignMethodsUnique},
		{name: ruleForeignMethodRequires, check: checkForeignMethodsRequire},
		{name: ruleForeignScopeHasAction, check: checkForeignScopesHaveActions},
		{name: ruleForeignMethodOwnKind, check: checkForeignMethodsOwnKinds},
		{name: ruleForeignMethodOwnAct, check: checkForeignMethodsOwnActions},
		{name: ruleThroughNotOnCalls, check: checkThroughNotOnRequirements},
		{name: ruleRoleName, check: checkRoleNames},
		{name: ruleRoleUnique, check: checkRolesUnique},
		{name: ruleRoleGrants, check: checkRolesGrant},
		{name: ruleRoleKindName, check: checkRoleKindNames},
		{name: ruleRoleKindUnique, check: checkRoleKindsUnique},
		{name: ruleRoleKindHasAction, check: checkRoleKindsHaveActions},
		{name: ruleRoleBindingKnown, check: checkRoleBindingsKnown},
		{name: ruleRoleBindingOp, check: checkRoleBindingsPersonOperations},
		{name: ruleRoleBindingDeclOp, check: checkRoleBindingsDeclaredOperations},
		{name: ruleRoleBindingUnique, check: checkRoleBindingsUnique},
		{name: ruleRoleInstaller, check: checkInstallerRole},
		{name: ruleThroughOnRoles, check: checkThroughOnRoleScopes},
		{name: ruleRoleThroughKnown, check: checkRoleThroughBindingsKnown},
		{name: ruleRoleThroughSelf, check: checkRoleThroughNotSelf},
		{name: ruleRoleThroughPerson, check: checkRoleThroughPersonOperations},
		{name: ruleRoleThroughOwn, check: checkRoleThroughNotOwnKinds},
		{name: ruleRoleWithinRequest, check: checkRolesWithinRequest},
	}, governanceRules()...)
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
	for _, role := range contract.Roles {
		for _, list := range role.scopeLists() {
			for _, scope := range list.scopes {
				lists = append(lists, struct {
					label   string
					actions []string
				}{"role " + role.Name + " " + list.label + scope.ResourceKind, scope.Actions})
			}
		}
	}
	for _, scope := range contract.governanceScopes() {
		lists = append(lists, struct {
			label   string
			actions []string
		}{"source_governance " + scope.ResourceKind, scope.Actions})
	}
	return lists
}

// sortedOperations lists a ceiling map's keys in a fixed order, so a refusal
// names the same operation on every run.
func sortedOperations[V any](byOperation map[string]V) []string {
	return slices.Sorted(maps.Keys(byOperation))
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
		for _, declared := range binding.fieldSlots() {
			all = append(all, labelledSlot{prefix + declared.field, declared.field, declared.slot})
		}
	}
	return all
}

// checkOneAudienceField: a binding is addressed to one audience or to a list
// of them, and a binding writing both says two things about where it may be
// exercised.
func checkOneAudienceField(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Audiences != nil && binding.Audience.declared() {
			return fmt.Errorf("%w: binding %q declares both audience and audiences; a binding is addressed to one audience or to a list of them", ErrInvalid, binding.ID)
		}
	}
	return nil
}

// checkKindsNeedAudiences: per-audience kinds qualify an audience list and
// nothing else.
func checkKindsNeedAudiences(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Audiences == nil && binding.ResourceKinds != nil {
			return fmt.Errorf("%w: binding %q declares resource_kinds without audiences; per-audience kinds qualify an audience list, and a binding with one audience names its kind in resource_kind", ErrInvalid, binding.ID)
		}
	}
	return nil
}

// checkAudiencesWithoutResourceKind: one resource kind beside an audience
// list would name one module's kind at every other.
func checkAudiencesWithoutResourceKind(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Audiences != nil && binding.ResourceKind != nil {
			return fmt.Errorf("%w: binding %q declares resource_kind beside audiences; each audience's kinds are its own, so a list qualifies its bare actions per audience in resource_kinds", ErrInvalid, binding.ID)
		}
	}
	return nil
}

// checkAudiencesWithoutBindingKey: a binding with an audience list is
// delivered once per listed audience, and one key cannot install them all.
func checkAudiencesWithoutBindingKey(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Audiences != nil && binding.BindingKey != nil {
			return fmt.Errorf("%w: binding %q declares binding_key beside audiences; it is delivered as one binding per listed audience, and one key cannot install them all", ErrInvalid, binding.ID)
		}
	}
	return nil
}

func checkSlotsAreReferences(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.literal != nil {
			hint := ""
			if entry.field == fieldAudience {
				hint = "; a binding addressed to the module itself writes audience: {self: true}"
			}
			return fmt.Errorf("%w: %s slot is {from: <group>/<key>}, not the literal %q; another module's vocabulary is supplied by the composition, never spelled in a module repository%s", ErrInvalid, entry.label, *entry.slot.literal, hint)
		}
	}
	return nil
}

func checkSlotsCarryOnlyFrom(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.hasExtra {
			return fmt.Errorf("%w: %s slot carries the unknown slot field %q (a slot carries only from, or an audience only self)", ErrInvalid, entry.label, entry.slot.extra)
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

// checkSlotsSelfBesideFrom: a slot is a composition reference or the module
// itself, never both.
func checkSlotsSelfBesideFrom(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.Self && entry.slot.From != "" {
			return fmt.Errorf("%w: %s slot writes self beside from %q; a slot is the composition's value or the module itself, never both", ErrInvalid, entry.label, entry.slot.From)
		}
	}
	return nil
}

// checkSlotsSelfIsTrue: self is written only as self: true. self: false is a
// slot naming what it is not, and says nothing about what it is.
func checkSlotsSelfIsTrue(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.selfWritten && !entry.slot.Self {
			return fmt.Errorf("%w: %s slot writes self: false; a slot that is not the module itself writes from, and self is written only as self: true", ErrInvalid, entry.label)
		}
	}
	return nil
}

// checkSelfOnlyAudience: the module itself is an audience — its principal —
// and never a resource kind, a binding key, or a list to be delivered once
// per entry.
func checkSelfOnlyAudience(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.Self && entry.field != fieldAudience {
			return fmt.Errorf("%w: %s slot is {self: true}; only an audience may be the module itself — its principal is no %s", ErrInvalid, entry.label, entry.field)
		}
	}
	return nil
}

// checkOptionalBindingsHaveASlot: an optional binding is left out by a
// composition that fills none of its slots, so one with no slot the
// composition fills — addressed to the module itself and nothing else — could
// never be left out, and would read as optional while being delivered always.
func checkOptionalBindingsHaveASlot(contract *Contract) error {
	for index := range contract.Bindings {
		binding := &contract.Bindings[index]
		if binding.Optional && len(binding.composedSlots()) == 0 {
			return fmt.Errorf("%w: optional binding %q has no slot the composition fills, so no composition can leave it out; a binding addressed to the module itself with nothing else to fill is mandatory", ErrInvalid, binding.ID)
		}
	}
	return nil
}

// checkSelfAudienceNoResourceKind: a binding addressed to the module itself
// asks for the module's own vocabulary, which it declares in scope_ceilings
// and names literally — never reads from the composition.
func checkSelfAudienceNoResourceKind(contract *Contract) error {
	for _, binding := range contract.Bindings {
		if binding.Audience.Self && binding.ResourceKind != nil {
			return fmt.Errorf("%w: binding %q is addressed to the module itself and declares a resource_kind slot; its kinds are its own scope_ceilings, named literally as {resource_kind, actions} entries", ErrInvalid, binding.ID)
		}
	}
	return nil
}

// checkSelfAudienceOwnScopes: every scope a binding addressed to the module
// itself names is within the module's own ceiling — a scope at itself beyond
// what it contributes is one no host could grant.
func checkSelfAudienceOwnScopes(contract *Contract) error {
	own := contract.ownCeilings()
	for _, entry := range contract.ceilings() {
		if !entry.binding.Audience.Self {
			continue
		}
		for _, scope := range entry.ceiling.Scopes {
			actions, declared := own[scope.ResourceKind]
			for _, action := range scope.Actions {
				if !declared || !slices.Contains(actions, action) {
					return fmt.Errorf("%w: binding %q is addressed to the module itself and its %s ceiling names %s:%s, beyond the module's own scope_ceilings", ErrInvalid, entry.binding.ID, entry.operation, scope.ResourceKind, action)
				}
			}
		}
	}
	return nil
}

func checkSlotReferences(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.Self {
			continue
		}
		group, key, found := strings.Cut(entry.slot.From, "/")
		if !found || !namePattern.MatchString(group) || !slotKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: %s slot %q is not <group>/<key>", ErrInvalid, entry.label, entry.slot.From)
		}
	}
	return nil
}

func checkSlotKeyMeanings(contract *Contract) error {
	for _, entry := range contract.slots() {
		if entry.slot.Self {
			continue
		}
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

// checkAudiencesWithoutExplicitScopes: an explicit scope names its kind
// literally, and beside an audience list that one kind would be asked for at
// every listed audience — one module's vocabulary at every other, exactly what
// resource_kind is refused there for.
func checkAudiencesWithoutExplicitScopes(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if entry.binding.Audiences != nil && len(entry.ceiling.Scopes) > 0 {
			return fmt.Errorf("%w: %s ceiling spells {resource_kind, actions} scopes beside audiences; each audience's kinds are its own, so a list names its calls or qualifies bare actions per audience in resource_kinds", ErrInvalid, entry.label)
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
			if binding.ScopeCeiling[operation].empty() && len(binding.Calls[operation]) == 0 {
				return fmt.Errorf("%w: binding %q declares operation %q with no scope ceiling and no calls", ErrInvalid, binding.ID, operation)
			}
		}
	}
	return nil
}

func checkCallsForDeclaredOperations(contract *Contract) error {
	for _, binding := range contract.Bindings {
		for _, operation := range sortedOperations(binding.Calls) {
			if !slices.Contains(binding.Operations, operation) {
				return fmt.Errorf("%w: binding %q names calls for %q, an operation it does not declare", ErrInvalid, binding.ID, operation)
			}
		}
	}
	return nil
}

func checkOperationsHaveOneCeilingSource(contract *Contract) error {
	for _, binding := range contract.Bindings {
		for _, operation := range sortedOperations(binding.Calls) {
			// One source per operation: a ceiling written beside calls would
			// make the reader guess whether the calls bound it or widened it.
			if _, written := binding.ScopeCeiling[operation]; written && len(binding.Calls[operation]) > 0 {
				return fmt.Errorf("%w: binding %q operation %q names both a scope ceiling and calls; an operation's ceiling is one or the other", ErrInvalid, binding.ID, operation)
			}
		}
	}
	return nil
}

// callPattern is a call as an audience's method policy names it: a gRPC method
// (/<package>.<Service>/<Method>) or an HTTP route (<VERB> <path>).
var callPattern = regexp.MustCompile(`^(/([A-Za-z_][A-Za-z0-9_]*\.)*[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*|(GET|HEAD|POST|PUT|PATCH|DELETE) /[^\s]*)$`)

func checkCallNames(contract *Contract) error {
	for _, binding := range contract.Bindings {
		for _, operation := range sortedOperations(binding.Calls) {
			for _, call := range binding.Calls[operation] {
				if !callPattern.MatchString(call) {
					return fmt.Errorf("%w: binding %q operation %q call %q is neither a gRPC method (/<package>.<Service>/<Method>) nor an HTTP route (<VERB> <path>)", ErrInvalid, binding.ID, operation, call)
				}
			}
		}
	}
	return nil
}

func checkCallsUnique(contract *Contract) error {
	for _, binding := range contract.Bindings {
		for _, operation := range sortedOperations(binding.Calls) {
			seen := map[string]bool{}
			for _, call := range binding.Calls[operation] {
				if seen[call] {
					return fmt.Errorf("%w: binding %q operation %q names call %q twice", ErrInvalid, binding.ID, operation, call)
				}
				seen[call] = true
			}
		}
	}
	return nil
}

func checkBareActionsNeedKind(contract *Contract) error {
	for _, entry := range contract.ceilings() {
		if len(entry.ceiling.Actions) > 0 && entry.binding.ResourceKind == nil && entry.binding.ResourceKinds == nil {
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

// grpcMethodPattern is a full gRPC method name: /<package>.<Service>/<Method>.
var grpcMethodPattern = regexp.MustCompile(`^/([A-Za-z_][A-Za-z0-9_]*\.)*[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*$`)

func checkForeignMethodNames(contract *Contract) error {
	for _, method := range contract.ForeignMethods {
		if !grpcMethodPattern.MatchString(method.Method) {
			return fmt.Errorf("%w: foreign method %q is not a full gRPC method name (/<package>.<Service>/<Method>)", ErrInvalid, method.Method)
		}
	}
	return nil
}

func checkForeignMethodsUnique(contract *Contract) error {
	names := make([]string, 0, len(contract.ForeignMethods))
	for _, method := range contract.ForeignMethods {
		names = append(names, method.Method)
	}
	if duplicate, found := firstDuplicate(names); found {
		return fmt.Errorf("%w: foreign method %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

func checkForeignMethodsRequire(contract *Contract) error {
	for _, method := range contract.ForeignMethods {
		// A method that requires nothing would be open to any checked caller
		// by omission; a foreign method states what it requires.
		if len(method.Requires) == 0 {
			return fmt.Errorf("%w: foreign method %q requires nothing; it names what a call must hold", ErrInvalid, method.Method)
		}
	}
	return nil
}

func checkForeignScopesHaveActions(contract *Contract) error {
	for _, method := range contract.ForeignMethods {
		for _, scope := range method.Requires {
			if len(scope.Actions) == 0 {
				return fmt.Errorf("%w: foreign method %q requires %q with no action", ErrInvalid, method.Method, scope.ResourceKind)
			}
		}
	}
	return nil
}

// ownCeilings is the module's own vocabulary: each kind and its actions.
func (contract *Contract) ownCeilings() map[string][]string {
	own := map[string][]string{}
	for _, ceiling := range contract.ScopeCeilings {
		own[ceiling.ResourceKind] = ceiling.Actions
	}
	return own
}

func checkForeignMethodsOwnKinds(contract *Contract) error {
	own := contract.ownCeilings()
	for _, method := range contract.ForeignMethods {
		for _, scope := range method.Requires {
			// A module decides calls to itself in its own vocabulary: a kind
			// it does not declare is one no caller could be granted here.
			if _, declared := own[scope.ResourceKind]; !declared {
				return fmt.Errorf("%w: foreign method %q requires %q, which is not one of the module's own scope ceilings", ErrInvalid, method.Method, scope.ResourceKind)
			}
		}
	}
	return nil
}

func checkForeignMethodsOwnActions(contract *Contract) error {
	own := contract.ownCeilings()
	for _, method := range contract.ForeignMethods {
		for _, scope := range method.Requires {
			actions, declared := own[scope.ResourceKind]
			if !declared {
				continue
			}
			for _, action := range scope.Actions {
				if !slices.Contains(actions, action) {
					return fmt.Errorf("%w: foreign method %q requires %s:%s, beyond the module's own ceiling on %s", ErrInvalid, method.Method, scope.ResourceKind, action, scope.ResourceKind)
				}
			}
		}
	}
	return nil
}

func checkRoleNames(contract *Contract) error {
	for _, role := range contract.Roles {
		if !namePattern.MatchString(role.Name) {
			return fmt.Errorf("%w: role %q is not a lowercase name", ErrInvalid, role.Name)
		}
	}
	return nil
}

func checkRolesUnique(contract *Contract) error {
	names := make([]string, 0, len(contract.Roles))
	for _, role := range contract.Roles {
		names = append(names, role.Name)
	}
	if duplicate, found := firstDuplicate(names); found {
		return fmt.Errorf("%w: role %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

// roleScopes is one of a role's lists of literal scopes, with the label a
// refusal names it by: "" for scopes, "installation-owned " for
// installation_owned_scopes.
type roleScopes struct {
	label  string
	scopes []CeilingScope
}

// scopeLists are the role's literal scopes, both lists, each held to the same
// rules: an installation-owned scope differs only in what the authority
// resolves it to, never in what a role may name. A scope reached through a
// binding (through, on scopes alone) is the one exception to what it is held
// within: the authority holds it downstream (checkRolesWithinRequest).
func (role Role) scopeLists() []roleScopes {
	return []roleScopes{{"", role.Scopes}, {"installation-owned ", role.InstallationOwnedScopes}}
}

func checkRolesGrant(contract *Contract) error {
	for _, role := range contract.Roles {
		if len(role.Scopes) == 0 && len(role.InstallationOwnedScopes) == 0 && len(role.Bindings) == 0 {
			return fmt.Errorf("%w: role %q grants nothing", ErrInvalid, role.Name)
		}
	}
	return nil
}

func checkRoleKindNames(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, list := range role.scopeLists() {
			for _, scope := range list.scopes {
				if !namePattern.MatchString(scope.ResourceKind) {
					return fmt.Errorf("%w: role %q %sresource kind %q is not a lowercase name", ErrInvalid, role.Name, list.label, scope.ResourceKind)
				}
			}
		}
	}
	return nil
}

// checkRoleKindsUnique: each list names a kind once at each place it is
// reached — on the unit, or through one binding. A kind may appear in both
// lists, each granting its own actions: on the whole kind, and on what the
// installation owns of it; and among scopes both directly and through a
// binding, or through two bindings, each reaching it somewhere else.
func checkRoleKindsUnique(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, list := range role.scopeLists() {
			seen := map[[2]string]bool{}
			for _, scope := range list.scopes {
				key := [2]string{scope.ResourceKind, scope.Through}
				if !seen[key] {
					seen[key] = true
					continue
				}
				where := ""
				if scope.Through != "" {
					where = fmt.Sprintf(" through %q", scope.Through)
				}
				return fmt.Errorf("%w: role %q names %sresource kind %q%s twice", ErrInvalid, role.Name, list.label, scope.ResourceKind, where)
			}
		}
	}
	return nil
}

func checkRoleKindsHaveActions(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, list := range role.scopeLists() {
			for _, scope := range list.scopes {
				if len(scope.Actions) == 0 {
					return fmt.Errorf("%w: role %q grants %sresource kind %q no action", ErrInvalid, role.Name, list.label, scope.ResourceKind)
				}
			}
		}
	}
	return nil
}

// declaredBinding is the contract's binding with id, or nil.
func (contract *Contract) declaredBinding(id string) *Binding {
	for index := range contract.Bindings {
		if contract.Bindings[index].ID == id {
			return &contract.Bindings[index]
		}
	}
	return nil
}

func checkRoleBindingsKnown(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, granted := range role.Bindings {
			if contract.declaredBinding(granted.Binding) == nil {
				return fmt.Errorf("%w: role %q grants binding %q, which the contract does not declare", ErrInvalid, role.Name, granted.Binding)
			}
		}
	}
	return nil
}

// checkRoleBindingsPersonOperations: a role is what a person holds, so it
// grants only an operation a person causes; a headless operation has no
// person in it.
func checkRoleBindingsPersonOperations(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, granted := range role.Bindings {
			if granted.Operation != OperationInvoke && granted.Operation != OperationLookup {
				return fmt.Errorf("%w: role %q grants binding %q operation %q; a role grants %s or %s, the operations a person causes", ErrInvalid, role.Name, granted.Binding, granted.Operation, OperationInvoke, OperationLookup)
			}
		}
	}
	return nil
}

func checkRoleBindingsDeclaredOperations(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, granted := range role.Bindings {
			if binding := contract.declaredBinding(granted.Binding); binding != nil && !slices.Contains(binding.Operations, granted.Operation) {
				return fmt.Errorf("%w: role %q grants binding %q operation %q, an operation the binding does not declare", ErrInvalid, role.Name, granted.Binding, granted.Operation)
			}
		}
	}
	return nil
}

func checkRoleBindingsUnique(contract *Contract) error {
	for _, role := range contract.Roles {
		granted := make([]string, 0, len(role.Bindings))
		for _, entry := range role.Bindings {
			granted = append(granted, entry.Binding+" "+entry.Operation)
		}
		if duplicate, found := firstDuplicate(granted); found {
			return fmt.Errorf("%w: role %q grants binding operation %q twice", ErrInvalid, role.Name, duplicate)
		}
	}
	return nil
}

func checkInstallerRole(contract *Contract) error {
	if len(contract.Roles) == 0 {
		return nil
	}
	for _, role := range contract.Roles {
		if role.Name == InstallerRole {
			return nil
		}
	}
	return fmt.Errorf("%w: the contract declares %d role(s) and none is %q; whoever installs the unit holds it", ErrInvalid, len(contract.Roles), InstallerRole)
}

// grantable is what a role may grant: the module's own vocabulary, and each
// scope a binding spells out for an operation a person causes (invoke,
// lookup) — the permissions at another module the unit asks for on a person's
// behalf. A headless operation has no person in it, so nothing a person holds
// comes from one; a bare action or a call names its kind only when the
// composition resolves it, so a role cannot be held to it here — a role grants
// those by naming the binding in its bindings, resolved with it.
func (contract *Contract) grantable() map[string][]string {
	grantable := map[string][]string{}
	for kind, actions := range contract.ownCeilings() {
		grantable[kind] = slices.Clone(actions)
	}
	for _, binding := range contract.Bindings {
		for _, operation := range []string{OperationInvoke, OperationLookup} {
			for _, scope := range binding.ScopeCeiling[operation].Scopes {
				grantable[scope.ResourceKind] = append(grantable[scope.ResourceKind], scope.Actions...)
			}
		}
	}
	return grantable
}

func checkRolesWithinRequest(contract *Contract) error {
	grantable := contract.grantable()
	for _, role := range contract.Roles {
		for _, list := range role.scopeLists() {
			for _, scope := range list.scopes {
				if scope.Through != "" {
					continue // reached downstream of its binding; the authority holds it there
				}
				for _, action := range scope.Actions {
					if !slices.Contains(grantable[scope.ResourceKind], action) {
						return fmt.Errorf("%w: role %q grants %s%s:%s, which is neither within the module's own scope ceilings nor a scope one of its bindings spells out for invoke or lookup (a role grants what a binding's calls require by naming the binding in its bindings)", ErrInvalid, role.Name, list.label, scope.ResourceKind, action)
					}
				}
			}
		}
	}
	return nil
}

// checkThroughOnRoleScopes: through names how a person's role reaches a
// kind, so only a role's scopes carry it — never an installation-owned scope,
// whose ids are its own installation's, nor a binding ceiling's scope, which
// is what the binding itself is issued (constructed: Parse refuses the field
// there as unknown).
func checkThroughOnRoleScopes(contract *Contract) error {
	type placed struct {
		label string
		scope CeilingScope
	}
	var scopes []placed
	for _, role := range contract.Roles {
		for _, scope := range role.InstallationOwnedScopes {
			scopes = append(scopes, placed{fmt.Sprintf("role %q installation-owned scope", role.Name), scope})
		}
	}
	for _, entry := range contract.ceilings() {
		for _, scope := range entry.ceiling.Scopes {
			scopes = append(scopes, placed{entry.label + " ceiling scope", scope})
		}
	}
	for _, entry := range scopes {
		if entry.scope.Through != "" {
			return fmt.Errorf("%w: %s %s names through %q; only a role's scopes are reached through a binding", ErrInvalid, entry.label, entry.scope.ResourceKind, entry.scope.Through)
		}
	}
	return nil
}

// checkThroughNotOnRequirements: what a call to the module requires is in its
// own vocabulary, decided where it is served, so it is reached through nothing.
func checkThroughNotOnRequirements(contract *Contract) error {
	for _, method := range contract.ForeignMethods {
		for _, scope := range method.Requires {
			if scope.Through != "" {
				return fmt.Errorf("%w: foreign method %q requires %s through %q; a requirement is the module's own, reached through nothing", ErrInvalid, method.Method, scope.ResourceKind, scope.Through)
			}
		}
	}
	return nil
}

// checkRoleThroughBindingsKnown: a scope reached through a binding names one
// the unit declares.
func checkRoleThroughBindingsKnown(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, scope := range role.Scopes {
			if scope.Through != "" && contract.declaredBinding(scope.Through) == nil {
				return fmt.Errorf("%w: role %q grants %s through binding %q, which the contract does not declare", ErrInvalid, role.Name, scope.ResourceKind, scope.Through)
			}
		}
	}
	return nil
}

// checkRoleThroughPersonOperations: a person's capability travels a binding
// only on an operation a person causes, so a scope is reached only through a
// binding declaring invoke or lookup.
func checkRoleThroughPersonOperations(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, scope := range role.Scopes {
			binding := contract.declaredBinding(scope.Through)
			if scope.Through == "" || binding == nil {
				continue
			}
			if !slices.Contains(binding.Operations, OperationInvoke) && !slices.Contains(binding.Operations, OperationLookup) {
				return fmt.Errorf("%w: role %q grants %s through binding %q, which declares neither %s nor %s; a person's capability travels only the operations a person causes", ErrInvalid, role.Name, scope.ResourceKind, scope.Through, OperationInvoke, OperationLookup)
			}
		}
	}
	return nil
}

// checkRoleThroughNotSelf: a binding addressed to the unit itself has no
// downstream; what a role grants on the unit it grants directly, held to the
// unit's own ceilings.
func checkRoleThroughNotSelf(contract *Contract) error {
	for _, role := range contract.Roles {
		for _, scope := range role.Scopes {
			if binding := contract.declaredBinding(scope.Through); scope.Through != "" && binding != nil && binding.Audience.Self {
				return fmt.Errorf("%w: role %q grants %s through binding %q, which is addressed to the module itself; a scope on the module is granted directly, within its own scope_ceilings", ErrInvalid, role.Name, scope.ResourceKind, scope.Through)
			}
		}
	}
	return nil
}

// checkRoleThroughNotOwnKinds: the unit serves its own kinds here, never
// downstream of a binding, so a scope of one is granted directly and held to
// the unit's own ceiling rather than reached through anything.
func checkRoleThroughNotOwnKinds(contract *Contract) error {
	own := contract.ownCeilings()
	for _, role := range contract.Roles {
		for _, scope := range role.Scopes {
			if _, declared := own[scope.ResourceKind]; scope.Through != "" && declared {
				return fmt.Errorf("%w: role %q grants %s through binding %q, a kind of the module's own; it is granted directly, within its own scope_ceilings", ErrInvalid, role.Name, scope.ResourceKind, scope.Through)
			}
		}
	}
	return nil
}

func checkInstallationRoleWhitespace(contract *Contract) error {
	if strings.TrimSpace(contract.InstallationRole) != contract.InstallationRole {
		return fmt.Errorf("%w: installation_role %q has surrounding whitespace; declare the exact role name or none", ErrInvalid, contract.InstallationRole)
	}
	return nil
}

func checkInstallationRole(contract *Contract) error {
	if contract.InstallationRole == "" || contract.InstallationRole == "none" {
		return nil
	}
	names := make([]string, 0, len(contract.Roles))
	for _, role := range contract.Roles {
		if role.Name == contract.InstallationRole {
			return nil
		}
		names = append(names, role.Name)
	}
	return fmt.Errorf("%w: installation_role %q is not a declared role; declared roles: %v (or declare none)", ErrInvalid, contract.InstallationRole, names)
}

func checkRoleReserved(contract *Contract) error {
	for _, role := range contract.Roles {
		if role.Name == "none" {
			return fmt.Errorf("%w: role name none is reserved for the installation_role declaration of no standing grant", ErrInvalid)
		}
	}
	return nil
}

func checkEdgeModuleNames(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		if edge.Module == "" {
			return fmt.Errorf("%w: edge_dependencies module must be non-empty", ErrInvalid)
		}
	}
	return nil
}

func checkEdgeModuleWhitespace(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		if strings.TrimSpace(edge.Module) != edge.Module {
			return fmt.Errorf("%w: edge_dependencies module %q has surrounding whitespace", ErrInvalid, edge.Module)
		}
	}
	return nil
}

func checkEdgeModulesUnique(contract *Contract) error {
	names := make([]string, 0, len(contract.EdgeDependencies))
	for _, edge := range contract.EdgeDependencies {
		names = append(names, edge.Module)
	}
	if duplicate, found := firstDuplicate(names); found {
		return fmt.Errorf("%w: edge_dependencies module %q is declared twice", ErrInvalid, duplicate)
	}
	return nil
}

// checkEdgePresenceOneSpelling holds an edge to ONE of the two spellings of
// its presence. Two answers to one question let whichever spelling a reader
// happened to prefer be the answer — which is how an edge ends up optional in
// production, because someone added the per-class form and left the old line
// above it.
func checkEdgePresenceOneSpelling(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		if edge.Presence != "" && edge.perClass() {
			return fmt.Errorf("%w: edge_dependencies module %q declares both presence and presence_by_class; a presence is declared in one spelling — presence for the same presence everywhere, presence_by_class for a presence that differs along the promotion path", ErrInvalid, edge.Module)
		}
	}
	return nil
}

// checkEdgePresenceDeclared refuses an edge that declares neither spelling. An
// edge dependency's presence has no default, in either spelling: a module
// either must resolve or need not, and silence is not the second one.
func checkEdgePresenceDeclared(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		if edge.Presence == "" && !edge.perClass() {
			return fmt.Errorf("%w: edge_dependencies module %q declares no presence; declare presence: %s or %s, or presence_by_class naming the classes its presence differs by", ErrInvalid, edge.Module, PresenceRequired, PresenceOptional)
		}
	}
	return nil
}

func checkEdgePresence(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		if edge.perClass() {
			// The per-class spelling's values are their own rule.
			continue
		}
		if edge.Presence != PresenceRequired && edge.Presence != PresenceOptional {
			return fmt.Errorf("%w: edge_dependencies module %q presence %q must be required or optional", ErrInvalid, edge.Module, edge.Presence)
		}
	}
	return nil
}

// checkEdgePresenceClasses holds every key of a per-class presence to the
// environment-class vocabulary, so a contract cannot declare a presence for
// "prod" or "dev" and have it answer for nothing: the class it names is never
// the class a deliverer resolves, and the refusal would come at delivery
// against a misspelling the contract could have refused.
func checkEdgePresenceClasses(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		for _, class := range slices.Sorted(maps.Keys(edge.PresenceByClass)) {
			if !KnownEnvironmentClass(class) {
				return fmt.Errorf("%w: edge_dependencies module %q declares a presence for %q, which is not an environment class (%s)", ErrInvalid, edge.Module, class, strings.Join(EnvironmentClasses(), ", "))
			}
		}
	}
	return nil
}

func checkEdgePresenceClassValues(contract *Contract) error {
	for _, edge := range contract.EdgeDependencies {
		for _, class := range slices.Sorted(maps.Keys(edge.PresenceByClass)) {
			if presence := edge.PresenceByClass[class]; presence != PresenceRequired && presence != PresenceOptional {
				return fmt.Errorf("%w: edge_dependencies module %q presence %q in class %s must be required or optional", ErrInvalid, edge.Module, presence, class)
			}
		}
	}
	return nil
}

// checkGovernanceHasScopes: a declaration names at least one scope. An empty
// one would read as "declared" and govern for nobody, which is what leaving it
// out already says.
func checkGovernanceHasScopes(contract *Contract) error {
	if contract.SourceGovernance != nil && len(contract.SourceGovernance.Scopes) == 0 {
		return fmt.Errorf("%w: source_governance names no scope; leave it out to govern no source", ErrInvalid)
	}
	return nil
}

func checkGovernanceScopesHaveActions(contract *Contract) error {
	for _, scope := range contract.governanceScopes() {
		if len(scope.Actions) == 0 {
			return fmt.Errorf("%w: source_governance names resource kind %q with no action", ErrInvalid, scope.ResourceKind)
		}
	}
	return nil
}

func checkGovernanceKindsUnique(contract *Contract) error {
	kinds := make([]string, 0, len(contract.governanceScopes()))
	for _, scope := range contract.governanceScopes() {
		kinds = append(kinds, scope.ResourceKind)
	}
	if duplicate, found := firstDuplicate(kinds); found {
		return fmt.Errorf("%w: source_governance names resource kind %q twice", ErrInvalid, duplicate)
	}
	return nil
}

// checkGovernanceNotThrough: a governing scope is decided where the unit is
// installed, on its own kinds; one reached through a binding is decided by
// the module downstream of it.
func checkGovernanceNotThrough(contract *Contract) error {
	for _, scope := range contract.governanceScopes() {
		if scope.Through != "" {
			return fmt.Errorf("%w: source_governance names %s through %q; a governing scope is one of the unit's own, reached through nothing", ErrInvalid, scope.ResourceKind, scope.Through)
		}
	}
	return nil
}

func checkGovernanceOwnKinds(contract *Contract) error {
	own := contract.ownCeilings()
	for _, scope := range contract.governanceScopes() {
		if _, declared := own[scope.ResourceKind]; !declared {
			return fmt.Errorf("%w: source_governance names %q, which is not one of the unit's own scope ceilings", ErrInvalid, scope.ResourceKind)
		}
	}
	return nil
}

// checkGovernanceOwnActions: each governing action is one the unit's own
// ceiling declares on the kind, so a role's grant of it is held at the unit
// itself. One a binding alone underwrites is granted at that binding's
// audience, and through an optional binding left out, nowhere: it would
// govern for nobody on the installation.
func checkGovernanceOwnActions(contract *Contract) error {
	own := contract.ownCeilings()
	for _, scope := range contract.governanceScopes() {
		for _, action := range scope.Actions {
			if !slices.Contains(own[scope.ResourceKind], action) {
				return fmt.Errorf("%w: source_governance names %s:%s, which the unit's own scope ceiling does not declare", ErrInvalid, scope.ResourceKind, action)
			}
		}
	}
	return nil
}

// checkGovernanceNotShared: a shared kind is no installation's, so a scope of
// it is issued unselected and would govern every installation's sources at
// once.
func checkGovernanceNotShared(contract *Contract) error {
	for _, scope := range contract.governanceScopes() {
		for _, ceiling := range contract.ScopeCeilings {
			if ceiling.ResourceKind == scope.ResourceKind && ceiling.Shared {
				return fmt.Errorf("%w: source_governance names %q, which the unit declares shared; a governing scope is held on one installation's data", ErrInvalid, scope.ResourceKind)
			}
		}
	}
	return nil
}

// checkGovernanceRoleGrants: each governing scope is one a role grants
// directly in its scopes, so whoever governs holds it on the installation
// through that role. A scope no role grants would govern for nobody.
func checkGovernanceRoleGrants(contract *Contract) error {
	for _, scope := range contract.governanceScopes() {
		for _, action := range scope.Actions {
			granted := slices.ContainsFunc(contract.Roles, func(role Role) bool {
				return slices.ContainsFunc(role.Scopes, func(held CeilingScope) bool {
					return held.Through == "" && held.ResourceKind == scope.ResourceKind && slices.Contains(held.Actions, action)
				})
			})
			if !granted {
				return fmt.Errorf("%w: source_governance names %s:%s, which no role grants in its scopes", ErrInvalid, scope.ResourceKind, action)
			}
		}
	}
	return nil
}
