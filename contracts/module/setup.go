package module

import (
	"fmt"
	"slices"
)

// Setup selects a required binding and exact methods for installation only.
// It grants no standing role and carries no caller-authored scope or owner.
type Setup struct {
	Binding string   `yaml:"binding" json:"binding"`
	Methods []string `yaml:"methods" json:"methods"`
}

// ResolvedSetup preserves the selected methods as well as their callee-derived
// requirements. Methods sharing requirements are not interchangeable grants.
type ResolvedSetup struct {
	Binding string   `yaml:"binding" json:"binding"`
	Methods []string `yaml:"methods" json:"methods"`
	Scopes  []string `yaml:"scopes" json:"scopes"`
}

func (contract *Contract) resolveSetup(resolved *Resolved, callees Callees) ([]ResolvedSetup, error) {
	var result []ResolvedSetup
	for _, setup := range contract.Setup {
		i := slices.IndexFunc(resolved.Bindings, func(b ResolvedBinding) bool { return b.ID == setup.Binding })
		if i < 0 || callees == nil {
			return nil, fmt.Errorf("%w: setup %s needs its resolved binding and callee declarations", ErrUnresolvedCall, setup.Binding)
		}
		binding := resolved.Bindings[i]
		if binding.Audience == resolved.Principal {
			return nil, fmt.Errorf("%w: setup %s resolves to self", ErrUnresolvedCall, setup.Binding)
		}
		entry := ResolvedSetup{Binding: setup.Binding, Methods: slices.Sorted(slices.Values(setup.Methods))}
		for _, method := range entry.Methods {
			required, err := callees.Requires(binding.Audience, method)
			if err != nil || len(required) == 0 {
				return nil, fmt.Errorf("%w: setup %s method %s requires explicit callee requirements: %v", ErrUnresolvedCall, setup.Binding, method, err)
			}
			for _, scope := range required {
				if !slices.Contains(binding.Scopes[OperationInvoke], scope) {
					return nil, fmt.Errorf("%w: setup %s method %s requires %s beyond its invoke ceiling", ErrUnresolvedCall, setup.Binding, method, scope)
				}
				entry.Scopes = append(entry.Scopes, scope)
			}
		}
		slices.Sort(entry.Scopes)
		entry.Scopes = slices.Compact(entry.Scopes)
		result = append(result, entry)
	}
	return result, nil
}
