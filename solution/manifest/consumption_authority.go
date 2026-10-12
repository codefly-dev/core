package manifest

import (
	"sort"

	modulecontract "github.com/codefly-dev/core/contracts/module"
)

// ConsumptionAuthority derives the authority request a solution's declared
// consumption makes: one invoke binding per api.consumes entry that asks for
// scopes, addressed to the consumed module and bounded by those scopes.
//
// It is the solution's counterpart of a module contract's outward bindings,
// derived from the one place a solution already declares what it consumes, so
// a renderer derives the solution's authority document from it exactly as it
// derives a module's from its contract — and nothing restates the consumption
// in a second, hand-kept file. principal is the name the solution is composed
// under: a module's principal is its own name, and a composed solution is a
// module.
//
// The audience is the consumed module's name, which is that module's principal
// and the audience a capability for it is addressed to; a module repository
// spells another module only through a slot, but a solution composes modules by
// name, so its declaration names them directly. Each binding is invoke only: a
// solution calling a module on a person's behalf attenuates that person's own
// authority, and has nothing to look up and nothing to do with nobody present.
//
// A solution that asks for no scopes asks for no authority, and this returns
// nil. Namespaces, queues and scope ceilings are a module's own declarations;
// consumption contributes none, and they are returned empty rather than nil,
// because an authority document distinguishes "declares none" from "dropped".
func (m *Manifest) ConsumptionAuthority(principal string) *modulecontract.Resolved {
	var bindings []modulecontract.ResolvedBinding
	for _, declaration := range m.API.Consumes {
		if len(declaration.Scopes) == 0 {
			continue
		}
		var scopes []string
		for _, scope := range sortedConsumedScopes(declaration.Scopes) {
			for _, action := range scope.Actions {
				scopes = append(scopes, scope.ResourceKind+":"+action)
			}
		}
		sort.Strings(scopes)
		bindings = append(bindings, modulecontract.ResolvedBinding{
			ID:         declaration.ID,
			Revision:   1,
			Operations: []string{modulecontract.OperationInvoke},
			Audience:   declaration.Module,
			Scopes:     map[string][]string{modulecontract.OperationInvoke: scopes},
		})
	}
	if len(bindings) == 0 {
		return nil
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	return &modulecontract.Resolved{
		Principal:     principal,
		Namespaces:    []string{},
		Queues:        []string{},
		ScopeCeilings: []modulecontract.ScopeCeiling{},
		Bindings:      bindings,
	}
}
