package runnable_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

const ingestPath = "/ingest/text"

// ingestionDocument is the REST twin of the ingestion service: the same
// operation, published over HTTP and described by OpenAPI instead of by a
// descriptor. edit reshapes it for the case under test.
func ingestionDocument(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/openapi/ingestion.json")
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

func pathItem(doc map[string]any) map[string]any {
	return doc["paths"].(map[string]any)[ingestPath].(map[string]any)
}

func applyTextOperation(doc map[string]any) map[string]any {
	return pathItem(doc)["post"].(map[string]any)
}

// ingestRestOwner publishes the same operation on the service's http endpoint.
func ingestRestOwner() runnable.ServiceOwner {
	owner := ingestOwner()
	owner.Endpoint = "rest"
	return owner
}

func derivedRestIngestion(t *testing.T) (*basev0.RunnablePackage, *runnable.OperationSpec) {
	t.Helper()
	pkg, spec, err := runnable.PackageFromOpenAPIOperation(
		ingestionDocument(t, nil), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
	require.NoError(t, err)
	return pkg, spec
}

func TestPackageFromOpenAPIOperationIsByteStable(t *testing.T) {
	pkg, spec := derivedRestIngestion(t)
	require.NoError(t, runnable.VerifyPackage(pkg))
	require.Equal(t, applyTextRoute, spec.Method)
	require.Equal(t, "3bb7c8f93cf2e9c0a9320d47451ad8a32ad54eceb4a8c5e9955fb4077d608d6f", pkg.GetDigest())

	again, _ := derivedRestIngestion(t)
	require.True(t, proto.Equal(pkg, again))
	require.Equal(t, pkg.GetDigest(), again.GetDigest())
}

func TestPackageFromOpenAPIOperationDerivesTheWholePackage(t *testing.T) {
	pkg, spec := derivedRestIngestion(t)

	require.Equal(t, runnable.PackageSchemaV1, pkg.GetSchema())
	require.True(t, proto.Equal(&basev0.RunnableIdentity{
		Workspace: "module-document-store", Module: "documents", Name: "ingest-text", Version: "0.1.0"}, pkg.GetIdentity()))
	require.True(t, proto.Equal(ingestOwner().Agent, pkg.GetAgent()))
	require.Equal(t, resources.RunnableServiceProtocolV1, pkg.GetContract().GetProtocol())

	require.True(t, proto.Equal(&basev0.RunnableExecution{
		Facilities:     []*basev0.RunnableFacility{{Kind: basev0.RunnableFacility_SERVICE}},
		Timeout:        durationpb.New(spec.TotalTimeout),
		Cancellation:   basev0.RunnableExecution_CANCELLATION_NONE,
		Recovery:       basev0.RunnableExecution_RECOVERY_RECEIPT,
		MaxInputBytes:  resources.DefaultRunnablePayloadBytes,
		MaxOutputBytes: resources.DefaultRunnablePayloadBytes,
	}, pkg.GetExecution()), "execution %v", pkg.GetExecution())

	// The operation is the owner's own spelling of the route, and the message
	// identities are the components the document describes the payloads by.
	require.Len(t, pkg.GetServiceOperations(), 1)
	require.True(t, proto.Equal(&basev0.RunnableServiceOperation{
		Module:        "documents",
		Name:          "runtime-worker",
		Endpoint:      "rest",
		Operation:     applyTextRoute,
		InputMessage:  "ApplyTextRequest",
		OutputMessage: "ApplyTextResponse",
		Adaptation:    basev0.RunnableServiceOperation_ADAPTATION_BOUNDED_JSON_V1,
	}, pkg.GetServiceOperations()[0]), "service operation %v", pkg.GetServiceOperations()[0])
}

// TestTheTwoBuildersDeriveOneOperation is the package-level half of the
// drift gate: the same operation published over gRPC and over HTTP derives one
// contract and one execution policy, and differs only where the transport
// genuinely differs — the endpoint it is reached on, the route that names it,
// and the identities the owner publishes its messages under.
func TestTheTwoBuildersDeriveOneOperation(t *testing.T) {
	overGRPC, _ := derivedIngestion(t)
	overREST, _ := derivedRestIngestion(t)

	require.True(t, proto.Equal(overGRPC.GetContract(), overREST.GetContract()),
		"contracts differ:\ngrpc %v\nrest %v", overGRPC.GetContract(), overREST.GetContract())
	require.True(t, proto.Equal(overGRPC.GetExecution(), overREST.GetExecution()))
	require.True(t, proto.Equal(overGRPC.GetIdentity(), overREST.GetIdentity()))
	require.True(t, proto.Equal(overGRPC.GetAgent(), overREST.GetAgent()))

	grpcOperation, restOperation := overGRPC.GetServiceOperations()[0], overREST.GetServiceOperations()[0]
	require.Equal(t, grpcOperation.GetModule(), restOperation.GetModule())
	require.Equal(t, grpcOperation.GetName(), restOperation.GetName())
	require.Equal(t, grpcOperation.GetAdaptation(), restOperation.GetAdaptation())
	require.Equal(t, "grpc", grpcOperation.GetEndpoint())
	require.Equal(t, "rest", restOperation.GetEndpoint())
	require.Equal(t, applyText, grpcOperation.GetOperation())
	require.Equal(t, applyTextRoute, restOperation.GetOperation())

	// The two packages are therefore different releases of one contract, not
	// one release: the digest covers where the operation is reached.
	require.NotEqual(t, overGRPC.GetDigest(), overREST.GetDigest())
}

func TestPackageFromOpenAPIOperationAnswersAnUnmarkedRoute(t *testing.T) {
	_, _, err := runnable.PackageFromOpenAPIOperation(
		ingestionDocument(t, func(doc map[string]any) {
			delete(applyTextOperation(doc), runnable.OpenAPIOperationMarker)
		}), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
	require.ErrorIs(t, err, runnable.ErrNotAnOperation)
	require.ErrorContains(t, err, applyTextRoute)
}

// TestPackageFromOpenAPIOperationAcceptsAHeaderParameter guards the edge of the
// parameter rule: what the input must be free of is what would have to be
// folded into the payload, and a header is carried by the transport.
func TestPackageFromOpenAPIOperationAcceptsAHeaderParameter(t *testing.T) {
	_, _, err := runnable.PackageFromOpenAPIOperation(
		ingestionDocument(t, func(doc map[string]any) {
			applyTextOperation(doc)["parameters"] = []any{
				map[string]any{"name": "Idempotency-Key", "in": "header", "required": true},
			}
		}), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
	require.NoError(t, err)
}

func TestPackageFromOpenAPIOperationDerivesAPut(t *testing.T) {
	pkg, _, err := runnable.PackageFromOpenAPIOperation(
		ingestionDocument(t, func(doc map[string]any) {
			item := pathItem(doc)
			item["put"], item["post"] = item["post"], nil
			delete(item, "post")
		}), ingestLocation(), ingestRestOwner(), "PUT", ingestPath)
	require.NoError(t, err)
	require.Equal(t, "PUT "+ingestPath, pkg.GetServiceOperations()[0].GetOperation())
}

func TestPackageFromOpenAPIOperationRefusesWhatABoundedContractCannotDescribe(t *testing.T) {
	for _, test := range []struct {
		name    string
		method  string
		path    string
		edit    func(map[string]any)
		because string
	}{
		{name: "get", method: "GET", because: "is not POST or PUT"},
		{name: "delete", method: "DELETE", because: "is not POST or PUT"},
		{name: "patch", method: "PATCH", because: "is not POST or PUT"},
		{name: "unpublished path", path: "/ingest/blob", because: "publishes no path"},
		{name: "unpublished method", edit: func(doc map[string]any) {
			delete(pathItem(doc), "post")
		}, because: "publishes no POST"},
		{name: "path item $ref", edit: func(doc map[string]any) {
			pathItem(doc)["$ref"] = "#/components/pathItems/Ingest"
		}, because: "$ref to another path item"},
		{name: "path parameter", edit: func(doc map[string]any) {
			applyTextOperation(doc)["parameters"] = []any{map[string]any{"name": "container", "in": "path"}}
		}, because: `path parameter "container"`},
		{name: "query parameter", edit: func(doc map[string]any) {
			applyTextOperation(doc)["parameters"] = []any{map[string]any{"name": "dry_run", "in": "query"}}
		}, because: `query parameter "dry_run"`},
		{name: "inherited path parameter", edit: func(doc map[string]any) {
			pathItem(doc)["parameters"] = []any{map[string]any{"name": "tenant", "in": "query"}}
		}, because: `query parameter "tenant"`},
		{name: "$ref parameter", edit: func(doc map[string]any) {
			applyTextOperation(doc)["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/Tenant"}}
		}, because: "$ref parameter"},
		{name: "no request body", edit: func(doc map[string]any) {
			delete(applyTextOperation(doc), "requestBody")
		}, because: "declares no request body"},
		{name: "$ref request body", edit: func(doc map[string]any) {
			applyTextOperation(doc)["requestBody"] = map[string]any{"$ref": "#/components/requestBodies/ApplyText"}
		}, because: "request body is a $ref"},
		{name: "two request encodings", edit: func(doc map[string]any) {
			content := applyTextOperation(doc)["requestBody"].(map[string]any)["content"].(map[string]any)
			content["application/xml"] = content["application/json"]
		}, because: "carried as 2 media types"},
		{name: "not json", edit: func(doc map[string]any) {
			body := applyTextOperation(doc)["requestBody"].(map[string]any)
			content := body["content"].(map[string]any)
			content["application/x-protobuf"] = content["application/json"]
			delete(content, "application/json")
		}, because: "not carried as application/json"},
		{name: "no request schema", edit: func(doc map[string]any) {
			applyTextOperation(doc)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"] = map[string]any{}
		}, because: "declares no schema"},
		{name: "inline request schema", edit: func(doc map[string]any) {
			applyTextOperation(doc)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"] =
				map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}}
		}, because: "no published identity to record"},
		{name: "two successful responses", edit: func(doc map[string]any) {
			responses := applyTextOperation(doc)["responses"].(map[string]any)
			responses["201"] = responses["200"]
		}, because: "declares 2 successful responses (200, 201)"},
		{name: "no successful response", edit: func(doc map[string]any) {
			applyTextOperation(doc)["responses"] = map[string]any{"400": map[string]any{"description": "bad request"}}
		}, because: "declares 0 successful responses"},
		{name: "empty response", edit: func(doc map[string]any) {
			applyTextOperation(doc)["responses"] = map[string]any{"204": map[string]any{"description": "applied"}}
		}, because: "response is carried as 0 media types"},
		{name: "unprojectable payload", edit: func(doc map[string]any) {
			schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
			schemas["ApplyTextRequest"].(map[string]any)["properties"].(map[string]any)["cursor"] =
				map[string]any{"type": "number"}
		}, because: "#/components/schemas/ApplyTextRequest/properties/cursor"},
	} {
		t.Run(test.name, func(t *testing.T) {
			method, path := test.method, test.path
			if method == "" {
				method = "POST"
			}
			if path == "" {
				path = ingestPath
			}
			_, _, err := runnable.PackageFromOpenAPIOperation(
				ingestionDocument(t, test.edit), ingestLocation(), ingestRestOwner(), method, path)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
		})
	}
}

func TestPackageFromOpenAPIOperationRequiresAReadableDocumentAndARelease(t *testing.T) {
	_, _, err := runnable.PackageFromOpenAPIOperation(
		[]byte(`{"paths":`), ingestLocation(), ingestRestOwner(), "POST", ingestPath)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "not readable OpenAPI JSON")

	for _, location := range []*resources.RunnableLocation{nil, {}} {
		_, _, err = runnable.PackageFromOpenAPIOperation(
			ingestionDocument(t, nil), location, ingestRestOwner(), "POST", ingestPath)
		require.ErrorIs(t, err, runnable.ErrInvalid)
		require.ErrorContains(t, err, "release identity")
	}
}
