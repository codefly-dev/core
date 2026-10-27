package module

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// declaredCallees answers from a table: per audience, per call, the scopes the
// audience's method policy requires. A call it does not list is not served.
type declaredCallees map[string]map[string][]string

func (c declaredCallees) Requires(audience, call string) ([]string, error) {
	required, ok := c[audience][call]
	if !ok {
		return nil, fmt.Errorf("%s does not serve %s", audience, call)
	}
	return required, nil
}

func callsContract(t *testing.T) *Contract {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/binding-names-calls.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func callsValues() MapValues {
	return MapValues{Public: map[string]map[string]string{"assistant": {
		"evidence-audience": "documents", "evidence-binding": "evidence", "model-audience": "model",
	}}}
}

var storeDeclarations = declaredCallees{
	"documents": {
		"/documents.v1.DocumentsService/ResolveEvidence":     {"documents:read", "documents.collection:read"},
		"/documents.v1.DocumentsService/ReadDocumentExcerpt": {"documents:read", "documents.collection:read"},
	},
	"model": {
		"GET /v1/model/profiles":                  {"modelservice.profiles:read"},
		"POST /v1/model/profiles/{name}:generate": {"modelservice.profiles:invoke"},
	},
}

// A binding that names its calls carries what those calls require, read from
// the audience's own declarations: the union, each scope once, sorted. The
// module named no kind of the audience's; the calls did.
func TestABindingsCallsResolveToWhatTheAudienceDeclares(t *testing.T) {
	resolved, err := callsContract(t).Resolve(callsValues(), WithCallees(storeDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	evidence, model := resolved.Bindings[0], resolved.Bindings[1]
	if got := strings.Join(evidence.Scopes["invoke"], ","); got != "documents.collection:read,documents:read" {
		t.Fatalf("evidence invoke scopes %q", got)
	}
	if got := strings.Join(evidence.Scopes["lookup"], ","); got != "documents:read" {
		t.Fatalf("a written ceiling beside calls on another operation stays as written: %q", got)
	}
	if got := strings.Join(evidence.Calls["invoke"], ","); got != "/documents.v1.DocumentsService/ReadDocumentExcerpt,/documents.v1.DocumentsService/ResolveEvidence" {
		t.Fatalf("evidence calls %q", got)
	}
	if got := strings.Join(model.Scopes["invoke"], ","); got != "modelservice.profiles:invoke,modelservice.profiles:read" {
		t.Fatalf("model invoke scopes %q", got)
	}
}

// With no callee declarations a binding that names calls has no ceiling to
// carry, and the resolution says so rather than delivering it empty; a call the
// audience does not serve is refused by name.
func TestACallWithNoDeclarationToReadIsRefusedByName(t *testing.T) {
	_, err := callsContract(t).Resolve(callsValues())
	if !errors.Is(err, ErrUnresolvedCall) || !strings.Contains(err.Error(), "binding evidence invoke names calls") {
		t.Fatalf("no callees: %v", err)
	}
	partial := declaredCallees{"documents": storeDeclarations["documents"]}
	_, err = callsContract(t).Resolve(callsValues(), WithCallees(partial))
	if !errors.Is(err, ErrUnresolvedCall) || !strings.Contains(err.Error(), "call GET /v1/model/profiles at model") {
		t.Fatalf("an unserved call: %v", err)
	}
	// An unresolved slot and an unresolved call are both reported, each
	// under its own sentinel.
	_, err = callsContract(t).Resolve(MapValues{Public: map[string]map[string]string{"assistant": {"evidence-audience": "documents", "evidence-binding": "evidence"}}}, WithCallees(storeDeclarations))
	if !errors.Is(err, ErrUnresolvedSlot) {
		t.Fatalf("the missing model audience: %v", err)
	}
}
