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
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
)

const (
	// componentSchemaPrefix and definitionSchemaPrefix are the only $refs the
	// projection follows, one per dialect: OpenAPI 3 keeps its schemas under
	// components and Swagger 2.0 under definitions. A $ref anywhere else in the
	// operation — its request body, a response, a parameter — is refused rather
	// than resolved: core reads the operation as the document writes it, and the
	// payload schemas are where the issue of one shape described once actually
	// arises.
	componentSchemaPrefix  = "#/components/schemas/"
	definitionSchemaPrefix = "#/definitions/"
	// jsonMediaType is the one media type a derived operation carries. The
	// bounded profile is a JSON profile, so a route offering another encoding
	// offers a payload the contract cannot describe.
	jsonMediaType = "application/json"
)

// openAPIOperationMethods are the HTTP methods a Runnable operation may be
// published under. POST and PUT carry the input as a JSON body and may apply
// any effect. GET is a read: it is admitted for an operation that recomputes,
// presents a read-only tool and is invoked holding nothing but read
// (readOnlyRoute), and its input is its query (queryPayload) — it is the safest
// operation to re-run, not one outside the profile. DELETE describes an effect
// with no body to name its input, and PATCH a diff against a state the contract
// does not carry.
var openAPIOperationMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut}

// transportParameterLocations are the parameter locations a derived operation
// may carry: the transport carries them and they are no part of the payload.
// Every other location names an input the bounded contract would have to
// describe, and is refused rather than ignored — except `query` on a GET,
// whose query is its whole input (queryPayload).
var transportParameterLocations = []string{"header", "cookie"}

// queryParameterLocation is where a GET's input travels.
const queryParameterLocation = "query"

// The document versions a Runnable operation is derived from: OpenAPI 3, and
// Swagger 2.0, the dialect protoc-gen-openapiv2, core's own OpenAPICombinator
// and solution-runtime-go's interface artifact write. Swagger spells the same
// operation in other places — a body is an `in: body` parameter, a response
// schema hangs off the response itself, media types are listed once in
// consumes and produces — and the reader moves each into the place OpenAPI 3
// spells it before anything else reads the document (fromSwagger), so one
// derivation reads both.
const (
	supportedOpenAPIMajor = "3"
	supportedSwagger      = "2.0"
)

// OpenAPIDocument is the part of an OpenAPI document a Runnable operation is
// derived from, read as JSON. It is deliberately not a model of OpenAPI:
// everything outside the operation's own payloads — servers, security, tags,
// examples — says how the route is served and never what the contract is.
type OpenAPIDocument struct {
	// OpenAPI and Swagger are the two spellings of a document's version. Both
	// are read so a Swagger 2.0 document is read as one rather than as an
	// OpenAPI 3 document that happens to declare nothing.
	OpenAPI    string                      `json:"openapi"`
	Swagger    string                      `json:"swagger"`
	Paths      map[string]*OpenAPIPathItem `json:"paths"`
	Components OpenAPIComponents           `json:"components"`
	// Definitions, Consumes and Produces are Swagger 2.0's: its schemas, and
	// the media types every operation carries unless it lists its own.
	Definitions map[string]*JSONSchema `json:"definitions"`
	Consumes    []string               `json:"consumes"`
	Produces    []string               `json:"produces"`
	// schemaPrefix is where this document's payload $refs point.
	schemaPrefix string
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
	Get        *OpenAPIOperation  `json:"get"`
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
	// OperationID, Summary and Description present the operation as a tool
	// when its marker names none.
	OperationID string `json:"operationId"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	// Consumes and Produces are Swagger 2.0's per-operation media types.
	Consumes []string `json:"consumes"`
	Produces []string `json:"produces"`
}

// OpenAPIParameter is an input carried outside the request body. OpenAPI 3
// describes its value with a schema; Swagger 2.0 with type and format beside
// the name, or — for `in: body` — with a schema that is the request body's.
type OpenAPIParameter struct {
	Ref      string      `json:"$ref"`
	Name     string      `json:"name"`
	In       string      `json:"in"`
	Required bool        `json:"required"`
	Schema   *JSONSchema `json:"schema"`
	Type     string      `json:"type"`
	Format   string      `json:"format"`
}

// OpenAPIBody is a request body or a response: the media types it is carried
// as, and the schema of each. Schema is Swagger 2.0's response schema, which
// the reader moves into Content.
type OpenAPIBody struct {
	Ref     string                      `json:"$ref"`
	Content map[string]OpenAPIMediaType `json:"content"`
	Schema  *JSONSchema                 `json:"schema"`
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
	// same operation in different places, so reading one as OpenAPI 3 would
	// find no request body and report that, pointing at the wrong part of a
	// document that plainly declares one. It is read as itself instead.
	if doc.Swagger != "" {
		if doc.Swagger != supportedSwagger || doc.OpenAPI != "" {
			return nil, fmt.Errorf("%w: document declares swagger %q; a Runnable operation is derived from swagger %s or OpenAPI %s", ErrInvalid, doc.Swagger, supportedSwagger, supportedOpenAPIMajor)
		}
		doc.fromSwagger()
		return doc, nil
	}
	if major, _, _ := strings.Cut(doc.OpenAPI, "."); major != supportedOpenAPIMajor {
		return nil, fmt.Errorf("%w: document declares openapi %q; a Runnable operation is derived from OpenAPI %s", ErrInvalid, doc.OpenAPI, supportedOpenAPIMajor)
	}
	doc.schemaPrefix = componentSchemaPrefix
	return doc, nil
}

// fromSwagger moves what Swagger 2.0 spells differently into the place OpenAPI
// 3 spells it, and nothing else: an `in: body` parameter becomes the request
// body, a response's schema its content, each carried as the media types the
// operation (or else the document) lists, and a typed parameter's type and
// format its schema. Every rule after this — one JSON media type, one 2xx, the
// parameter allow-list — then reads both dialects alike, so a Swagger body in
// two encodings or a formData parameter is refused by the same rule, in the
// same words, as its OpenAPI 3 spelling.
func (d *OpenAPIDocument) fromSwagger() {
	d.schemaPrefix = definitionSchemaPrefix
	d.Components.Schemas = d.Definitions
	carried := func(schema *JSONSchema, listed, document []string) map[string]OpenAPIMediaType {
		types := listed
		if len(types) == 0 {
			types = document
		}
		content := make(map[string]OpenAPIMediaType, len(types))
		for _, kind := range types {
			content[kind] = OpenAPIMediaType{Schema: schema}
		}
		return content
	}
	for _, item := range d.Paths {
		if item == nil {
			continue
		}
		item.Parameters = typedParameters(item.Parameters)
		for _, operation := range []*OpenAPIOperation{item.Get, item.Post, item.Put} {
			if operation == nil {
				continue
			}
			kept := make([]OpenAPIParameter, 0, len(operation.Parameters))
			for _, parameter := range operation.Parameters {
				if parameter.In == "body" && parameter.Ref == "" && operation.RequestBody == nil {
					operation.RequestBody = &OpenAPIBody{Content: carried(parameter.Schema, operation.Consumes, d.Consumes)}
					continue
				}
				kept = append(kept, parameter)
			}
			operation.Parameters = typedParameters(kept)
			for _, response := range operation.Responses {
				if response != nil && response.Schema != nil && response.Content == nil {
					response.Content = carried(response.Schema, operation.Produces, d.Produces)
				}
			}
		}
	}
}

// typedParameters gives each Swagger parameter that states its value by type
// the schema OpenAPI 3 would state it with.
func typedParameters(parameters []OpenAPIParameter) []OpenAPIParameter {
	for i := range parameters {
		if parameters[i].Schema == nil && parameters[i].Type != "" {
			parameters[i].Schema = &JSONSchema{Type: JSONSchemaType{parameters[i].Type}, Format: parameters[i].Format}
		}
	}
	return parameters
}

// componentSchema resolves a $ref against the document's own schemas.
func (d *OpenAPIDocument) componentSchema(ref string) (*JSONSchema, bool) {
	if d == nil {
		return nil, false
	}
	name, isComponent := strings.CutPrefix(ref, d.schemaPrefix)
	if !isComponent {
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
// why: v1 keeps an operation's input as one JSON object — a POST or PUT's
// body, a GET's query — and folding a path parameter or a query beside a body
// into it is a later profile decision rather than a coercion core may make now.
func (d *OpenAPIDocument) operation(method, path string) (*OpenAPIOperation, string, error) {
	route := Route(method, path)
	verb := strings.ToUpper(method)
	if !slices.Contains(openAPIOperationMethods, verb) {
		return nil, "", fmt.Errorf("%w: %s is not %s, and only those carry an input a declared policy can retry", ErrInvalid, route, joinedMethods())
	}
	// A template names an input the route carries in its path, which no
	// parameter list need declare for the document to look complete.
	if strings.ContainsAny(path, "{}") {
		return nil, "", fmt.Errorf("%w: %s is a path template, and a derived operation's route is one fixed path", ErrInvalid, route)
	}
	item, published := d.Paths[path]
	if !published || item == nil {
		return nil, "", fmt.Errorf("%w: the document publishes no path %s", ErrInvalid, path)
	}
	if item.Ref != "" {
		return nil, "", fmt.Errorf("%w: %s is a $ref to another path item, which core does not follow", ErrInvalid, path)
	}
	operation := map[string]*OpenAPIOperation{http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put}[verb]
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
		if verb == http.MethodGet && parameter.In == queryParameterLocation {
			continue
		}
		if !slices.Contains(transportParameterLocations, parameter.In) {
			return nil, "", fmt.Errorf("%w: %s carries %q parameter %q, and a derived operation's input is the JSON request body alone, or a GET's query", ErrInvalid, route, parameter.In, parameter.Name)
		}
	}
	return operation, route, nil
}

// joinedMethods spells the admitted methods for a refusal: "GET, POST or PUT".
func joinedMethods() string {
	last := len(openAPIOperationMethods) - 1
	return strings.Join(openAPIOperationMethods[:last], ", ") + " or " + openAPIOperationMethods[last]
}

// readOnlyRoute holds a GET to being a read in every half of its operation: it
// recomputes, so an uncertain outcome is asked again rather than resolved by a
// receipt; its tool says it only reads; and it is invoked holding nothing but
// the read action, so the capability a call presents cannot change what it
// reads whatever the route does. A mutation keeps its body, its receipt and its
// POST or PUT.
func readOnlyRoute(spec *OperationSpec, route string) error {
	invoked := &Authority{Requires: spec.InvokeScopes}
	if spec.EffectiveRecovery() != basev0.RunnableExecution_RECOVERY_RECOMPUTE ||
		spec.Tool.GetEffect() != runnablev0.ToolExposure_EFFECT_READ_ONLY || !invoked.ReadOnly() {
		return fmt.Errorf("%w: %s is a GET, which is admitted only as a read: recovery %s, a %s tool, and invoke scopes requiring only %q; a mutation is published as POST or PUT",
			ErrInvalid, route, basev0.RunnableExecution_RECOVERY_RECOMPUTE, runnablev0.ToolExposure_EFFECT_READ_ONLY, ReadOnlyScopeAction)
	}
	return nil
}

// queryPayload is a GET's input: one JSON object whose members are its query
// parameters, each a scalar a query string spells, optional exactly when the
// parameter is. A caller carries the object as the query — each member as one
// parameter of that name, a string as written, a number or a boolean as its
// JSON spelling — and a GET with no parameter takes the empty object. No
// component describes a query, so the identity input_message records is the
// one the document publishes it under: the operation's operationId.
func (d *OpenAPIDocument) queryPayload(operation *OpenAPIOperation, parameters []OpenAPIParameter, route string) (*basev0.RunnableSchema, string, error) {
	if operation.RequestBody != nil {
		return nil, "", fmt.Errorf("%w: %s declares a request body, and a GET's input is its query", ErrInvalid, route)
	}
	if operation.OperationID == "" {
		return nil, "", fmt.Errorf("%w: %s declares no operationId, which is the published identity a GET's query is recorded by", ErrInvalid, route)
	}
	properties := JSONSchemaProperties{}
	var required []string
	for _, parameter := range parameters {
		if parameter.In != queryParameterLocation {
			continue
		}
		if parameter.Schema == nil {
			return nil, "", fmt.Errorf("%w: %s query parameter %q declares no schema", ErrInvalid, route, parameter.Name)
		}
		if slices.ContainsFunc(properties, func(p JSONSchemaProperty) bool { return p.Name == parameter.Name }) {
			return nil, "", fmt.Errorf("%w: %s declares query parameter %q twice", ErrInvalid, route, parameter.Name)
		}
		properties = append(properties, JSONSchemaProperty{Name: parameter.Name, Schema: parameter.Schema})
		if parameter.Required {
			required = append(required, parameter.Name)
		}
	}
	input, err := ProjectJSONSchema(d, &JSONSchema{
		Type: JSONSchemaType{"object"}, Properties: &properties, Required: required, AdditionalProperties: json.RawMessage("false"),
	})
	if err != nil {
		return nil, "", err
	}
	for _, field := range input.GetFields() {
		switch field.GetType() {
		case basev0.RunnableField_STRING, basev0.RunnableField_INTEGER, basev0.RunnableField_NUMBER, basev0.RunnableField_BOOLEAN:
		default:
			return nil, "", fmt.Errorf("%w: %s query parameter %q is %s, and a query carries one scalar per name", ErrInvalid, route, field.GetName(), field.GetType())
		}
		if field.GetNullable() {
			return nil, "", fmt.Errorf("%w: %s query parameter %q is nullable, which a query string cannot spell", ErrInvalid, route, field.GetName())
		}
	}
	return input, operation.OperationID, nil
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
	name, isComponent := strings.CutPrefix(media.Schema.Ref, d.schemaPrefix)
	if !isComponent || !onlyRef(media.Schema) {
		return nil, "", fmt.Errorf("%w: %s %s schema is not a %s reference, and a payload described inline has no published identity to record", ErrInvalid, route, what, d.schemaPrefix)
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
// method is GET, POST or PUT; a GET is admitted as a read only, its query its
// input (readOnlyRoute, queryPayload). document is OpenAPI 3 or Swagger 2.0.
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
	if spec.Tool == nil {
		spec.Tool = restTool(operation, route, spec.EffectiveRecovery())
		if err = spec.Validate(); err != nil {
			return nil, nil, err
		}
	}
	var input *basev0.RunnableSchema
	var inputMessage string
	if strings.EqualFold(method, http.MethodGet) {
		if err = readOnlyRoute(spec, route); err != nil {
			return nil, nil, err
		}
		input, inputMessage, err = doc.queryPayload(operation, slices.Concat(doc.Paths[path].Parameters, operation.Parameters), route)
	} else {
		input, inputMessage, err = doc.payload(operation.RequestBody, route, "request body")
	}
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
			Recovery:       spec.EffectiveRecovery(),
			Completion:     spec.Completion,
			MaxInputBytes:  payloadBound(spec.MaxInputBytes),
			MaxOutputBytes: payloadBound(spec.MaxOutputBytes),
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

// restTool presents a REST operation as a tool from what its document says:
// its operationId (or its route), its description (or summary), and its
// effect, read-only exactly when it recomputes.
func restTool(operation *OpenAPIOperation, route string, recovery basev0.RunnableExecution_Recovery) *runnablev0.ToolExposure {
	name := operation.OperationID
	if name == "" {
		name = route
	}
	description := toolDescription(operation.Description)
	if description == "" {
		description = toolDescription(operation.Summary)
	}
	if description == "" {
		description = "Calls " + route + "."
	}
	effect := runnablev0.ToolExposure_EFFECT_MUTATION
	if recovery == basev0.RunnableExecution_RECOVERY_RECOMPUTE {
		effect = runnablev0.ToolExposure_EFFECT_READ_ONLY
	}
	return &runnablev0.ToolExposure{Name: toolName(name), Description: description, Effect: effect}
}

// DefaultReadRetryableStatuses are the outcomes a derived REST read is retried
// on: DefaultReadRetryableCodes, spelled as the HTTP status it is.
var DefaultReadRetryableStatuses = []string{"503"}

// DefaultRouteOperation is DefaultOperation for a REST route: the operation a
// route declaring an authority policy derives with nothing added, which its
// owner publishes as the route's x-codefly-operation marker so the marker
// states core's defaults rather than the owner's guess at them. name and
// description present it as a tool — the route's operationId and its one-line
// summary — and audience is the trust boundary the owner's hop admits.
//
// A read recomputes and may be served over GET, POST or PUT; anything else is
// a mutation resolved by its receipt and may not be a GET. A policy requiring
// nothing, a mutation over GET, or a path template (a relayed subtree, whose
// input is partly its path) is ErrNotDerivable: the route stays a call its own
// clients make.
func DefaultRouteOperation(method, path string, authority *Authority, audience, name, description string) (*OperationSpec, error) {
	route := Route(method, path)
	if authority == nil || len(authority.Requires) == 0 {
		return nil, fmt.Errorf("%w: %s requires no authority, so there is nothing to mint the call's capability from", ErrNotDerivable, route)
	}
	if strings.ContainsAny(path, "{}") {
		return nil, fmt.Errorf("%w: %s is a path template, and an operation's route is one fixed path", ErrNotDerivable, route)
	}
	if strings.EqualFold(method, http.MethodGet) && !authority.ReadOnly() {
		return nil, fmt.Errorf("%w: %s requires more than %q, and a GET is derived only as a read", ErrNotDerivable, route, ReadOnlyScopeAction)
	}
	spec := &OperationSpec{
		Method:         route,
		AttemptTimeout: DefaultAttemptTimeout,
		TotalTimeout:   DefaultTotalTimeout,
		Backoff:        DefaultBackoff,
		Codes:          HTTPStatusCodes,
		Audience:       audience,
		Completion:     basev0.RunnableExecution_COMPLETION_CALL,
		InvokeScopes:   clonedScopes(authority.Requires),
	}
	effect := runnablev0.ToolExposure_EFFECT_MUTATION
	if authority.ReadOnly() {
		effect = runnablev0.ToolExposure_EFFECT_READ_ONLY
		spec.Recovery = basev0.RunnableExecution_RECOVERY_RECOMPUTE
		spec.MaxAttempts = DefaultReadAttempts
		spec.RetryableCodes = slices.Clone(DefaultReadRetryableStatuses)
	} else {
		spec.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
		spec.MaxAttempts = DefaultMutationAttempts
	}
	if name == "" {
		name = route
	}
	text := toolDescription(description)
	if text == "" {
		text = "Calls " + route + "."
	}
	spec.Tool = &runnablev0.ToolExposure{Name: toolName(name), Description: text, Effect: effect}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}
