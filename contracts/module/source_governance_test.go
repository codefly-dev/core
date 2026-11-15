package module

import (
	"os"
	"slices"
	"testing"
)

func governanceFixture(t *testing.T, name string) *Contract {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

var governanceValues = MapValues{Public: map[string]map[string]string{"assistant": {"documents-audience": "documents"}}}

// The declaration resolves to the scopes a host reads off the manifest, and
// survives the one writer unchanged.
func TestSourceGovernanceResolvesToItsScopes(t *testing.T) {
	contract := governanceFixture(t, "source-governance")
	contract.SourceGovernance.Scopes = append(contract.SourceGovernance.Scopes, CeilingScope{ResourceKind: "assistant.pages", Actions: []string{"publish"}})
	contract.SourceGovernance.Scopes[0].Actions = []string{"write"}
	// Two scopes of one kind are refused; one kind with two actions is not.
	if err := contract.Validate(); err == nil {
		t.Fatal("a governing kind named twice was accepted")
	}
	contract.SourceGovernance.Scopes = []CeilingScope{{ResourceKind: "assistant.pages", Actions: []string{"write", "publish"}}}
	resolved, err := contract.Resolve(governanceValues)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"assistant.pages:publish", "assistant.pages:write"}; !slices.Equal(resolved.SourceGovernance, want) {
		t.Fatalf("resolved governance %v, want %v", resolved.SourceGovernance, want)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(again.governingScopes(), resolved.SourceGovernance) {
		t.Fatalf("encoding changed the declaration to %v", again.governingScopes())
	}
}

// Absent, a unit governs no source: nothing resolves, so nothing reaches the
// manifest and the host keeps every source the organization administrators'.
func TestNoSourceGovernanceResolvesToNone(t *testing.T) {
	contract := governanceFixture(t, "source-governance")
	contract.SourceGovernance = nil
	resolved, err := contract.Resolve(governanceValues)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SourceGovernance != nil {
		t.Fatalf("a contract declaring no governance resolved %v", resolved.SourceGovernance)
	}
}
