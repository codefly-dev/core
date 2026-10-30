package module

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func setupContract(t *testing.T) *Contract {
	t.Helper()
	data, err := os.ReadFile("testdata/setup.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSetupSelectsExactMethodWithoutStandingGrant(t *testing.T) {
	c := setupContract(t)
	written, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	c, err = Parse(written)
	if err != nil {
		t.Fatal(err)
	}
	values := MapValues{Public: map[string]map[string]string{"assistant": {"records-audience": "records"}}}
	callees := declaredCallees{"records": {
		"/records.v1.RecordsService/DefineRecordType": {"records.types:define"},
		"/records.v1.RecordsService/DefineProjection": {"records.types:define"},
	}}
	r, err := c.Resolve(values, WithCallees(callees))
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedSetup{{Binding: "schemas", Methods: []string{"/records.v1.RecordsService/DefineRecordType"}, Scopes: []string{"records.types:define"}}}
	if !reflect.DeepEqual(r.Setup, want) || len(r.Roles) != 0 || r.InstallationRole != "none" {
		t.Fatalf("setup widened or changed: %+v", r)
	}
	r.Setup[0].Methods[0] = "changed"
	if c.Setup[0].Methods[0] == "changed" {
		t.Fatal("resolved setup aliases authored storage")
	}
	if _, err := c.Resolve(values); !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("missing declarations: %v", err)
	}
	c.Setup[0].Methods = []string{"/records.v1.RecordsService/Other"}
	callees["records"][c.Setup[0].Methods[0]] = []string{"records.records:write"}
	if _, err := c.Resolve(values, WithCallees(callees)); !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("beyond ceiling: %v", err)
	}
	callees["records"][c.Setup[0].Methods[0]] = nil
	if _, err := c.Resolve(values, WithCallees(callees)); !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("empty requirement: %v", err)
	}
}

func TestSetupRefusesAmbiguousOrUnboundedSelections(t *testing.T) {
	for name, mutate := range map[string]func(*Contract){
		"unknown":           func(c *Contract) { c.Setup[0].Binding = "missing" },
		"duplicate binding": func(c *Contract) { c.Setup = append(c.Setup, c.Setup[0]) },
		"empty":             func(c *Contract) { c.Setup[0].Methods = nil },
		"duplicate method":  func(c *Contract) { c.Setup[0].Methods = append(c.Setup[0].Methods, c.Setup[0].Methods[0]) },
		"wildcard":          func(c *Contract) { c.Setup[0].Methods = []string{"POST /types/*"} },
		"route parameter":   func(c *Contract) { c.Setup[0].Methods = []string{"POST /types/{id}"} },
		"optional":          func(c *Contract) { c.Bindings[0].Optional = true },
		"self":              func(c *Contract) { c.Bindings[0].Audience = Slot{Self: true} },
		"headless":          func(c *Contract) { c.Bindings[0].Operations = []string{OperationHeadless} },
	} {
		t.Run(name, func(t *testing.T) {
			c := setupContract(t)
			mutate(c)
			if err := c.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid setup: %v", err)
			}
		})
	}
}
