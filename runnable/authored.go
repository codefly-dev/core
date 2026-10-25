package runnable

import (
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
)

// An authored runnable — a handler a person wrote, packaged with the harness
// its agent generates — is called the way a derived operation is: through a
// prepared binding (codefly.runnable-prepared/v3) whose call is a Connect JSON
// POST of the bounded input to ServedInvokeProcedure, with the Work Context on
// the call. What differs is where the policy comes from. A derived operation's
// policy is its method option; an authored runnable's is the `operation` block
// of its declaration, and the bounds its execution block already states are
// read from there rather than stated twice.

// GeneratedServiceEndpoint is the endpoint a generated harness serves on, as
// an Endpoint names it. A harness serves one operation on one listener, so the
// name is fixed rather than declared: there is no second endpoint to tell it
// from.
const GeneratedServiceEndpoint = "runnable"

// executionOwnedFields are the Operation fields an authored runnable's
// execution block owns. Each is derived from it, so a declaration stating one
// in its operation block would be a second, possibly different, statement of
// the same bound.
var executionOwnedFields = []string{"attempt_timeout", "total_timeout", "completion", "max_input_bytes", "max_output_bytes", "lookup_method"}

// AuthoredOperation reads an authored runnable's operation policy: the
// declared block decoded strictly as a codefly.runnable.v0.Operation, with
// every execution-owned field derived from the execution block. A runnable
// that declares no operation block is ErrNotAnOperation — an answer, not a
// failure: it is simply not offered as an operation.
//
// One attempt is the default, because a harness call is not retried unless
// its author says it may be; the attempt bound is the execution timeout and
// the total bound covers every attempt and the backoffs between them. A
// receipt-recovery runnable is looked up on its harness's ServedLookupProcedure.
// The spec may still carry required scope slots: ResolveScopeSlots answers
// them for one composition before a binding is prepared.
func AuthoredOperation(r *resources.Runnable) (*OperationSpec, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: runnable is required", ErrInvalid)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if r.Operation == nil {
		return nil, fmt.Errorf("%w: runnable %s declares no operation block", ErrNotAnOperation, r.Name)
	}
	for _, field := range executionOwnedFields {
		if _, stated := r.Operation[field]; stated {
			return nil, fmt.Errorf("%w: runnable %s operation states %s, which its execution block owns; remove it from the operation block", ErrInvalid, r.Name, field)
		}
	}
	document, err := json.Marshal(r.Operation)
	if err != nil {
		return nil, fmt.Errorf("%w: runnable %s operation is not a JSON-representable document: %v", ErrInvalid, r.Name, err)
	}
	declared, err := DecodeOperation(document)
	if err != nil {
		return nil, fmt.Errorf("runnable %s operation: %w", r.Name, err)
	}
	attempts := declared.GetMaxAttempts()
	if attempts == 0 {
		attempts = 1
	}
	backoff := MinBackoff
	if declared.GetBackoff() != nil {
		if err := declared.GetBackoff().CheckValid(); err != nil {
			return nil, fmt.Errorf("%w: runnable %s operation backoff is not a valid duration: %v", ErrInvalid, r.Name, err)
		}
		backoff = declared.GetBackoff().AsDuration()
	}
	attempt := r.Execution.GetTimeout()
	lookup := ""
	if r.Execution.Recovery == resources.RunnableRecoveryReceipt {
		lookup = ServedLookupProcedure
	}
	spec := &OperationSpec{
		Method:             ServedInvokeProcedure,
		AttemptTimeout:     attempt,
		TotalTimeout:       attempt*time.Duration(attempts) + backoff*time.Duration(attempts-1),
		MaxAttempts:        attempts,
		Backoff:            backoff,
		RetryableCodes:     declared.GetRetryableCodes(),
		Codes:              GRPCStatusNames,
		Audience:           declared.GetAudience(),
		InvokeScopes:       clonedScopes(declared.GetInvokeScopes()),
		LookupScopes:       clonedScopes(declared.GetLookupScopes()),
		LookupMethod:       lookup,
		MaxInputBytes:      r.Execution.MaxInputBytes(),
		MaxOutputBytes:     r.Execution.MaxOutputBytes(),
		Completion:         r.Execution.Proto().GetCompletion(),
		Tool:               proto.CloneOf(declared.GetTool()),
		RequiredScopeSlots: clonedSlots(declared.GetRequiredScopeSlots()),
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("runnable %s operation: %w", r.Name, err)
	}
	return spec, nil
}

// PrepareGeneratedService prepares the binding of an authored runnable whose
// generated harness serves at address, the HTTP base URL its placement
// resolved. policy is the concrete policy: AuthoredOperation's spec with its
// scope slots resolved. The operation is named by the runnable's module and
// name and the fixed GeneratedServiceEndpoint; the call is ServedInvokeProcedure
// in Connect's JSON mode, which is what the harness answers. The returned
// binding is verified and carries its contract digest, so EncodePrepared
// writes it as it is.
func PrepareGeneratedService(r *resources.Runnable, module, address string, policy *runnablev0.Operation) (*runnablev0.PreparedBinding, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: runnable is required", ErrInvalid)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if !r.Execution.Packaged() {
		return nil, fmt.Errorf("%w: runnable %s builds no harness; a service method is prepared from its owner's endpoint", ErrInvalid, r.Name)
	}
	contract := r.Contract.Proto()
	digest, err := ContractDigest(contract)
	if err != nil {
		return nil, err
	}
	binding := &runnablev0.PreparedBinding{
		Schema: PreparedSchemaV3,
		Operation: &runnablev0.PreparedOperation{
			Module:   module,
			Service:  r.Name,
			Endpoint: GeneratedServiceEndpoint,
			Spelling: ServedInvokeProcedure,
		},
		Call: &runnablev0.PreparedCall{
			Address: address,
			Route:   &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: ServedInvokeProcedure}},
		},
		Contract:       contract,
		ContractDigest: digest,
		Policy:         proto.CloneOf(policy),
	}
	if err := VerifyPrepared(binding); err != nil {
		return nil, fmt.Errorf("runnable %s: %w", r.Name, err)
	}
	return binding, nil
}

// FileFields lists a schema's FILE fields, top-level and nested, by their
// path in the payload ("pdf", "pages[].scan"). A caller staging documents
// for a call, or publishing the ones it returned, walks exactly these.
func FileFields(schema *basev0.RunnableSchema) []string {
	var paths []string
	var walk func(prefix string, field *basev0.RunnableField)
	walk = func(prefix string, field *basev0.RunnableField) {
		switch field.GetType() {
		case basev0.RunnableField_FILE:
			paths = append(paths, prefix)
		case basev0.RunnableField_OBJECT:
			for _, member := range field.GetFields() {
				walk(prefix+"."+member.GetName(), member)
			}
		case basev0.RunnableField_ARRAY:
			walk(prefix+"[]", field.GetItems())
		}
	}
	for _, field := range schema.GetFields() {
		walk(field.GetName(), field)
	}
	return paths
}
