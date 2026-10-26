package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"

	"gopkg.in/yaml.v3"

	modulecontract "github.com/codefly-dev/core/contracts/module"
)

// ConsumptionContract is the module contract a solution's declared consumption
// amounts to: one INVOKE binding per api.consumes entry that asks for scopes —
// its ID the entry's, its audience the entry's audience slot, its ceiling
// exactly the declared scopes, its revision the entry's — under principal, the
// name the solution is composed under (a composed solution is a module, and a
// module's principal is its own name).
//
// It is a contract and not a resolved request on purpose. The request then
// goes through the ONE implementation of the module contract — its rules hold
// the grammar of every kind, action, slot and revision, and its Resolve
// resolves the audience against the composition with the same secret and
// ambiguity refusals a module's contract gets — so a solution's request and a
// module's cannot be read two ways. A renderer derives the authority document
// from it exactly as it derives one from a module's published contract.
//
// Invoke only: a solution calling a module on a person's behalf attenuates
// that person's own authority, and has nothing to look up and nothing to do
// with nobody present. Namespaces, queues, scope ceilings and destinations are
// a module's own declarations; consumption contributes none, and they are
// written empty, not absent.
//
// A solution asking for no scopes still declares its principal with an empty
// binding set. Delivery must admit it explicitly without granting permissions.
func (m *Manifest) ConsumptionContract(principal string) (*modulecontract.Contract, error) {
	contract := consumptionContract(principal, m.API.Consumes)
	if err := contract.Validate(); err != nil {
		return nil, fmt.Errorf("the authority api.consumes asks for: %w", err)
	}
	return contract, nil
}

func consumptionContract(principal string, declarations []APIDeclaration) *modulecontract.Contract {
	bindings := []modulecontract.Binding{}
	for _, declaration := range declarations {
		if len(declaration.Scopes) == 0 || declaration.Audience == nil {
			continue
		}
		scopes := make([]modulecontract.CeilingScope, 0, len(declaration.Scopes))
		for _, scope := range sortedConsumedScopes(declaration.Scopes) {
			scopes = append(scopes, modulecontract.CeilingScope{ResourceKind: scope.ResourceKind, Actions: scope.Actions})
		}
		bindings = append(bindings, modulecontract.Binding{
			ID:           declaration.ID,
			Revision:     declaration.Revision,
			Operations:   []string{modulecontract.OperationInvoke},
			Audience:     *declaration.Audience,
			ScopeCeiling: map[string]modulecontract.Ceiling{modulecontract.OperationInvoke: {Scopes: scopes}},
		})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	return &modulecontract.Contract{
		Schema:        modulecontract.SchemaV1,
		Principal:     principal,
		Namespaces:    []string{},
		Queues:        []string{},
		ScopeCeilings: []modulecontract.ScopeCeiling{},
		Bindings:      bindings,
		Destinations:  []modulecontract.Destination{},
	}
}

// consumptionValidationPrincipal stands in for the principal while a manifest
// is validated on its own. The principal is the renderer's to supply — the name
// the solution is composed under — and no binding rule depends on it; the
// renderer validates again, under the real one.
const consumptionValidationPrincipal = "solution"

// validateConsumption holds every request in api.consumes to the module
// contract's rules.
func validateConsumption(declarations []APIDeclaration) error {
	contract := consumptionContract(consumptionValidationPrincipal, declarations)
	if contract == nil {
		return nil
	}
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("api.consumes asks for authority the module contract refuses: %w", err)
	}
	return nil
}

// LoadConsumption reads what a solution manifest declares it CONSUMES, and
// nothing else: the schema version and api.consumes, strictly — an unknown
// field in a consumption, an invalid one, or a request the module contract
// refuses fails — while every other section is left unread.
//
// It exists for a reader that derives authority from the consumption alone.
// Such a reader must not fail on a section it does not read: a solution whose
// agent block or another section does not pass Load asks for no authority
// because of it, and refusing it there would refuse a render that has nothing
// to do with authority. The returned manifest carries the schema version and
// the consumption only.
func LoadConsumption(data []byte) (*Manifest, error) {
	var document struct {
		SchemaVersion string    `yaml:"schema_version"`
		API           yaml.Node `yaml:"api"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode solution manifest: %w", err)
	}
	if err := validateSchemaVersion(document.SchemaVersion); err != nil {
		return nil, err
	}
	consumes, err := consumesNode(&document.API)
	if err != nil {
		return nil, err
	}
	var declarations []APIDeclaration
	if consumes != nil {
		raw, err := yaml.Marshal(consumes)
		if err != nil {
			return nil, fmt.Errorf("decode api.consumes: %w", err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if err := decoder.Decode(&declarations); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("decode api.consumes: %w", err)
		}
	}
	if err := validateConsumedAPIs("api.consumes", declarations); err != nil {
		return nil, err
	}
	if err := validateConsumptionVersion(document.SchemaVersion, declarations); err != nil {
		return nil, err
	}
	if err := validateConsumption(declarations); err != nil {
		return nil, err
	}
	return &Manifest{SchemaVersion: document.SchemaVersion, API: API{Consumes: declarations}}, nil
}

// consumesNode finds api.consumes in a decoded api mapping, or nil.
func consumesNode(api *yaml.Node) (*yaml.Node, error) {
	if api == nil || api.Kind == 0 {
		return nil, nil
	}
	if api.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("decode solution manifest: api is not a mapping")
	}
	var found *yaml.Node
	for index := 0; index+1 < len(api.Content); index += 2 {
		if api.Content[index].Value != "consumes" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("decode solution manifest: api.consumes is declared twice")
		}
		found = api.Content[index+1]
	}
	return found, nil
}
