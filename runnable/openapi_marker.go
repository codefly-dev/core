package runnable

import (
	"encoding/json"
	"fmt"
	"slices"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// OpenAPIOperationMarker is the vendor extension an OpenAPI operation object
// carries to declare itself a Runnable operation. Its value is the proto3 JSON
// form of codefly.runnable.v0.Operation — the same field names, durations as
// strings such as "30s", scopes as WorkScopeV1 — so the marker and the method
// option are one schema read two ways rather than two schemas that drift.
const OpenAPIOperationMarker = "x-codefly-operation"

// OperationFromOpenAPIMarker reads and validates the marker an OpenAPI
// operation object carries. An operation without the marker is
// ErrNotAnOperation: a generator walks every route of a document and derives
// the marked ones. operation names the route as the owner spells it,
// "POST /path", and is what the resulting policy is reported against.
func OperationFromOpenAPIMarker(marker json.RawMessage, operation string) (*OperationSpec, error) {
	if len(marker) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotAnOperation, operation)
	}
	declared := &runnablev0.Operation{}
	// protojson rejects a field the message does not declare, so a misspelled
	// policy field is an error rather than a policy silently left at zero.
	if err := protojson.Unmarshal(marker, declared); err != nil {
		return nil, fmt.Errorf("%w: %s %s is not a codefly.runnable.v0.Operation: %v", ErrInvalid, operation, OpenAPIOperationMarker, err)
	}
	// The marker's presence is the marking, but every policy field is required:
	// an attempt budget and an authority nobody chose are not defaults core may
	// invent on an owner's behalf.
	if proto.Equal(declared, &runnablev0.Operation{}) {
		return nil, fmt.Errorf("%w: %s carries %s but declares no execution policy; the marker marks the operation and its fields state how it runs", ErrInvalid, operation, OpenAPIOperationMarker)
	}
	// A receipt lookup paired on the same service is a gRPC spelling: it names
	// a method, and validating one here would mean accepting a route nothing
	// checks. A REST operation's receipts are read through the route the SDK
	// publishes, which is why the generic lookup is the whole answer for it.
	if declared.GetLookupMethod() != "" {
		return nil, fmt.Errorf("%w: %s declares lookup_method, which a REST operation has no spelling for; its receipts are read through the generic receipts route", ErrInvalid, operation)
	}
	spec := &OperationSpec{
		Method:         operation,
		AttemptTimeout: declared.GetAttemptTimeout().AsDuration(),
		TotalTimeout:   declared.GetTotalTimeout().AsDuration(),
		MaxAttempts:    declared.GetMaxAttempts(),
		Backoff:        declared.GetBackoff().AsDuration(),
		RetryableCodes: slices.Clone(declared.GetRetryableCodes()),
		// A REST operation names the outcomes it retries by HTTP status, and
		// the validator holds it to that vocabulary.
		Codes:        HTTPStatusCodes,
		Audience:     declared.GetAudience(),
		InvokeScopes: clonedScopes(declared.GetInvokeScopes()),
		LookupScopes: clonedScopes(declared.GetLookupScopes()),
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}
