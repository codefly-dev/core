package module

import (
	"errors"
	"strings"
	"testing"
)

// The optional binding beside a mandatory one (testdata/binding-optional.yaml):
// what the composition supplies decides whether it is delivered, omitted or
// refused, and the mandatory one beside it is unaffected.

func optionalContract(t *testing.T) *Contract {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/binding-optional.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

var optionalDeclarations = declaredCallees{
	"documents": {"/documents.v1.DocumentsService/ResolveEvidence": {"documents:read"}},
	"ratings":   {"/ratings.v1.RatingsService/InstallVocabulary": {"ratings.vocabularies:write"}},
}

// optionalValues is the mandatory binding's slots plus the given ones.
func optionalValues(extra map[string]string) MapValues {
	group := map[string]string{"evidence-audience": "documents", "evidence-binding": "evidence"}
	for key, value := range extra {
		group[key] = value
	}
	return MapValues{Public: map[string]map[string]string{"assistant": group}}
}

func TestAnOptionalBindingNoSlotOfWhichIsFilledIsOmitted(t *testing.T) {
	for name, values := range map[string]MapValues{
		"absent": optionalValues(nil),
		// The spelling a module's declared configuration gives a key the
		// composition never set.
		"supplied empty": optionalValues(map[string]string{"RATINGS_AUDIENCE": "", "ratings-resource-kind": " ", "RATINGS_BINDING": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			// The audience is not composed either: the omitted binding's
			// calls are never read.
			callees := declaredCallees{"documents": optionalDeclarations["documents"]}
			resolved, err := optionalContract(t).Resolve(values, WithCallees(callees))
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.Bindings) != 1 || resolved.Bindings[0].ID != "evidence" {
				t.Fatalf("only the mandatory binding is delivered: %+v", resolved.Bindings)
			}
			if len(resolved.Omitted) != 1 || resolved.Omitted[0].ID != "ratings" {
				t.Fatalf("the optional binding is reported omitted: %+v", resolved.Omitted)
			}
			for _, slot := range []string{"audience ← assistant/ratings-audience", "resource_kind ← assistant/ratings-resource-kind", "binding_key ← assistant/ratings-binding"} {
				if !strings.Contains(resolved.Omitted[0].Reason, slot) {
					t.Errorf("the reason names %q: %s", slot, resolved.Omitted[0].Reason)
				}
			}
		})
	}
}

func TestAnOptionalBindingWhoseSlotsAreFilledIsDeliveredAsAMandatoryOne(t *testing.T) {
	resolved, err := optionalContract(t).Resolve(optionalValues(map[string]string{
		"ratings-audience": "ratings", "ratings-resource-kind": "ratings.ratings", "ratings-binding": "ratings",
	}), WithCallees(optionalDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Omitted) != 0 || len(resolved.Bindings) != 2 {
		t.Fatalf("both bindings are delivered and none omitted: %+v / %+v", resolved.Bindings, resolved.Omitted)
	}
	ratings := resolved.Bindings[1]
	if ratings.ID != "ratings" || ratings.Audience != "ratings" || ratings.BindingKey != "ratings" {
		t.Fatalf("the optional binding resolves its slots: %+v", ratings)
	}
	if got := strings.Join(ratings.Scopes[OperationInvoke], ","); got != "ratings.ratings:read,ratings.ratings:write" {
		t.Fatalf("invoke scopes %q", got)
	}
	if got := strings.Join(ratings.Scopes[OperationHeadless], ","); got != "ratings.vocabularies:write" {
		t.Fatalf("headless scopes %q", got)
	}
}

// Filled in part is a composition that meant to fill it and missed a key: the
// binding is resolved whole and the slot left unfilled is refused by name,
// never dropped.
func TestAnOptionalBindingFilledInPartIsRefusedByName(t *testing.T) {
	for name, test := range map[string]struct {
		values   map[string]string
		sentinel error
		names    string
	}{
		"a key missing": {
			values:   map[string]string{"ratings-audience": "ratings", "ratings-resource-kind": "ratings.ratings", "ratings-bindng": "ratings"},
			sentinel: ErrUnresolvedSlot, names: "binding_key ← assistant/ratings-binding",
		},
		"a key supplied empty": {
			values:   map[string]string{"ratings-audience": "", "ratings-resource-kind": "ratings.ratings", "ratings-binding": "ratings"},
			sentinel: ErrAmbiguousSlot, names: "audience ← assistant/ratings-audience",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := optionalContract(t).Resolve(optionalValues(test.values), WithCallees(optionalDeclarations))
			if !errors.Is(err, test.sentinel) {
				t.Fatalf("refused with %v, got: %v", test.sentinel, err)
			}
			for _, want := range []string{"optional binding ratings (filled in part, so resolved whole)", test.names} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal names %q: %v", want, err)
				}
			}
		})
	}
}

// A secret is a value: an optional binding whose only supplied slot is secret
// is filled, and refused as a secret, never omitted around it.
func TestAnOptionalBindingWithASecretSlotIsNotOmitted(t *testing.T) {
	values := optionalValues(nil)
	values.Secrets = map[string]map[string]string{"assistant": {"RATINGS_BINDING": "hidden"}}
	_, err := optionalContract(t).Resolve(values, WithCallees(optionalDeclarations))
	if !errors.Is(err, ErrSecretSlot) || strings.Contains(err.Error(), "hidden") {
		t.Fatalf("refused as a secret without its value: %v", err)
	}
}

// Mandatory stays the default: the same binding without optional, unfilled,
// is refused as it always was.
func TestAMandatoryBindingUnfilledIsRefusedAsBefore(t *testing.T) {
	contract := optionalContract(t)
	contract.Bindings[1].Optional = false
	_, err := contract.Resolve(optionalValues(nil), WithCallees(optionalDeclarations))
	if !errors.Is(err, ErrUnresolvedSlot) || !strings.Contains(err.Error(), "binding ratings audience ← assistant/ratings-audience") {
		t.Fatalf("an unfilled mandatory binding: %v", err)
	}
	if strings.Contains(err.Error(), "optional") {
		t.Fatalf("a mandatory binding's refusal says nothing of optional: %v", err)
	}
}

// The writer keeps the marking: an optional binding encodes and reads back
// optional, and a mandatory one writes no optional field at all.
func TestOptionalSurvivesEncoding(t *testing.T) {
	document, err := optionalContract(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(document), "optional: true") != 1 || strings.Contains(string(document), "optional: false") {
		t.Fatalf("the encoded contract marks exactly the optional binding:\n%s", document)
	}
	again, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	if again.Bindings[0].Optional || !again.Bindings[1].Optional {
		t.Fatalf("read back: %+v", again.Bindings)
	}
}
