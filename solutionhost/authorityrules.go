package solutionhost

import (
	"fmt"
	"slices"
)

// authorityRule is one rule the authority document's declarations are held to,
// under a name a refused fixture can protect. It is the build-size rule table's
// shape applied to the half that had none: the checks these replace were inline
// in Validate, so no fixture could say which one refused a document and a
// reader could drop any of them and still pass the kit.
type authorityRule struct {
	name  string
	check func(document *AuthorityDocument) error
}

// The rules, by name. A refused authority fixture names one of these.
const (
	ruleAuthoritySubjectDeclared         = "authority-subject-declared"
	ruleAuthoritySubjectAbsentWhenRemove = "authority-subject-absent-when-removed"
	ruleAuthorityQueueName               = "authority-queue-name"
	ruleAuthorityQueueUnique             = "authority-queue-unique"
	ruleAuthorityNamespaceName           = "authority-namespace-name"
	ruleAuthorityNamespaceUnique         = "authority-namespace-unique"
	ruleAuthorityCeilingKind             = "authority-ceiling-kind"
	ruleAuthorityCeilingKindUnique       = "authority-ceiling-kind-unique"
	ruleAuthorityCeilingActionsDeclared  = "authority-ceiling-actions-declared"
	ruleAuthorityCeilingActionName       = "authority-ceiling-action-name"
	ruleAuthorityCeilingActionUnique     = "authority-ceiling-action-unique"
	ruleAuthorityBindingKeyName          = "authority-binding-key-name"
	ruleAuthorityLookupMethodName        = "authority-lookup-method-name"
)

// authorityRules is the order a document's declarations are held in: that each
// is stated at all, that a withdrawal states none, then each list's entries,
// then the per-binding declarations.
func authorityRules() []authorityRule {
	return []authorityRule{
		{name: ruleAuthoritySubjectDeclared, check: checkAuthoritySubjectDeclared},
		{name: ruleAuthoritySubjectAbsentWhenRemove, check: checkAuthoritySubjectAbsentWhenRemoved},
		{name: ruleAuthorityQueueName, check: checkAuthorityQueueNames},
		{name: ruleAuthorityQueueUnique, check: checkAuthorityQueuesUnique},
		{name: ruleAuthorityNamespaceName, check: checkAuthorityNamespaceNames},
		{name: ruleAuthorityNamespaceUnique, check: checkAuthorityNamespacesUnique},
		{name: ruleAuthorityCeilingKind, check: checkAuthorityCeilingKinds},
		{name: ruleAuthorityCeilingKindUnique, check: checkAuthorityCeilingKindsUnique},
		{name: ruleAuthorityCeilingActionsDeclared, check: checkAuthorityCeilingActionsDeclared},
		{name: ruleAuthorityCeilingActionName, check: checkAuthorityCeilingActionNames},
		{name: ruleAuthorityCeilingActionUnique, check: checkAuthorityCeilingActionsUnique},
		{name: ruleAuthorityBindingKeyName, check: checkAuthorityBindingKeyNames},
		{name: ruleAuthorityLookupMethodName, check: checkAuthorityLookupMethodNames},
	}
}

// authorityRuleNames lists every rule, in order.
func authorityRuleNames() []string {
	all := authorityRules()
	out := make([]string, 0, len(all))
	for _, r := range all {
		out = append(out, r.name)
	}
	return out
}

// validateDeclarations runs every rule but the one named, in order; "" deletes
// none.
func (document *AuthorityDocument) validateDeclarations(without string) error {
	for _, r := range authorityRules() {
		if r.name == without {
			continue
		}
		if err := r.check(document); err != nil {
			return err
		}
	}
	return nil
}

func checkAuthoritySubjectDeclared(document *AuthorityDocument) error {
	for _, declared := range []struct {
		field   string
		missing bool
	}{
		{"queues", document.Queues == nil},
		{"namespaces", document.Namespaces == nil},
		{"scope_ceilings", document.ScopeCeilings == nil},
	} {
		if declared.missing {
			return fmt.Errorf("%w: authority %q declares no %s; the subject module's own declarations are written out, as [] when there are none, so a renderer that dropped one cannot deliver a narrower module than the contract described",
				ErrInvalid, document.Authority, declared.field)
		}
	}
	return nil
}

func checkAuthoritySubjectAbsentWhenRemoved(document *AuthorityDocument) error {
	if !document.Removed {
		return nil
	}
	for _, declared := range []struct {
		field string
		count int
	}{
		{"queues", len(document.Queues)},
		{"namespaces", len(document.Namespaces)},
		{"scope_ceilings", len(document.ScopeCeilings)},
	} {
		if declared.count != 0 {
			return fmt.Errorf("%w: a removed generation declares no %s; a withdrawal that still describes the module it withdraws leaves a host deciding which half meant it",
				ErrInvalid, declared.field)
		}
	}
	return nil
}

func checkAuthorityQueueNames(document *AuthorityDocument) error {
	return eachName(document, "queue", document.Queues)
}

func checkAuthorityQueuesUnique(document *AuthorityDocument) error {
	return eachUnique(document, "queue", document.Queues)
}

func checkAuthorityNamespaceNames(document *AuthorityDocument) error {
	return eachName(document, "namespace", document.Namespaces)
}

func checkAuthorityNamespacesUnique(document *AuthorityDocument) error {
	return eachUnique(document, "namespace", document.Namespaces)
}

func checkAuthorityCeilingKinds(document *AuthorityDocument) error {
	for _, ceiling := range document.ScopeCeilings {
		if ceiling.ResourceKind == "" || !singleLine(ceiling.ResourceKind) {
			return fmt.Errorf("%w: authority %q declares a scope ceiling on resource kind %q, which must be a single-line value",
				ErrInvalid, document.Authority, ceiling.ResourceKind)
		}
	}
	return nil
}

func checkAuthorityCeilingKindsUnique(document *AuthorityDocument) error {
	seen := make(map[string]struct{}, len(document.ScopeCeilings))
	for _, ceiling := range document.ScopeCeilings {
		if _, exists := seen[ceiling.ResourceKind]; exists {
			return fmt.Errorf("%w: authority %q declares a scope ceiling on resource kind %q twice; two entries for one kind are two bounds on the same scopes, and whichever a reader met first would be the ceiling",
				ErrInvalid, document.Authority, ceiling.ResourceKind)
		}
		seen[ceiling.ResourceKind] = struct{}{}
	}
	return nil
}

func checkAuthorityCeilingActionsDeclared(document *AuthorityDocument) error {
	for _, ceiling := range document.ScopeCeilings {
		if len(ceiling.Actions) == 0 {
			return fmt.Errorf("%w: authority %q declares a scope ceiling on %q with no action; a ceiling that permits nothing reads as a bound and is none, so the kind is left out instead",
				ErrInvalid, document.Authority, ceiling.ResourceKind)
		}
	}
	return nil
}

func checkAuthorityCeilingActionNames(document *AuthorityDocument) error {
	for _, ceiling := range document.ScopeCeilings {
		for _, action := range ceiling.Actions {
			if action == "" || !singleLine(action) {
				return fmt.Errorf("%w: authority %q scope ceiling on %q permits action %q, which must be a single-line value",
					ErrInvalid, document.Authority, ceiling.ResourceKind, action)
			}
		}
	}
	return nil
}

func checkAuthorityCeilingActionsUnique(document *AuthorityDocument) error {
	for _, ceiling := range document.ScopeCeilings {
		seen := make(map[string]struct{}, len(ceiling.Actions))
		for _, action := range ceiling.Actions {
			if _, exists := seen[action]; exists {
				return fmt.Errorf("%w: authority %q scope ceiling on %q permits action %q twice",
					ErrInvalid, document.Authority, ceiling.ResourceKind, action)
			}
			seen[action] = struct{}{}
		}
	}
	return nil
}

func checkAuthorityBindingKeyNames(document *AuthorityDocument) error {
	return eachBinding(document, func(principal string, binding AuthorityBinding) error {
		if binding.BindingKey != "" && !singleLine(binding.BindingKey) {
			return fmt.Errorf("%w: principal %q binding %q binding key must be a single-line value, got %q",
				ErrInvalid, principal, binding.ID, binding.BindingKey)
		}
		return nil
	})
}

func checkAuthorityLookupMethodNames(document *AuthorityDocument) error {
	return eachBinding(document, func(principal string, binding AuthorityBinding) error {
		if binding.LookupMethod != "" && !singleLine(binding.LookupMethod) {
			return fmt.Errorf("%w: principal %q binding %q lookup method must be a single-line value, got %q",
				ErrInvalid, principal, binding.ID, binding.LookupMethod)
		}
		return nil
	})
}

// eachName holds every entry of one declaration to the shape of a name. An
// empty entry fails it too: a list with a blank element declares nothing under
// a name nothing can compare.
func eachName(document *AuthorityDocument, label string, values []string) error {
	for _, value := range values {
		if value == "" || !singleLine(value) {
			return fmt.Errorf("%w: authority %q declares %s %q, which must be a single-line value",
				ErrInvalid, document.Authority, label, value)
		}
	}
	return nil
}

// eachUnique refuses a declaration naming the same thing twice. A repeated
// entry is not a wider claim, which is exactly why it must not pass: it reads
// as one and the envelope check would hold it twice over, so the duplicate's
// only effect is on the canonical bytes.
func eachUnique(document *AuthorityDocument, label string, values []string) error {
	for index, value := range values {
		if slices.Contains(values[:index], value) {
			return fmt.Errorf("%w: authority %q declares %s %q twice",
				ErrInvalid, document.Authority, label, value)
		}
	}
	return nil
}

func eachBinding(document *AuthorityDocument, check func(principal string, binding AuthorityBinding) error) error {
	for _, principal := range document.Principals {
		for _, binding := range principal.Bindings {
			if err := check(principal.Principal, binding); err != nil {
				return err
			}
		}
	}
	return nil
}
