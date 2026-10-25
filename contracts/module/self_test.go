package module

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// A binding addressed to the module itself (testdata/binding-self-audience.yaml)
// resolves to the contract's own principal, never to a configured value, and
// composes with an optional binding and with calls.

func selfContract(t *testing.T) *Contract {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/binding-self-audience.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// selfValues fills the evidence binding's slots, plus the given ones.
// Nothing in them names the module itself.
func selfValues(extra map[string]string) MapValues {
	assistant := map[string]string{"evidence-audience": "documents", "evidence-resource-kind": "documents.passages"}
	for key, value := range extra {
		assistant[key] = value
	}
	return MapValues{Public: map[string]map[string]string{
		"assistant": assistant,
	}}
}

var selfDeclarations = declaredCallees{
	"assistant": {"/assistant.v1.TasksService/GetTask": {"assistant.tasks:read"}},
}

func resolvedBinding(t *testing.T, resolved *Resolved, id string) ResolvedBinding {
	t.Helper()
	for _, binding := range resolved.Bindings {
		if binding.ID == id {
			return binding
		}
	}
	t.Fatalf("binding %s is not delivered: %+v", id, resolved.Bindings)
	return ResolvedBinding{}
}

func TestASelfAudienceResolvesToTheContractsOwnPrincipal(t *testing.T) {
	resolved, err := selfContract(t).Resolve(selfValues(nil), WithCallees(selfDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	occurrences := resolvedBinding(t, resolved, "occurrences")
	if occurrences.Audience != resolved.Principal || occurrences.Audience != "assistant" {
		t.Fatalf("the self binding is addressed to the module's principal %q, got %q", resolved.Principal, occurrences.Audience)
	}
	if want := []string{"assistant.tasks:cancel", "assistant.tasks:execute", "assistant.tasks:start"}; !slices.Equal(occurrences.Scopes[OperationInvoke], want) {
		t.Fatalf("the self binding carries its own scopes %v, got %v", want, occurrences.Scopes[OperationInvoke])
	}
	if evidence := resolvedBinding(t, resolved, "evidence"); evidence.Audience != "documents" {
		t.Fatalf("a configured audience beside it is unaffected: %+v", evidence)
	}
}

// The module itself is no slot the composition fills: an optional binding
// addressed to itself is omitted by its other slots alone, and the reason
// names only those.
func TestAnOptionalSelfBindingIsOmittedByItsOtherSlots(t *testing.T) {
	resolved, err := selfContract(t).Resolve(selfValues(nil), WithCallees(selfDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Omitted) != 1 || resolved.Omitted[0].ID != "occurrence-reads" {
		t.Fatalf("the optional self binding is omitted: %+v", resolved.Omitted)
	}
	reason := resolved.Omitted[0].Reason
	if !strings.Contains(reason, "binding_key ← assistant/occurrence-reads-binding") || strings.Contains(reason, "audience") {
		t.Fatalf("the reason names the binding key and not the audience: %s", reason)
	}
}

// Filled, the optional self binding is delivered to the module itself, its
// calls' ceiling read from the module's own declarations.
func TestAFilledSelfBindingReadsItsCallsFromTheModulesOwnDeclarations(t *testing.T) {
	resolved, err := selfContract(t).Resolve(selfValues(map[string]string{"occurrence-reads-binding": "reads"}), WithCallees(selfDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	reads := resolvedBinding(t, resolved, "occurrence-reads")
	if reads.Audience != "assistant" || reads.BindingKey != "reads" || !slices.Equal(reads.Scopes[OperationInvoke], []string{"assistant.tasks:read"}) {
		t.Fatalf("the filled self binding is delivered to the module with its own call's requirement: %+v", reads)
	}
	if _, err := selfContract(t).Resolve(selfValues(map[string]string{"occurrence-reads-binding": "reads"}), WithCallees(declaredCallees{})); !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("a call the module itself does not serve is refused by name, got %v", err)
	}
}

// A self binding that names calls is held to the module's own ceilings by
// what those calls require, exactly as one spelling its scopes out is: a call
// at itself requiring a scope beyond them is refused naming the scope, never
// delivered.
func TestASelfBindingsCallsAreHeldToTheModulesOwnCeilings(t *testing.T) {
	widening := declaredCallees{"assistant": {"/assistant.v1.TasksService/GetTask": {"assistant.tasks:read", "assistant.tasks:delete", "billing.invoices:read"}}}
	_, err := selfContract(t).Resolve(selfValues(map[string]string{"occurrence-reads-binding": "reads"}), WithCallees(widening))
	if !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("a call at itself beyond its own ceilings must be refused, got %v", err)
	}
	for _, want := range []string{"binding occurrence-reads invoke call /assistant.v1.TasksService/GetTask is addressed to the module itself", "requires assistant.tasks:delete, billing.invoices:read, beyond the module's own scope_ceilings"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal names %q, got %v", want, err)
		}
	}
}

// A self audience asks the composition for nothing: no group is read for it,
// and the slot groups a render bakes into the manifest do not include one.
func TestASelfAudienceReadsNoConfiguration(t *testing.T) {
	contract := selfContract(t)
	if groups := contract.SlotGroups(); !slices.Equal(groups, []string{"assistant"}) {
		t.Fatalf("only the composed slots' groups are listed: %v", groups)
	}
	contract.Bindings = slices.DeleteFunc(contract.Bindings, func(binding Binding) bool { return !binding.Audience.Self || binding.Optional })
	resolved, err := contract.Resolve(MapValues{})
	if err != nil {
		t.Fatalf("a contract whose only binding is addressed to itself resolves against no configuration: %v", err)
	}
	if len(resolved.Bindings) != 1 || resolved.Bindings[0].Audience != "assistant" {
		t.Fatalf("it is delivered to the module itself: %+v", resolved.Bindings)
	}
}

// Self is held whether written or constructed: an audience list, which is
// delivered once per entry, is never the module itself.
func TestAConstructedSelfAudienceListIsRefused(t *testing.T) {
	contract := selfContract(t)
	contract.Bindings[0].Audience = Slot{}
	contract.Bindings[0].Audiences = &Slot{Self: true}
	if err := contract.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "binding occurrences audiences slot is {self: true}") {
		t.Fatalf("an audience list addressed to the module itself is refused by name, got %v", err)
	}
}

// The model writes a self audience back as {self: true}, in YAML and in JSON,
// and the encoded document reads back to the same contract.
func TestASelfAudienceIsWrittenBackAsSelf(t *testing.T) {
	contract := selfContract(t)
	document, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(document), "self: true") || strings.Contains(string(document), "from: \"\"") {
		t.Fatalf("the self audience is written as self: true:\n%s", document)
	}
	again, err := Parse(document)
	if err != nil || !again.Bindings[0].Audience.Self || again.Bindings[0].Audience.From != "" {
		t.Fatalf("it reads back as the module itself: %+v %v", again, err)
	}
	encoded, err := json.Marshal(Slot{Self: true})
	if err != nil || string(encoded) != `{"self":true}` {
		t.Fatalf("a self slot is JSON {\"self\":true}, got %s %v", encoded, err)
	}
}
