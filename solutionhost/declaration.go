package solutionhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	modulecontract "github.com/codefly-dev/core/contracts/module"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Declaration is what a module's contract declares to its host, rendered in
// one place for every producer (the CLI's render and delivery, a test that
// composes the module in process): the authority block of its signed manifest,
// and the contract's half of the authority's document delivered beside it.
// Neither carries what the contract does not say: the method policies its
// package declares, and the prepared bindings a composition asks for.
type Declaration struct {
	Authority Authority         `json:"authority"`
	Document  AuthorityDocument `json:"document"`
}

// AuthorityDocument is the contract's half of the authority's document
// (codefly.dev/composition AuthorityManifestV1), in core's terms: the scope
// ceilings on the module's own kinds, its roles, its owner-aware and shared
// declarations, and its installation declaration.
type AuthorityDocument struct {
	ScopeCeilings    []DeclaredScope                 `json:"scope_ceilings,omitempty"`
	Roles            []DeclaredRole                  `json:"roles,omitempty"`
	OwnerAware       bool                            `json:"owner_aware,omitempty"`
	SharedKinds      []string                        `json:"shared_kinds,omitempty"`
	InstallationRole string                          `json:"installation_role,omitempty"`
	EdgeDependencies []modulecontract.EdgeDependency `json:"edge_dependencies,omitempty"`
}

// DeclaredRole is one role as the authority's document carries it: the scopes
// it grants, those it grants on what the installation owns, and those it names
// through each of the module's bindings, per binding in the order first named.
type DeclaredRole struct {
	Name                    string          `json:"name"`
	Scopes                  []DeclaredScope `json:"scopes,omitempty"`
	InstallationOwnedScopes []DeclaredScope `json:"installation_owned_scopes,omitempty"`
	Through                 []RoleThrough   `json:"through,omitempty"`
}

// RoleThrough is what a role names downstream of one binding.
type RoleThrough struct {
	Binding string          `json:"binding"`
	Scopes  []DeclaredScope `json:"scopes"`
}

// DeclaredScope is a kind and its actions, never ids.
type DeclaredScope struct {
	ResourceKind string   `json:"resource_kind"`
	Actions      []string `json:"actions"`
}

// Declare renders contract, resolved against its composition (resolved is
// contract.Resolve's answer, for the environment class being delivered to),
// into its Declaration.
//
// The authority block names the module's principal, each binding the
// composition delivers with its scopes per operation, the installation
// declaration verbatim but for an edge's class-resolved presence, the scopes
// that govern its sources, its queues and namespaces, and its own kinds.
//
// The document carries each role in declaration order: a scope naming a
// binding through is carried in the role's through, per binding in the order
// first named, only when the composition delivers that binding (an optional
// binding it fills none of the slots of is left out, and the role reaches
// nothing through it). An empty list is nil throughout, so a list and its
// absence spell the same bytes.
func Declare(contract *modulecontract.Contract, resolved *modulecontract.Resolved) (*Declaration, error) {
	if contract == nil || resolved == nil {
		return nil, fmt.Errorf("a declaration is rendered from a contract and its resolution")
	}
	roles, err := declaredRoles(contract, resolved)
	if err != nil {
		return nil, err
	}
	out := &Declaration{
		Authority: Authority{
			Principal:        resolved.Principal,
			InstallationRole: resolved.InstallationRole,
			EdgeDependencies: cloned(resolved.EdgeDependencies),
			SourceGovernance: cloned(resolved.SourceGovernance),
			Queues:           cloned(resolved.Queues),
			Namespaces:       cloned(resolved.Namespaces),
		},
		Document: AuthorityDocument{
			Roles:            roles,
			OwnerAware:       resolved.OwnerAware,
			InstallationRole: resolved.InstallationRole,
			EdgeDependencies: cloned(resolved.EdgeDependencies),
		},
	}
	for _, binding := range resolved.Bindings {
		operations := make(map[string][]string, len(binding.Operations))
		for _, operation := range binding.Operations {
			operations[operation] = cloned(binding.Scopes[operation])
		}
		out.Authority.Bindings = append(out.Authority.Bindings, AuthorityBinding{ID: binding.ID, Audience: binding.Audience, Operations: operations})
	}
	for _, ceiling := range resolved.ScopeCeilings {
		if !slices.Contains(out.Authority.ResourceKinds, ceiling.ResourceKind) {
			out.Authority.ResourceKinds = append(out.Authority.ResourceKinds, ceiling.ResourceKind)
		}
		out.Document.ScopeCeilings = append(out.Document.ScopeCeilings, DeclaredScope{ResourceKind: ceiling.ResourceKind, Actions: cloned(ceiling.Actions)})
		if ceiling.Shared {
			out.Document.SharedKinds = append(out.Document.SharedKinds, ceiling.ResourceKind)
		}
	}
	return out, nil
}

// declaredRoles are contract's roles as the document carries them, each held
// to the one resolved addresses at the same position.
func declaredRoles(contract *modulecontract.Contract, resolved *modulecontract.Resolved) ([]DeclaredRole, error) {
	if len(contract.Roles) == 0 {
		return nil, nil
	}
	if len(resolved.Roles) != len(contract.Roles) {
		return nil, fmt.Errorf("the module contract of %s resolves %d roles of the %d it declares", contract.Principal, len(resolved.Roles), len(contract.Roles))
	}
	out := make([]DeclaredRole, 0, len(contract.Roles))
	for index, role := range contract.Roles {
		if resolved.Roles[index].Name != role.Name {
			return nil, fmt.Errorf("the module contract of %s resolves role %q where it declares %q", contract.Principal, resolved.Roles[index].Name, role.Name)
		}
		delivered := resolved.Roles[index].Through
		declared := DeclaredRole{Name: role.Name, InstallationOwnedScopes: declaredScopes(role.InstallationOwnedScopes)}
		var own []modulecontract.CeilingScope
		for _, scope := range role.Scopes {
			if scope.Through == "" {
				own = append(own, scope)
				continue
			}
			if _, held := delivered[scope.Through]; !held {
				continue // through an optional binding the composition leaves out
			}
			i := slices.IndexFunc(declared.Through, func(t RoleThrough) bool { return t.Binding == scope.Through })
			if i < 0 {
				declared.Through = append(declared.Through, RoleThrough{Binding: scope.Through})
				i = len(declared.Through) - 1
			}
			declared.Through[i].Scopes = append(declared.Through[i].Scopes, declaredScopes([]modulecontract.CeilingScope{scope})...)
		}
		declared.Scopes = declaredScopes(own)
		out = append(out, declared)
	}
	return out, nil
}

// declaredScopes spells contract scopes as kinds and actions; nil for none.
func declaredScopes(scopes []modulecontract.CeilingScope) []DeclaredScope {
	if len(scopes) == 0 {
		return nil
	}
	out := make([]DeclaredScope, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, DeclaredScope{ResourceKind: scope.ResourceKind, Actions: cloned(scope.Actions)})
	}
	return out
}

// cloned is a copy of in, nil when in is empty: the spelling every producer of
// a declaration used, so an empty list serializes the same everywhere.
func cloned[T any](in []T) []T { return append([]T(nil), in...) }

// RetiredRolesFileName is the declaration a solution shipped beside its
// solution manifest before core's module contract carried roles. Nothing reads
// it; a unit still shipping one is refused naming it, so a role is never
// dropped in silence.
const RetiredRolesFileName = "roles.codefly.yaml"

// RefuseRetiredRoles refuses the module at dir when it still ships the retired
// roles declaration, saying where its contents now belong. An empty dir ships
// nothing.
func RefuseRetiredRoles(dir string) error {
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, RetiredRolesFileName)
	_, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("%s is no longer read: a unit's own kinds and its roles are declared in its %s, as scope_ceilings and roles (core's module contract); move them there and delete %s", path, modulecontract.FileName, RetiredRolesFileName)
}

// DeclareModule is the declaration of the module named name at dir, as every
// producer renders it: dir must not ship the retired roles declaration; the
// contract is request when the caller has one (a composed solution's
// consumption, core's ConsumptionContract), else dir's own module contract
// (nil, nil when dir publishes none); its principal must be name, the
// principal every credential of the module is issued to (no check when name
// is empty); resolve is the composition's resolution (Resolver); and Declare
// renders the rest. The resolution is returned with it, for what a caller
// checks or reports of it.
//
// Each call resolves the contract again, which reads the composition: a caller
// renders a module once and reads every field it needs off that one
// Declaration.
func DeclareModule(dir, name string, request *modulecontract.Contract, resolve func(*modulecontract.Contract) (*modulecontract.Resolved, error)) (*Declaration, *modulecontract.Resolved, error) {
	if err := RefuseRetiredRoles(dir); err != nil {
		return nil, nil, err
	}
	contract := request
	if contract == nil {
		if dir == "" {
			return nil, nil, nil
		}
		loaded, err := modulecontract.Load(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		contract = loaded
	}
	if name != "" && contract.Principal != name {
		return nil, nil, fmt.Errorf("module %s publishes a contract whose principal is %q; the principal every credential of a module is issued to is the module's own name, so the contract declares principal: %s", name, contract.Principal, name)
	}
	resolved, err := resolve(contract)
	if err != nil {
		return nil, nil, fmt.Errorf("module %s: %w", contract.Principal, err)
	}
	declaration, err := Declare(contract, resolved)
	if err != nil {
		return nil, nil, err
	}
	return declaration, resolved, nil
}

// Resolver resolves a contract against a composition's configurations (every
// information as supplied, ConfigurationValues), for the environment class
// being delivered to, reading a callee's method declarations from callees
// when a binding names calls (nil when none does).
func Resolver(infos []*basev0.ConfigurationInformation, class string, callees modulecontract.Callees) func(*modulecontract.Contract) (*modulecontract.Resolved, error) {
	return func(contract *modulecontract.Contract) (*modulecontract.Resolved, error) {
		options := []modulecontract.ResolveOption{modulecontract.WithEnvironmentClass(class)}
		if callees != nil {
			options = append(options, modulecontract.WithCallees(callees))
		}
		return contract.Resolve(ConfigurationValues(infos), options...)
	}
}

// ConfigurationValues are a composition's configurations as the values a
// contract's slots resolve from: a group's records exactly as supplied, across
// every information that contributes to the group — every record, in both
// spellings of a key, a key supplied twice, a key classified public here and
// secret there — and nothing decided, since selecting among them is core's
// resolution, never a provider's (modulecontract.Values).
func ConfigurationValues(infos []*basev0.ConfigurationInformation) modulecontract.Values {
	return configurationValues(infos)
}

type configurationValues []*basev0.ConfigurationInformation

// Records implements modulecontract.Values.
func (infos configurationValues) Records(group string) ([]modulecontract.Record, error) {
	var records []modulecontract.Record
	for _, info := range infos {
		if info.GetName() != group {
			continue
		}
		for _, value := range info.GetConfigurationValues() {
			records = append(records, modulecontract.Record{Key: value.GetKey(), Value: value.GetValue(), Secret: value.GetSecret()})
		}
	}
	return records, nil
}
