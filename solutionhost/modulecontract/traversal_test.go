package modulecontract

import (
	"strings"
	"testing"
)

// TestEveryTraversalReachesEverySource: a rule can be whole and still be
// applied to only some of what it governs. Deleting binding traversal from
// actionLists() left both action fixtures refusing exactly as before, because
// each modified a top-level scope ceiling — the rule was intact, it simply no
// longer looked at a binding's ceilings. Asserted over the valid contract,
// which carries one of each, with per-source fixtures beside it.
func TestEveryTraversalReachesEverySource(t *testing.T) {
	contract, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var topLevel, bareCeiling, explicitScope bool
	for _, list := range contract.actionLists() {
		switch {
		case strings.HasPrefix(list.label, "scope ceiling "):
			topLevel = true
		case strings.Contains(list.label, "annotations."):
			explicitScope = true
		case strings.HasPrefix(list.label, "binding "):
			bareCeiling = true
		}
	}
	for _, source := range []struct {
		reached bool
		what    string
	}{
		{topLevel, "a top-level scope ceiling"},
		{bareCeiling, "a binding's bare-action ceiling"},
		{explicitScope, "an explicit scope of a binding's ceiling"},
	} {
		if !source.reached {
			t.Errorf("actionLists() does not reach %s, so every action rule skips it", source.what)
		}
	}
	// Every slot a binding can carry, for the same reason.
	var audience, resourceKind, bindingKey bool
	for _, entry := range contract.slots() {
		switch entry.field {
		case fieldAudience:
			audience = true
		case fieldResourceKind:
			resourceKind = true
		case fieldBindingKey:
			bindingKey = true
		}
	}
	if !audience || !resourceKind || !bindingKey {
		t.Errorf("slots() reaches audience=%v resource_kind=%v binding_key=%v; every slot rule skips the ones it misses", audience, resourceKind, bindingKey)
	}
}
