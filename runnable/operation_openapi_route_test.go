package runnable_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

// swaggerDocument reads a Swagger 2.0 fixture, reshaped by edit.
func swaggerDocument(t *testing.T, name string, edit func(map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/openapi/" + name)
	require.NoError(t, err)
	if edit == nil {
		return raw
	}
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	edit(doc)
	edited, err := json.Marshal(doc)
	require.NoError(t, err)
	return edited
}

func releasesOperation(doc map[string]any, path string) map[string]any {
	return doc["paths"].(map[string]any)[path].(map[string]any)["get"].(map[string]any)
}

// releasesOwner publishes the releases solution's backend on its http
// endpoint, as a solution's API catalog names it.
func releasesOwner() runnable.ServiceOwner {
	owner := ingestOwner()
	owner.Module, owner.Service, owner.Endpoint = "releases", "backend", "http"
	return owner
}

func releasesLocation(name string) *resources.RunnableLocation {
	location := ingestLocation()
	location.Identity.Module, location.Identity.Name = "releases", name
	return location
}

// TestTheTwoDialectsDeriveOneOperation is the dialect half of the drift gate:
// the same operation written as Swagger 2.0 — its body an `in: body`
// parameter, its response schema on the response, its media types listed once
// for the document, its schemas under definitions — derives byte for byte the
// package its OpenAPI 3 spelling does. Nothing in a package records which
// dialect described it, so any difference is the reader's.
func TestTheTwoDialectsDeriveOneOperation(t *testing.T) {
	openAPI, openAPISpec := derivedRestIngestion(t)
	swagger, swaggerSpec, err := runnable.PackageFromOpenAPIOperation(
		swaggerDocument(t, "ingestion.swagger.json", nil), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
	require.NoError(t, err)
	require.True(t, proto.Equal(openAPI, swagger), "openapi %v\nswagger %v", openAPI, swagger)
	require.Equal(t, openAPI.GetDigest(), swagger.GetDigest())
	require.True(t, proto.Equal(openAPISpec.Policy(), swaggerSpec.Policy()))
}

// TestSwaggerIsHeldToTheSameRules shows the dialect is read, not trusted: each
// Swagger spelling of a shape the profile refuses is refused by the rule, and
// in the words, its OpenAPI 3 spelling is.
func TestSwaggerIsHeldToTheSameRules(t *testing.T) {
	post := func(doc map[string]any) map[string]any {
		return doc["paths"].(map[string]any)[ingestPath].(map[string]any)["post"].(map[string]any)
	}
	for _, test := range []struct {
		name    string
		edit    func(map[string]any)
		because string
	}{
		{name: "two request encodings", edit: func(doc map[string]any) {
			post(doc)["consumes"] = []any{"application/json", "application/xml"}
		}, because: "request body is carried as 2 media types"},
		{name: "not json", edit: func(doc map[string]any) {
			doc["produces"] = []any{"application/x-protobuf"}
		}, because: `response is carried as "application/x-protobuf"`},
		{name: "no media type listed", edit: func(doc map[string]any) {
			delete(doc, "consumes")
		}, because: "request body is carried as 0 media types"},
		{name: "form data", edit: func(doc map[string]any) {
			post(doc)["parameters"] = []any{map[string]any{"name": "text", "in": "formData", "type": "string"}}
		}, because: `"formData" parameter "text"`},
		{name: "no body", edit: func(doc map[string]any) {
			delete(post(doc), "parameters")
		}, because: "declares no request body"},
		{name: "response without schema", edit: func(doc map[string]any) {
			post(doc)["responses"] = map[string]any{"200": map[string]any{"description": "applied"}}
		}, because: "response is carried as 0 media types"},
		{name: "a component reference", edit: func(doc map[string]any) {
			post(doc)["parameters"].([]any)[0].(map[string]any)["schema"] = map[string]any{"$ref": "#/components/schemas/ApplyTextRequest"}
		}, because: "is not a #/definitions/ reference"},
		{name: "undefined definition", edit: func(doc map[string]any) {
			delete(doc["definitions"].(map[string]any), "ApplyTextResponse")
		}, because: "which the document does not define"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := runnable.PackageFromOpenAPIOperation(
				swaggerDocument(t, "ingestion.swagger.json", test.edit), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
		})
	}
}

// TestPackageFromOpenAPIOperationDerivesAGetRead covers a solution's read: a
// GET whose query is its input — each parameter a member, required exactly
// when the parameter is — recomputed and presented as a read-only tool.
func TestPackageFromOpenAPIOperationDerivesAGetRead(t *testing.T) {
	pkg, spec, err := runnable.PackageFromOpenAPIOperation(
		swaggerDocument(t, "releases.swagger.json", nil), releasesLocation("backend-http-history"), releasesOwner(), "GET", "/history")
	require.NoError(t, err)
	require.NoError(t, runnable.VerifyPackage(pkg))
	require.Equal(t, "GET /history", spec.Method)

	require.True(t, proto.Equal(&basev0.RunnableSchema{Fields: []*basev0.RunnableField{
		{Name: "repo", Type: basev0.RunnableField_STRING},
		{Name: "component", Type: basev0.RunnableField_STRING, Optional: true},
	}}, pkg.GetContract().GetInput()), "input %v", pkg.GetContract().GetInput())
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, pkg.GetExecution().GetRecovery())
	require.True(t, proto.Equal(&basev0.RunnableServiceOperation{
		Module:   "releases",
		Name:     "backend",
		Endpoint: "http",
		// No component describes a query: the operation publishes it.
		Operation:     "GET /history",
		InputMessage:  "Releases_Get_History",
		OutputMessage: "History",
		Adaptation:    basev0.RunnableServiceOperation_ADAPTATION_BOUNDED_JSON_V1,
	}, pkg.GetServiceOperations()[0]), "service operation %v", pkg.GetServiceOperations()[0])
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, spec.Tool.GetEffect())

	// A GET with no query takes the empty object.
	recorded, _, err := runnable.PackageFromOpenAPIOperation(
		swaggerDocument(t, "releases.swagger.json", nil), releasesLocation("backend-http-recorded"), releasesOwner(), "GET", "/recorded")
	require.NoError(t, err)
	require.Empty(t, recorded.GetContract().GetInput().GetFields())
	require.Equal(t, "Board", recorded.GetServiceOperations()[0].GetOutputMessage())
}

// TestAGetIsAdmittedOnlyAsARead holds each half of a GET's operation to being
// a read, and refuses a GET whose input is not one flat query.
func TestAGetIsAdmittedOnlyAsARead(t *testing.T) {
	marker := func(doc map[string]any) map[string]any {
		return releasesOperation(doc, "/history")[runnable.OpenAPIOperationMarker].(map[string]any)
	}
	for _, test := range []struct {
		name    string
		edit    func(map[string]any)
		because string
	}{
		{name: "resolved by a receipt", edit: func(doc map[string]any) {
			declared := marker(doc)
			declared["recovery"], declared["max_attempts"], declared["retryable_codes"] = "RECOVERY_RECEIPT", 1, []any{}
			declared["tool"].(map[string]any)["effect"] = "EFFECT_MUTATION"
		}, because: "is a GET, which is admitted only as a read"},
		{name: "invoked holding a write", edit: func(doc map[string]any) {
			marker(doc)["invoke_scopes"] = []any{map[string]any{"resource_kind": "records.records", "actions": []any{"read", "write"}}}
		}, because: "is a GET, which is admitted only as a read"},
		{name: "a body", edit: func(doc map[string]any) {
			releasesOperation(doc, "/history")["parameters"] = []any{
				map[string]any{"name": "body", "in": "body", "schema": map[string]any{"$ref": "#/definitions/History"}},
			}
		}, because: "declares a request body, and a GET's input is its query"},
		{name: "a path parameter", edit: func(doc map[string]any) {
			releasesOperation(doc, "/history")["parameters"] = []any{map[string]any{"name": "repo", "in": "path", "type": "string"}}
		}, because: `"path" parameter "repo"`},
		{name: "a list in the query", edit: func(doc map[string]any) {
			releasesOperation(doc, "/history")["parameters"] = []any{
				map[string]any{"name": "repo", "in": "query", "schema": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
			}
		}, because: `query parameter "repo" is ARRAY`},
		{name: "an object in the query", edit: func(doc map[string]any) {
			releasesOperation(doc, "/history")["parameters"] = []any{
				map[string]any{"name": "filter", "in": "query", "schema": map[string]any{"type": "object"}},
			}
		}, because: `query parameter "filter" is JSON`},
		{name: "no operationId", edit: func(doc map[string]any) {
			delete(releasesOperation(doc, "/history"), "operationId")
		}, because: "declares no operationId"},
		{name: "an untyped query parameter", edit: func(doc map[string]any) {
			releasesOperation(doc, "/history")["parameters"] = []any{map[string]any{"name": "repo", "in": "query"}}
		}, because: `query parameter "repo" declares no schema`},
		{name: "a query parameter twice", edit: func(doc map[string]any) {
			releasesOperation(doc, "/history")["parameters"] = []any{
				map[string]any{"name": "repo", "in": "query", "type": "string"},
				map[string]any{"name": "repo", "in": "query", "type": "string"},
			}
		}, because: `declares query parameter "repo" twice`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := runnable.PackageFromOpenAPIOperation(
				swaggerDocument(t, "releases.swagger.json", test.edit), releasesLocation("backend-http-history"), releasesOwner(), "GET", "/history")
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
		})
	}

	// A query is a GET's input alone: a POST still carries its input as a body.
	_, _, err := runnable.PackageFromOpenAPIOperation(
		ingestionDocument(t, func(doc map[string]any) {
			applyTextOperation(doc)["parameters"] = []any{map[string]any{"name": "tenant", "in": "query", "schema": map[string]any{"type": "string"}}}
		}), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, `"query" parameter "tenant"`)
}

// TestDefaultRouteOperationIsTheMarkerAnOwnerPublishes covers the REST twin of
// DefaultOperation: what an owner that declares only a route's authority
// policy writes as its marker. A read is core's default read, presented by the
// route's summary; written as the marker and read back, it derives the
// operation the fixture declares by hand.
func TestDefaultRouteOperationIsTheMarkerAnOwnerPublishes(t *testing.T) {
	reads := &runnable.Authority{Requires: []*basev0.WorkScopeV1{{ResourceKind: "records.records", Actions: []string{"read"}}}}
	spec, err := runnable.DefaultRouteOperation("GET", "/history", reads, "releases", "Releases_Get_History",
		"Read the recorded tags of one repository and the recorded pins of one component")
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, spec.Recovery)
	require.Equal(t, uint32(runnable.DefaultReadAttempts), spec.MaxAttempts)
	require.Equal(t, []string{"503"}, spec.RetryableCodes)
	require.Equal(t, 30*time.Second, spec.AttemptTimeout)
	require.True(t, proto.Equal(&runnablev0.ToolExposure{
		Name:        "Releases_Get_History",
		Description: "Read the recorded tags of one repository and the recorded pins of one component",
		Effect:      runnablev0.ToolExposure_EFFECT_READ_ONLY,
	}, spec.Tool))

	// RouteMarker is that operation as the marker an owner writes, from the
	// policy JSON the owner already publishes beside it.
	written, err := runnable.RouteMarker("GET", "/history",
		json.RawMessage(`{"requires":[{"resource_kind":"records.records","actions":["read"]}]}`), "releases", "Releases_Get_History",
		"Read the recorded tags of one repository and the recorded pins of one component")
	require.NoError(t, err)
	again, err := runnable.RouteMarker("GET", "/history",
		json.RawMessage(`{"requires":[{"resource_kind":"records.records","actions":["read"]}]}`), "releases", "Releases_Get_History",
		"Read the recorded tags of one repository and the recorded pins of one component")
	require.NoError(t, err)
	require.Equal(t, string(written), string(again), "a committed marker is written byte-stably")
	_, err = runnable.RouteMarker("GET", "/history", json.RawMessage(`{"require":[]}`), "releases", "", "")
	require.ErrorIs(t, err, runnable.ErrInvalid)
	_, err = runnable.RouteMarker("GET", "/status", json.RawMessage(`{"any_checked_caller":true}`), "releases", "", "")
	require.ErrorIs(t, err, runnable.ErrNotDerivable)
	fromDefault, _, err := runnable.PackageFromOpenAPIOperation(
		swaggerDocument(t, "releases.swagger.json", func(doc map[string]any) {
			var marker map[string]any
			require.NoError(t, json.Unmarshal(written, &marker))
			releasesOperation(doc, "/history")[runnable.OpenAPIOperationMarker] = marker
		}), releasesLocation("backend-http-history"), releasesOwner(), "GET", "/history")
	require.NoError(t, err)
	byHand, _, err := runnable.PackageFromOpenAPIOperation(
		swaggerDocument(t, "releases.swagger.json", nil), releasesLocation("backend-http-history"), releasesOwner(), "GET", "/history")
	require.NoError(t, err)
	require.Equal(t, byHand.GetDigest(), fromDefault.GetDigest())

	// Anything else is a mutation resolved by its receipt, over POST or PUT.
	writes := &runnable.Authority{Requires: []*basev0.WorkScopeV1{{ResourceKind: "records.records", Actions: []string{"read", "write"}}}}
	mutation, err := runnable.DefaultRouteOperation("POST", "/record", writes, "releases", "Releases_Post_Record", "")
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECEIPT, mutation.Recovery)
	require.Equal(t, uint32(runnable.DefaultMutationAttempts), mutation.MaxAttempts)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_MUTATION, mutation.Tool.GetEffect())
	require.Equal(t, "Calls POST /record.", mutation.Tool.GetDescription())

	// Neither a mutation over GET nor a policy requiring nothing is derived.
	for name, refused := range map[string]func() error{
		"mutation over GET": func() error {
			_, err := runnable.DefaultRouteOperation("GET", "/record", writes, "releases", "", "")
			return err
		},
		"path template": func() error {
			_, err := runnable.DefaultRouteOperation("GET", "/modules/records/{path=**}", reads, "releases", "", "")
			return err
		},
		"any checked caller": func() error {
			_, err := runnable.DefaultRouteOperation("GET", "/status", &runnable.Authority{AnyCheckedCaller: true}, "releases", "", "")
			return err
		},
		"no policy": func() error {
			_, err := runnable.DefaultRouteOperation("POST", "/record", nil, "releases", "", "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, refused(), runnable.ErrNotDerivable)
		})
	}
}
