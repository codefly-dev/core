package runnable

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

const (
	// componentSchemaPrefix is the only $ref the projection follows. A $ref
	// anywhere else in the operation — its request body, a response, a
	// parameter — is refused rather than resolved: core reads the operation as
	// the document writes it, and the payload schemas are where the issue of
	// one shape described once actually arises.
	componentSchemaPrefix = "#/components/schemas/"
	// jsonMediaType is the one media type a derived operation carries. The
	// bounded profile is a JSON profile, so a route offering another encoding
	// offers a payload the contract cannot describe.
	jsonMediaType = "application/json"
)

// openAPIOperationMethods are the HTTP methods a Runnable operation may be
// published under. An operation applies an effect and is retried under a
// declared policy, which GET and DELETE do not describe and PATCH describes as
// a diff against a state the contract does not carry.
var openAPIOperationMethods = []string{http.MethodPost, http.MethodPut}

// transportParameterLocations are the parameter locations a derived operation
// may carry: the transport carries them and they are no part of the payload.
// Every other location names an input the bounded contract would have to
// describe, and is refused rather than ignored.
var transportParameterLocations = []string{"header", "cookie"}

// supportedOpenAPIMajor is the document version a Runnable operation is derived
// from.
const supportedOpenAPIMajor = "3"

// OpenAPIDocument is the part of an OpenAPI document a Runnable operation is
// derived from, read as JSON. It is deliberately not a model of OpenAPI:
// everything outside the operation's own payloads — servers, security, tags,
// examples — says how the route is served and never what the contract is.
type OpenAPIDocument struct {
	// OpenAPI and Swagger are the two spellings of a document's version. Both
	// are read so a Swagger 2.0 document is refused as one rather than as an
	// OpenAPI 3 document that happens to declare nothing.
	OpenAPI    string                      `json:"openapi"`
	Swagger    string                      `json:"swagger"`
	Paths      map[string]*OpenAPIPathItem `json:"paths"`
	Components OpenAPIComponents           `json:"components"`
}

// OpenAPIComponents holds the schemas a payload $ref names.
type OpenAPIComponents struct {
	Schemas map[string]*JSONSchema `json:"schemas"`
}

// OpenAPIPathItem is one path. Only the methods an operation may be published
// under are read: the others are refused before the document is consulted.
type OpenAPIPathItem struct {
	Ref string `json:"$ref"`
	// Parameters are declared for every operation on the path, so they are as
	// much a part of one operation's shape as the ones it declares itself.
	Parameters []OpenAPIParameter `json:"parameters"`
	Post       *OpenAPIOperation  `json:"post"`
	Put        *OpenAPIOperation  `json:"put"`
}

// OpenAPIOperation is one route's operation object.
type OpenAPIOperation struct {
	Parameters  []OpenAPIParameter      `json:"parameters"`
	RequestBody *OpenAPIBody            `json:"requestBody"`
	Responses   map[string]*OpenAPIBody `json:"responses"`
	// Marker is the x-codefly-operation vendor extension, whose presence marks
	// the operation as a Runnable one.
	Marker json.RawMessage `json:"x-codefly-operation"`
}

// OpenAPIParameter is an input carried outside the request body.
type OpenAPIParameter struct {
	Ref  string `json:"$ref"`
	Name string `json:"name"`
	In   string `json:"in"`
}

// OpenAPIBody is a request body or a response: the media types it is carried
// as, and the schema of each.
type OpenAPIBody struct {
	Ref     string                      `json:"$ref"`
	Content map[string]OpenAPIMediaType `json:"content"`
}

// OpenAPIMediaType is one encoding of a body.
type OpenAPIMediaType struct {
	Schema *JSONSchema `json:"schema"`
}

// ParseOpenAPIDocument reads the OpenAPI JSON a Runnable operation is derived
// from. Keys the derivation does not read are ignored; keys it does are read
// as written.
func ParseOpenAPIDocument(document []byte) (*OpenAPIDocument, error) {
	doc := &OpenAPIDocument{}
	if err := json.Unmarshal(document, doc); err != nil {
		return nil, fmt.Errorf("%w: document is not readable OpenAPI JSON: %v", ErrInvalid, err)
	}
	// The version is checked rather than assumed. Swagger 2.0 describes the
	// same operation in different places — a body is an `in: body` parameter
	// and a response schema hangs off the response itself — so reading one as
	// OpenAPI 3 finds no request body and reports that, pointing at the wrong
	// part of a document that plainly declares one. Core's own
	// OpenAPICombinator still writes 2.0, so this is a document that turns up.
	if doc.Swagger != "" {
		return nil, fmt.Errorf("%w: document declares swagger %q; a Runnable operation is derived from OpenAPI %s, which spells a request body and a response schema in different places", ErrInvalid, doc.Swagger, supportedOpenAPIMajor)
	}
	if major, _, _ := strings.Cut(doc.OpenAPI, "."); major != supportedOpenAPIMajor {
		return nil, fmt.Errorf("%w: document declares openapi %q; a Runnable operation is derived from OpenAPI %s", ErrInvalid, doc.OpenAPI, supportedOpenAPIMajor)
	}
	return doc, nil
}

// componentSchema resolves a $ref against the document's own components.
func (d *OpenAPIDocument) componentSchema(ref string) (*JSONSchema, bool) {
	name, isComponent := strings.CutPrefix(ref, componentSchemaPrefix)
	if d == nil || !isComponent {
		return nil, false
	}
	// A component spelled as null is a key the map reports as present carrying
	// no schema, which is a component the document does not define — not one
	// the walk may descend into.
	schema, defined := d.Components.Schemas[name]
	return schema, defined && schema != nil
}

// Route spells an operation the way a derived package records it: the method
// and the path, as the owner writes them.
func Route(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

// operation finds the operation a route publishes and refuses one whose shape
// a bounded contract cannot describe. Each refusal names what it refused and
// why: v1 keeps an operation's input as one JSON object, and folding a path or
// query parameter into it is a later profile decision rather than a coercion
// core may make now.
func (d *OpenAPIDocument) operation(method, path string) (*OpenAPIOperation, string, error) {
	route := Route(method, path)
	if !slices.Contains(openAPIOperationMethods, strings.ToUpper(method)) {
		return nil, "", fmt.Errorf("%w: %s is not %s, and only those apply an effect a declared policy can retry", ErrInvalid, route, strings.Join(openAPIOperationMethods, " or "))
	}
	item, published := d.Paths[path]
	if !published || item == nil {
		return nil, "", fmt.Errorf("%w: the document publishes no path %s", ErrInvalid, path)
	}
	if item.Ref != "" {
		return nil, "", fmt.Errorf("%w: %s is a $ref to another path item, which core does not follow", ErrInvalid, path)
	}
	operation := item.Post
	if strings.EqualFold(method, http.MethodPut) {
		operation = item.Put
	}
	if operation == nil {
		return nil, "", fmt.Errorf("%w: the document publishes no %s", ErrInvalid, route)
	}
	for _, parameter := range slices.Concat(item.Parameters, operation.Parameters) {
		if parameter.Ref != "" {
			return nil, "", fmt.Errorf("%w: %s carries a $ref parameter, which core does not follow and so cannot prove it carries no input", ErrInvalid, route)
		}
		// The locations are allow-listed, not the reverse. A location this did
		// not recognise would otherwise pass silently, and some of them name an
		// input: `body` and `formData` are how Swagger 2.0 spells a request
		// body, so denying only path and query would drop the operation's whole
		// input from the contract without saying so.
		if !slices.Contains(transportParameterLocations, parameter.In) {
			return nil, "", fmt.Errorf("%w: %s carries %q parameter %q, and a derived operation's input is the JSON request body alone", ErrInvalid, route, parameter.In, parameter.Name)
		}
	}
	return operation, route, nil
}

// payload projects one body and names the component it was described by. The
// schema has to be a component $ref: input_message and output_message record
// the owner's published message identity so the reuse is auditable, and an
// inline schema has no identity to record.
func (d *OpenAPIDocument) payload(body *OpenAPIBody, route, what string) (*basev0.RunnableSchema, string, error) {
	if body == nil {
		return nil, "", fmt.Errorf("%w: %s declares no %s, and a derived operation is one finite call with one input and one output", ErrInvalid, route, what)
	}
	if body.Ref != "" {
		return nil, "", fmt.Errorf("%w: %s %s is a $ref, which core does not follow outside a payload schema", ErrInvalid, route, what)
	}
	if len(body.Content) != 1 {
		return nil, "", fmt.Errorf("%w: %s %s is carried as %d media types, and a bounded payload is %s alone", ErrInvalid, route, what, len(body.Content), jsonMediaType)
	}
	// A media type's parameters do not change what a payload is encoded as, and
	// its type is case-insensitive, so the key is read as a media type rather
	// than compared as a string: "application/json; charset=utf-8" is JSON, and
	// refusing it would refuse a document that is spelling it legally.
	var media OpenAPIMediaType
	for spelling, carried := range body.Content {
		kind, _, parseErr := mime.ParseMediaType(spelling)
		if parseErr != nil || kind != jsonMediaType {
			return nil, "", fmt.Errorf("%w: %s %s is carried as %q, and the bounded profile describes no encoding but %s", ErrInvalid, route, what, spelling, jsonMediaType)
		}
		media = carried
	}
	if media.Schema == nil {
		return nil, "", fmt.Errorf("%w: %s %s declares no schema, and an undescribed payload is not a contract", ErrInvalid, route, what)
	}
	name, isComponent := strings.CutPrefix(media.Schema.Ref, componentSchemaPrefix)
	if !isComponent || !onlyRef(media.Schema) {
		return nil, "", fmt.Errorf("%w: %s %s schema is not a %s reference, and a payload described inline has no published identity to record", ErrInvalid, route, what, componentSchemaPrefix)
	}
	schema, err := ProjectJSONSchema(d, media.Schema)
	if err != nil {
		return nil, "", err
	}
	return schema, name, nil
}

// success finds the one response a derived operation completes with. Several
// would make the output shape depend on which status came back, which the
// single output schema of a contract cannot say.
func success(operation *OpenAPIOperation, route string) (*OpenAPIBody, error) {
	statuses := make([]string, 0, len(operation.Responses))
	for status := range operation.Responses {
		if len(status) == 3 && status[0] == '2' {
			statuses = append(statuses, status)
		}
	}
	if len(statuses) != 1 {
		slices.Sort(statuses)
		named := ""
		if len(statuses) > 0 {
			named = " (" + strings.Join(statuses, ", ") + ")"
		}
		return nil, fmt.Errorf("%w: %s declares %d successful responses%s, and a derived operation has exactly one output", ErrInvalid, route, len(statuses), named)
	}
	return operation.Responses[statuses[0]], nil
}

// PackageFromOpenAPIOperation derives the immutable package of the Runnable
// operation a REST route declares, and the execution policy and authority that
// are installed beside it rather than digested into it.
//
// It is PackageFromMethod's twin for a service described by OpenAPI rather than
// by a descriptor, and deliberately lands in the same place: one profile, one
// digest and one drift rule, so a FastAPI service and a gRPC one publish the
// same kind of operation rather than two kinds that resemble each other.
func PackageFromOpenAPIOperation(document []byte, location *resources.RunnableLocation, owner ServiceOwner, method, path string) (*basev0.RunnablePackage, *OperationSpec, error) {
	if location == nil || location.Identity == nil {
		return nil, nil, fmt.Errorf("%w: the release identity a derived package carries is required", ErrInvalid)
	}
	doc, err := ParseOpenAPIDocument(document)
	if err != nil {
		return nil, nil, err
	}
	operation, route, err := doc.operation(method, path)
	if err != nil {
		return nil, nil, err
	}
	spec, err := OperationFromOpenAPIMarker(operation.Marker, route)
	if err != nil {
		return nil, nil, err
	}
	input, inputMessage, err := doc.payload(operation.RequestBody, route, "request body")
	if err != nil {
		return nil, nil, err
	}
	response, err := success(operation, route)
	if err != nil {
		return nil, nil, err
	}
	output, outputMessage, err := doc.payload(response, route, "response")
	if err != nil {
		return nil, nil, err
	}
	identity, err := location.Identity.Proto()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: identity: %v", ErrInvalid, err)
	}
	pkg, err := PreparePackage(&basev0.RunnablePackage{
		Schema:   PackageSchemaV1,
		Identity: identity,
		Agent:    owner.Agent,
		Contract: &basev0.RunnableContract{Protocol: resources.RunnableServiceProtocolV1, Input: input, Output: output},
		Execution: &basev0.RunnableExecution{
			Facilities:     []*basev0.RunnableFacility{{Kind: basev0.RunnableFacility_SERVICE}},
			Timeout:        durationpb.New(spec.TotalTimeout),
			Cancellation:   basev0.RunnableExecution_CANCELLATION_NONE,
			Recovery:       basev0.RunnableExecution_RECOVERY_RECEIPT,
			MaxInputBytes:  resources.DefaultRunnablePayloadBytes,
			MaxOutputBytes: resources.DefaultRunnablePayloadBytes,
		},
		ServiceOperations: []*basev0.RunnableServiceOperation{{
			Module:        owner.Module,
			Name:          owner.Service,
			Endpoint:      owner.Endpoint,
			Operation:     spec.Method,
			InputMessage:  inputMessage,
			OutputMessage: outputMessage,
			Adaptation:    basev0.RunnableServiceOperation_ADAPTATION_BOUNDED_JSON_V1,
		}},
	})
	if err != nil {
		return nil, nil, err
	}
	return pkg, spec, nil
}
