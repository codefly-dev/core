package runnable_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
)

func routedBinding(verb, path string) *runnablev0.PreparedBinding {
	return &runnablev0.PreparedBinding{
		Operation: &runnablev0.PreparedOperation{Module: "releases", Service: "backend", Endpoint: "http", Spelling: runnable.Route(verb, path)},
		Call: &runnablev0.PreparedCall{
			Address: "http://localhost:36201",
			Route:   &runnablev0.PreparedCall_Rest{Rest: &runnablev0.HTTPRoute{Verb: verb, Path: path}},
		},
	}
}

func procedureBinding(procedure string) *runnablev0.PreparedBinding {
	return &runnablev0.PreparedBinding{
		Operation: &runnablev0.PreparedOperation{Module: "documents", Service: "documents", Endpoint: "connect", Spelling: procedure},
		Call: &runnablev0.PreparedCall{
			Address: "http://localhost:36202",
			Route:   &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: procedure}},
		},
	}
}

// A Connect procedure is POSTed with the input as its body, and carries the
// protocol version. That is what every caller did before a REST route had a
// request line of its own, so it is pinned here first.
func TestAConnectProcedureIsPostedWithTheInputAsItsBody(t *testing.T) {
	request, err := runnable.CallRequestFor(procedureBinding("/documents.v1.Documents/CountDocuments"), []byte(`{"collection":"c1"}`))
	if err != nil {
		t.Fatalf("addressing a Connect call: %v", err)
	}
	if request.Method != http.MethodPost || request.Path != "/documents.v1.Documents/CountDocuments" {
		t.Fatalf("method %q path %q", request.Method, request.Path)
	}
	if request.Query != "" || string(request.Body) != `{"collection":"c1"}` || !request.Connect {
		t.Fatalf("query %q body %q connect %v", request.Query, request.Body, request.Connect)
	}
}

// A REST mutation keeps its body and its own verb, and is not a Connect call:
// an owner's own route carries no Connect protocol header.
func TestARestMutationKeepsItsBodyAndIsNotAConnectCall(t *testing.T) {
	for _, verb := range []string{http.MethodPost, http.MethodPut} {
		request, err := runnable.CallRequestFor(routedBinding(verb, "/records/apply"), []byte(`{"value":3}`))
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if request.Method != verb || request.Path != "/records/apply" || request.Query != "" {
			t.Fatalf("%s: method %q path %q query %q", verb, request.Method, request.Path, request.Query)
		}
		if string(request.Body) != `{"value":3}` || request.Connect {
			t.Fatalf("%s: body %q connect %v", verb, request.Body, request.Connect)
		}
	}
}

// A GET carries its input as its query and no body: each member one parameter
// of that name, a string as written, a number or a boolean in its JSON
// spelling.
func TestAGetCarriesItsInputAsItsQueryAndNoBody(t *testing.T) {
	request, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/history"),
		[]byte(`{"repo":"obin-ai/handbook","limit":25,"ratio":1.5,"all":true}`))
	if err != nil {
		t.Fatalf("addressing a GET: %v", err)
	}
	if request.Method != http.MethodGet || request.Path != "/v1/releases/history" {
		t.Fatalf("method %q path %q", request.Method, request.Path)
	}
	if request.Body != nil {
		t.Fatalf("a GET carries no body, and this one carries %q", request.Body)
	}
	values, err := url.ParseQuery(request.Query)
	if err != nil {
		t.Fatalf("the query does not parse: %v", err)
	}
	for name, want := range map[string]string{"repo": "obin-ai/handbook", "limit": "25", "ratio": "1.5", "all": "true"} {
		if got := values.Get(name); got != want {
			t.Fatalf("query %s=%q, want %q (whole query %q)", name, got, want, request.Query)
		}
	}
	if len(values) != 4 {
		t.Fatalf("the query carries %d parameters, want 4: %q", len(values), request.Query)
	}
}

// One input object addresses one request line: the parameters are sorted by
// name, so two spellings of the same object do not reach the owner as two
// different requests (and so an effect keyed by the call is the same effect).
func TestOneInputObjectAddressesOneRequestLine(t *testing.T) {
	first, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/history"), []byte(`{"repo":"a","component":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/history"), []byte(`{"component":"b","repo":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Query != second.Query {
		t.Fatalf("the same object addressed two request lines: %q and %q", first.Query, second.Query)
	}
	if first.Query != "component=b&repo=a" {
		t.Fatalf("query %q", first.Query)
	}
}

// A GET with no parameter takes the empty object, and its query is empty.
func TestAGetWithNoParameterTakesTheEmptyObject(t *testing.T) {
	request, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/recorded"), []byte(`{}`))
	if err != nil {
		t.Fatalf("the empty object is a GET's input: %v", err)
	}
	if request.Query != "" || request.Body != nil {
		t.Fatalf("query %q body %q", request.Query, request.Body)
	}
}

// A string member is escaped rather than spelled into the query raw: a value
// carrying an ampersand is one parameter, never two.
func TestAStringQueryValueIsEscapedAndStaysOneParameter(t *testing.T) {
	request, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/history"), []byte(`{"repo":"a&limit=99"}`))
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(request.Query)
	if err != nil {
		t.Fatalf("the query does not parse: %v", err)
	}
	if len(values) != 1 || values.Get("repo") != "a&limit=99" {
		t.Fatalf("a value with an ampersand became %d parameters: %q", len(values), request.Query)
	}
}

// What a query string cannot spell is refused by name, never coerced: core
// derives a GET only from scalar, non-nullable query parameters, so a null, an
// object or an array means the delivered input does not match the operation's
// published contract.
func TestAGetMemberAQueryCannotSpellIsRefusedByName(t *testing.T) {
	for name, input := range map[string]string{
		"null":      `{"repo":null}`,
		"an object": `{"repo":{"name":"handbook"}}`,
		"an array":  `{"repo":["a","b"]}`,
	} {
		_, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/history"), []byte(input))
		if err == nil {
			t.Fatalf("%s was accepted as a query value", name)
		}
		if !strings.Contains(err.Error(), `"repo"`) {
			t.Fatalf("%s: the refusal does not name the parameter: %v", name, err)
		}
	}
}

// An input that is not one JSON object is refused: a GET's input is an object
// whose members are its parameters, and there is nothing else to spell.
func TestAGetInputThatIsNotOneObjectIsRefused(t *testing.T) {
	for name, input := range map[string]string{
		"an array": `[1,2]`,
		"a string": `"repo"`,
		"nothing":  ``,
		"not JSON": `{`,
	} {
		if _, err := runnable.CallRequestFor(routedBinding(http.MethodGet, "/v1/releases/history"), []byte(input)); err == nil {
			t.Fatalf("%s was accepted as a GET's input", name)
		}
	}
}

// A verb outside the three HTTPRoute admits is refused by name rather than
// dialed. core's own validation refuses one on the wire; this is the caller's
// half of the same rule, for a value that reached it another way.
func TestARestVerbOutsideTheThreeIsRefusedByName(t *testing.T) {
	for _, verb := range []string{http.MethodDelete, http.MethodPatch, "HEAD", "OPTIONS"} {
		_, err := runnable.CallRequestFor(routedBinding(verb, "/records/apply"), []byte(`{"value":1}`))
		if err == nil {
			t.Fatalf("%s was addressed as a runnable call", verb)
		}
		if !strings.Contains(err.Error(), verb) {
			t.Fatalf("%s: the refusal does not name the verb: %v", verb, err)
		}
	}
}

// A route that is not an absolute path, or that carries a query of its own, is
// refused: the query of a call is the input's, and a route carrying one would
// put a second input on the wire.
func TestARouteThatIsNotAnAbsolutePathIsRefused(t *testing.T) {
	for _, path := range []string{"v1/releases", "/v1/releases?limit=1", "/v1/releases#top", "/v1/releases/", "/v1 releases"} {
		if _, err := runnable.CallRequestFor(routedBinding(http.MethodGet, path), []byte(`{}`)); err == nil {
			t.Fatalf("route %q was addressed", path)
		}
		if _, err := runnable.CallRequestFor(procedureBinding(path), []byte(`{}`)); err == nil {
			t.Fatalf("procedure %q was addressed", path)
		}
	}
}

// A binding with no route arm reaches nothing, and says so naming the
// operation an operator is looking for.
func TestABindingWithNoRouteIsRefusedNamingItsOperation(t *testing.T) {
	binding := &runnablev0.PreparedBinding{
		Operation: &runnablev0.PreparedOperation{Module: "releases", Service: "backend", Endpoint: "http", Spelling: "GET /v1/releases/history"},
		Call:      &runnablev0.PreparedCall{Address: "http://localhost:36201"},
	}
	_, err := runnable.CallRequestFor(binding, []byte(`{}`))
	if err == nil {
		t.Fatal("a binding with no route was addressed")
	}
	if !strings.Contains(err.Error(), "GET /v1/releases/history") {
		t.Fatalf("the refusal does not name the operation: %v", err)
	}
}
