package module

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A binding addressed to a list of audiences (testdata/binding-audiences.yaml):
// one resolved binding per listed audience, each with the ceiling that
// audience's own declarations give it.

func audiencesContract(t *testing.T) *Contract {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/binding-audiences.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

const (
	reach     = "/targets.v1.TargetService/Reach"
	listKinds = "/targets.v1.TargetService/ListKinds"
)

// Two owners serving one protocol, each declaring its methods in its own
// vocabulary: the same call requires different scopes at each.
var ownerDeclarations = declaredCallees{
	"robin":     {reach: {"robin.answers:read"}, listKinds: {"robin.answers:list"}},
	"documents": {reach: {"documents.versions:read", "documents:read"}, listKinds: nil},
}

func audiencesValues(group map[string]string) MapValues {
	return MapValues{Public: map[string]map[string]string{"assistant": group}}
}

// describe renders a resolution as one line per binding, so a test states the
// whole delivered set at once.
func describe(resolved *Resolved) string {
	lines := make([]string, 0, len(resolved.Bindings))
	for _, binding := range resolved.Bindings {
		line := fmt.Sprintf("%s → %s", binding.ID, binding.Audience)
		for _, operation := range []string{OperationInvoke, OperationLookup, OperationHeadless} {
			if scopes, ok := binding.Scopes[operation]; ok {
				line += fmt.Sprintf(" %s=%s", operation, strings.Join(scopes, ","))
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func TestEachListedAudienceResolvesItsOwnCeilingFromItsOwnPolicies(t *testing.T) {
	resolved, err := audiencesContract(t).Resolve(audiencesValues(map[string]string{
		"OWNER_AUDIENCES":      "robin,documents",
		"owner-resource-kinds": "robin=robin.answers,documents=documents.versions",
	}), WithCallees(ownerDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"owner:documents → documents invoke=documents.versions:read,documents:read headless=",
		"owner:robin → robin invoke=robin.answers:read headless=robin.answers:list",
		"owner-review:documents → documents invoke=documents.versions:read,documents.versions:review",
		"owner-review:robin → robin invoke=robin.answers:read,robin.answers:review",
	}, "\n")
	if got := describe(resolved); got != want {
		t.Fatalf("delivered:\n%s\nwant:\n%s", got, want)
	}
	if len(resolved.Omitted) != 0 {
		t.Fatalf("nothing is omitted: %+v", resolved.Omitted)
	}
	// The order a composition lists them in is not part of the document.
	reordered, err := audiencesContract(t).Resolve(audiencesValues(map[string]string{
		"OWNER_AUDIENCES":      "documents,robin",
		"owner-resource-kinds": "documents=documents.versions,robin=robin.answers",
	}), WithCallees(ownerDeclarations))
	if err != nil || describe(reordered) != want {
		t.Fatalf("a reordered list delivers the same bindings: %v\n%s", err, describe(reordered))
	}
}

func TestACallOneListedAudienceDoesNotServeIsRefusedByName(t *testing.T) {
	callees := declaredCallees{"robin": ownerDeclarations["robin"], "documents": {reach: {"documents:read"}}}
	_, err := audiencesContract(t).Resolve(audiencesValues(map[string]string{
		"OWNER_AUDIENCES":      "robin,documents",
		"OWNER_RESOURCE_KINDS": "robin=robin.answers,documents=documents.versions",
	}), WithCallees(callees))
	if !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("want ErrUnresolvedCall, got %v", err)
	}
	if want := "binding owner headless call " + listKinds + " at documents: documents does not serve"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal names the binding, the call and the audience (%q): %v", want, err)
	}
	if strings.Contains(err.Error(), "at robin") {
		t.Fatalf("the audience that serves the call is not refused: %v", err)
	}
}

func TestAnOptionalAudienceListLeftEmptyIsOmitted(t *testing.T) {
	for name, group := range map[string]map[string]string{
		"absent":         {},
		"supplied empty": {"OWNER_AUDIENCES": "", "OWNER_RESOURCE_KINDS": ""},
	} {
		t.Run(name, func(t *testing.T) {
			// No owner is composed: the omitted bindings' calls are never read.
			resolved, err := audiencesContract(t).Resolve(audiencesValues(group), WithCallees(declaredCallees{}))
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.Bindings) != 0 || len(resolved.Omitted) != 2 {
				t.Fatalf("both bindings are omitted: %+v %+v", resolved.Bindings, resolved.Omitted)
			}
			if !strings.Contains(resolved.Omitted[0].Reason, "audiences ← assistant/owner-audiences") ||
				!strings.Contains(resolved.Omitted[1].Reason, "resource_kinds ← assistant/owner-resource-kinds") {
				t.Fatalf("each reason names the unfilled slots: %+v", resolved.Omitted)
			}
		})
	}
}

func TestAnOptionalAudienceListFilledWithoutItsKindsIsRefusedByName(t *testing.T) {
	_, err := audiencesContract(t).Resolve(audiencesValues(map[string]string{"OWNER_AUDIENCES": "robin"}), WithCallees(ownerDeclarations))
	if !errors.Is(err, ErrUnresolvedSlot) {
		t.Fatalf("want ErrUnresolvedSlot, got %v", err)
	}
	if want := "optional binding owner-review (filled in part, so resolved whole) resource_kinds ← assistant/owner-resource-kinds"; !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q in %v", want, err)
	}
}

func TestPerAudienceKindsAreHeldToTheList(t *testing.T) {
	for kinds, want := range map[string]struct {
		sentinel error
		message  string
	}{
		"robin=robin.answers": {ErrUnresolvedSlot, "names no kind for the listed audience documents"},
		"robin=robin.answers,documents=documents.versions,x=y": {ErrAmbiguousSlot, `names a kind for "x", which the binding's audience list does not list`},
		"robin=robin.answers,robin=robin.other,documents=d":    {ErrAmbiguousSlot, `names a kind for the audience "robin" twice`},
		"robin=robin.answers,documents":                        {ErrAmbiguousSlot, `carries "documents", which is not <audience>=<kind>`},
		"robin=robin.answers,documents=Documents":              {ErrAmbiguousSlot, `resolves the kind of "documents" to "Documents"`},
	} {
		t.Run(kinds, func(t *testing.T) {
			_, err := audiencesContract(t).Resolve(audiencesValues(map[string]string{
				"OWNER_AUDIENCES": "robin,documents", "OWNER_RESOURCE_KINDS": kinds,
			}), WithCallees(ownerDeclarations))
			if !errors.Is(err, want.sentinel) || !strings.Contains(err.Error(), "binding owner-review resource_kinds ← assistant/owner-resource-kinds "+want.message) {
				t.Fatalf("want %v naming %q, got %v", want.sentinel, want.message, err)
			}
		})
	}
}

func TestAMandatoryAudienceListIsRequired(t *testing.T) {
	contract := audiencesContract(t)
	for index := range contract.Bindings {
		contract.Bindings[index].Optional = false
	}
	_, err := contract.Resolve(audiencesValues(map[string]string{}), WithCallees(ownerDeclarations))
	if !errors.Is(err, ErrUnresolvedSlot) || !strings.Contains(err.Error(), "binding owner audiences ← assistant/owner-audiences") {
		t.Fatalf("an unfilled mandatory list is refused by name: %v", err)
	}
}

// A binding with one audience keeps its meaning: delivered under its own id,
// to its one audience, with no derived binding beside it.
func TestASingleAudienceBindingIsUnchanged(t *testing.T) {
	resolved, err := callsContract(t).Resolve(callsValues(), WithCallees(storeDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	want := "evidence → documents invoke=documents.collection:read,documents:read lookup=documents:read\n" +
		"model → model invoke=modelservice.profiles:invoke,modelservice.profiles:read"
	if got := describe(resolved); got != want {
		t.Fatalf("delivered:\n%s\nwant:\n%s", got, want)
	}
}

// The list's group is one the render bakes in, and the model writes the
// binding back in the spelling it was read in: no empty audience beside the
// list.
func TestAnAudienceListIsAGroupAndEncodesAsWritten(t *testing.T) {
	contract := audiencesContract(t)
	if groups := contract.SlotGroups(); strings.Join(groups, ",") != "assistant" {
		t.Fatalf("slot groups: %v", groups)
	}
	document, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(document), "audience:") || !strings.Contains(string(document), "audiences:") {
		t.Fatalf("the list is written as audiences alone:\n%s", document)
	}
	constructed := &Contract{
		Schema: SchemaV1, Principal: "assistant", Namespaces: []string{}, Queues: []string{},
		Bindings: []Binding{{ID: "owner", Operations: []string{OperationInvoke}, Audiences: &Slot{From: "assistant/owner-audiences"},
			Calls: map[string][]string{OperationInvoke: {reach}}}},
	}
	if err := constructed.Validate(); err != nil {
		t.Fatalf("a constructed list-addressed binding validates: %v", err)
	}
	if got := AudienceBindingID("owner", "robin"); got != "owner:robin" || namePattern.MatchString(got) {
		t.Fatalf("a derived id is outside the declared-id grammar: %q", got)
	}
}
