package module

import (
	"slices"
)

// The rules a source governance declaration is held to (SourceGovernance),
// after the roles it names are themselves held to theirs.
const (
	ruleGovernanceHasScope   = "source-governance-has-scope"
	ruleGovernanceHasAction  = "source-governance-scope-has-action"
	ruleGovernanceKindUnique = "source-governance-kind-unique"
	ruleGovernanceNotThrough = "source-governance-not-through"
	ruleGovernanceOwnKind    = "source-governance-own-kind"
	ruleGovernanceOwnAction  = "source-governance-own-action"
	ruleGovernanceNotShared  = "source-governance-not-shared"
	ruleGovernanceRoleGrants = "source-governance-role-grants"
)

func governanceRules() []rule {
	return []rule{
		{name: ruleGovernanceHasScope, check: checkGovernanceHasScopes},
		{name: ruleGovernanceHasAction, check: checkGovernanceScopesHaveActions},
		{name: ruleGovernanceKindUnique, check: checkGovernanceKindsUnique},
		{name: ruleGovernanceNotThrough, check: checkGovernanceNotThrough},
		{name: ruleGovernanceOwnKind, check: checkGovernanceOwnKinds},
		{name: ruleGovernanceOwnAction, check: checkGovernanceOwnActions},
		{name: ruleGovernanceNotShared, check: checkGovernanceNotShared},
		{name: ruleGovernanceRoleGrants, check: checkGovernanceRoleGrants},
	}
}

// governanceScopes is the declaration's scopes, or none when it declares no
// governance.
func (contract *Contract) governanceScopes() []CeilingScope {
	if contract.SourceGovernance == nil {
		return nil
	}
	return contract.SourceGovernance.Scopes
}

// governingScopes is the declaration resolved: each scope
// "<resource kind>:<action>", sorted, each once; nil when none is declared.
func (contract *Contract) governingScopes() []string {
	var out []string
	for _, scope := range contract.governanceScopes() {
		for _, action := range scope.Actions {
			out = append(out, scope.ResourceKind+":"+action)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
