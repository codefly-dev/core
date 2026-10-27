package runnable

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

/*
How one call to a prepared binding is addressed.

A prepared binding says which typed route its owner is reached on, and each
route spells one call differently: a Connect procedure is POSTed with the
input as its body, and a REST route is called with its own verb — POST or PUT
carrying the input as the body, GET carrying it as the query. Every caller of a
prepared binding needs that same translation, and there is more than one: the
runtime's task worker dispatches an installed binding, and robin's chat turn
calls one as the asking person. Writing it in each would put the wire form of
HTTPRoute — which this package defines and documents (docs/runnable.md, "The
REST reader") — in two places that can disagree about the same owner, so it is
one function here instead.

It addresses the call and nothing else. What authorizes the call, what headers
it carries, which client dials it, how an answer is classified and where a
receipt is read stay with the caller: those differ between a durable task and a
chat turn, and the request line does not.
*/

// CallRequest is how one call to a prepared binding's operation is sent: the
// method, the absolute path, the query and the body. Path and Query are
// relative to the binding's resolved address, which a caller joins and holds
// to its own transport rule — this says nothing about where the owner is.
type CallRequest struct {
	// Method is POST for a Connect procedure, and the route's own verb for a
	// REST one.
	Method string
	// Path is the procedure or the route's path, beginning with "/".
	Path string
	// Query is a GET's input object spelled as its query, and empty for every
	// other call. It is already encoded, and sorted by parameter name so one
	// input produces one request line.
	Query string
	// Body is the input document a POST or PUT carries, and nil for a GET — a
	// GET's input is its query, so a body beside it would be the same input
	// stated twice.
	Body []byte
	// Connect reports a Connect procedure, which carries the protocol version
	// header. A REST route is the owner's own route and carries none.
	Connect bool
}

// CallRequestFor addresses one call to the operation a prepared binding names,
// carrying input — the operation's own bounded input document, as the caller
// holds it. The binding is read as delivered: a route arm that is not set, a
// verb outside the three HTTPRoute admits, or a path that is not absolute is
// refused by name rather than dialed.
//
// It does not re-verify the binding. VerifyPrepared is what refuses a binding a
// caller must not act on, including holding a delivered GET to being a read,
// and a caller runs it once when it installs the binding rather than on every
// call.
func CallRequestFor(binding *runnablev0.PreparedBinding, input []byte) (CallRequest, error) {
	switch route := binding.GetCall().GetRoute().(type) {
	case *runnablev0.PreparedCall_Connect:
		procedure := route.Connect.GetProcedure()
		if !absolutePath(procedure) {
			return CallRequest{}, fmt.Errorf("%w: prepared binding's Connect procedure %q is not an absolute path", ErrInvalid, procedure)
		}
		return CallRequest{Method: http.MethodPost, Path: procedure, Body: input, Connect: true}, nil
	case *runnablev0.PreparedCall_Rest:
		verb, path := strings.ToUpper(route.Rest.GetVerb()), route.Rest.GetPath()
		if !absolutePath(path) {
			return CallRequest{}, fmt.Errorf("%w: prepared binding's REST route %q is not an absolute path", ErrInvalid, path)
		}
		switch verb {
		case http.MethodPost, http.MethodPut:
			return CallRequest{Method: verb, Path: path, Body: input}, nil
		case http.MethodGet:
			query, err := CallQuery(input)
			if err != nil {
				return CallRequest{}, fmt.Errorf("%w: %s", err, Route(verb, path))
			}
			return CallRequest{Method: verb, Path: path, Query: query}, nil
		default:
			return CallRequest{}, fmt.Errorf("%w: prepared binding's REST verb %q is none of GET, POST or PUT", ErrInvalid, verb)
		}
	default:
		return CallRequest{}, fmt.Errorf("%w: prepared binding for operation %q names no route to call",
			ErrInvalid, binding.GetOperation().GetSpelling())
	}
}

// CallQuery spells a runnable GET's input object as its query: each member one
// parameter of that name, a string as written, a number or a boolean in its
// JSON spelling, and the empty object as the empty query. It is the encoding
// side of the rule PackageFromOpenAPIOperation derives a GET under — a query
// parameter is one scalar per name and never nullable — so a member that is
// null, an object or an array is refused rather than spelled some other way:
// the owner published no parameter a query string can carry it in, and
// inventing one would send the owner an input it never described.
//
// The parameters are sorted by name (url.Values.Encode), so one input object
// addresses one request line whatever order its members were written in.
func CallQuery(input []byte) (string, error) {
	if len(input) == 0 {
		return "", fmt.Errorf("%w: a GET carries its input as its query, and this call carries no input document", ErrInvalid)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(input, &members); err != nil {
		return "", fmt.Errorf("%w: a GET's input is one JSON object, and this call's is not: %v", ErrInvalid, err)
	}
	if members == nil {
		return "", fmt.Errorf("%w: a GET's input must be a JSON object, not null", ErrInvalid)
	}
	values := url.Values{}
	for name, raw := range members {
		spelled, err := queryValue(raw)
		if err != nil {
			return "", fmt.Errorf("%w: query parameter %q %w", ErrInvalid, name, err)
		}
		values.Set(name, spelled)
	}
	return values.Encode(), nil
}

// queryValue spells one member of a GET's input as its query value. A string
// goes in as written — url.Values escapes it — and a number or a boolean in
// the JSON spelling the document published it under, which for a number is the
// token as the caller wrote it rather than a float reformatted through a parse.
func queryValue(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", fmt.Errorf("carries no value")
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", fmt.Errorf("is not a JSON string: %v", err)
		}
		return text, nil
	case '{', '[':
		return "", fmt.Errorf("is an object or an array, and a query carries one scalar per name")
	case 'n':
		return "", fmt.Errorf("is null, which a query string cannot spell")
	case 't', 'f':
		if trimmed != "true" && trimmed != "false" {
			return "", fmt.Errorf("is not a JSON value")
		}
		return trimmed, nil
	default:
		if _, err := strconv.ParseFloat(trimmed, 64); err != nil {
			return "", fmt.Errorf("is not a JSON number: %v", err)
		}
		return trimmed, nil
	}
}

// absolutePath holds a route to an absolute path with no query, fragment or
// space of its own: the query of a call is the input's,
// and a route carrying one would put a second input on the wire.
func absolutePath(route string) bool {
	return strings.HasPrefix(route, "/") && !strings.ContainsAny(route, "?# ")
}
