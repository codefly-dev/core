package module

import (
	"os"
	"reflect"
	"testing"
)

// TestRolesAreReadAsDeclaredAndWrittenBack: a unit's roles are part of its
// contract, read in order with every scope as written, and Encode writes them
// back so the bytes read again to the same roles.
func TestRolesAreReadAsDeclaredAndWrittenBack(t *testing.T) {
	document, err := os.ReadFile("testdata/roles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	want := []Role{
		{Name: InstallerRole, Scopes: []CeilingScope{
			{ResourceKind: "wiki.page", Actions: []string{"read", "write", "publish"}},
			{ResourceKind: "documents", Actions: []string{"read", "write"}},
		}},
		{Name: "editor", Scopes: []CeilingScope{{ResourceKind: "wiki.page", Actions: []string{"read", "write"}}}},
		{Name: "viewer", Scopes: []CeilingScope{
			{ResourceKind: "wiki.page", Actions: []string{"read"}},
			{ResourceKind: "documents", Actions: []string{"read"}},
		}},
	}
	if !reflect.DeepEqual(contract.Roles, want) {
		t.Fatalf("roles read as %+v, declared as %+v", contract.Roles, want)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatalf("a contract with roles must encode: %v", err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Roles, want) {
		t.Fatalf("roles written and read back as %+v", again.Roles)
	}
}

// TestAContractWithoutRolesDeclaresNone: a bare module's access is the
// organisation's roles, so it writes no roles key and Encode writes none.
func TestAContractWithoutRolesDeclaresNone(t *testing.T) {
	contract, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if contract.Roles != nil {
		t.Fatalf("a contract declaring no roles reads as %+v", contract.Roles)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Parse(encoded); again.Roles != nil {
		t.Fatalf("Encode wrote roles for a contract declaring none")
	}
}
