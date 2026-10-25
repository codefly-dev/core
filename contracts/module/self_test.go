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

// selfValues fills the owner binding's slots, plus the given ones in the
// scheduler group. Nothing in them names the scheduler.
func selfValues(scheduler map[string]string) MapValues {
	return MapValues{Public: map[string]map[string]string{
		"scheduler-owner": {"owner-audience": "documents", "owner-resource-kind": "documents.versions"},
		"scheduler":       scheduler,
	}}
}

var selfDeclarations = declaredCallees{
	"scheduler": {"/scheduler.v1.TasksService/GetTask": {"scheduler.tasks:read"}},
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
	if occurrences.Audience != resolved.Principal || occurrences.Audience != "scheduler" {
		t.Fatalf("the self binding is addressed to the module's principal %q, got %q", resolved.Principal, occurrences.Audience)
	}
	if want := []string{"scheduler.tasks:cancel", "scheduler.tasks:execute", "scheduler.tasks:start"}; !slices.Equal(occurrences.Scopes[OperationInvoke], want) {
		t.Fatalf("the self binding carries its own scopes %v, got %v", want, occurrences.Scopes[OperationInvoke])
	}
	if owner := resolvedBinding(t, resolved, "owner"); owner.Audience != "documents" {
		t.Fatalf("a configured audience beside it is unaffected: %+v", owner)
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
	if !strings.Contains(reason, "binding_key ← scheduler/occurrence-reads-binding") || strings.Contains(reason, "audience") {
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
	if reads.Audience != "scheduler" || reads.BindingKey != "reads" || !slices.Equal(reads.Scopes[OperationInvoke], []string{"scheduler.tasks:read"}) {
		t.Fatalf("the filled self binding is delivered to the module with its own call's requirement: %+v", reads)
	}
	if _, err := selfContract(t).Resolve(selfValues(map[string]string{"occurrence-reads-binding": "reads"}), WithCallees(declaredCallees{})); !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("a call the module itself does not serve is refused by name, got %v", err)
	}
}

// A self audience asks the composition for nothing: no group is read for it,
// and the slot groups a render bakes into the manifest do not include one.
func TestASelfAudienceReadsNoConfiguration(t *testing.T) {
	contract := selfContract(t)
	if groups := contract.SlotGroups(); !slices.Equal(groups, []string{"scheduler", "scheduler-owner"}) {
		t.Fatalf("only the composed slots' groups are listed: %v", groups)
	}
	contract.Bindings = slices.DeleteFunc(contract.Bindings, func(binding Binding) bool { return !binding.Audience.Self || binding.Optional })
	resolved, err := contract.Resolve(MapValues{})
	if err != nil {
		t.Fatalf("a contract whose only binding is addressed to itself resolves against no configuration: %v", err)
	}
	if len(resolved.Bindings) != 1 || resolved.Bindings[0].Audience != "scheduler" {
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
