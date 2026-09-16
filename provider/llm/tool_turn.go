package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ToolDeclaration describes a caller-owned tool and the JSON accepted by it.
type ToolDeclaration struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolProposal is model output. It is not authorization to execute the tool.
type ToolProposal struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolResultOutcome string

const (
	ToolResultSucceeded ToolResultOutcome = "SUCCEEDED"
	ToolResultFailed    ToolResultOutcome = "FAILED"
)

// ToolResult is the committed outcome supplied by the caller after execution.
type ToolResult struct {
	CallID  string
	Outcome ToolResultOutcome
	JSON    json.RawMessage
	Error   string
}

// AuthorizedToolResult records the caller's non-secret authorization decision
// separately from both the model proposal and the committed result. Core
// validates the reference but does not send it to the model executor.
type AuthorizedToolResult struct {
	AuthorizationReference string
	Result                 ToolResult
}

// ContinuationRequest resumes one completed model turn with caller-owned tool
// results. Invocation identifies this new model call; Previous.Invocation
// identifies the call that made the proposals.
type ContinuationRequest struct {
	Request    *GenerationRequest
	Previous   *GenerationResult
	Results    []AuthorizedToolResult
	Invocation Invocation
}

// ToolContinuationRequest is the model-facing continuation after Core has
// validated caller authorization and removed that policy metadata.
type ToolContinuationRequest struct {
	Request      *GenerationRequest
	Previous     Invocation
	Continuation string
	ToolCalls    []ToolProposal
	Results      []ToolResult
	Invocation   Invocation
}

type ContinuationErrorCode string

const (
	ContinuationMissing     ContinuationErrorCode = "MISSING"
	ContinuationDuplicate   ContinuationErrorCode = "DUPLICATE"
	ContinuationMismatched  ContinuationErrorCode = "MISMATCHED"
	ContinuationExpired     ContinuationErrorCode = "EXPIRED"
	ContinuationUnsupported ContinuationErrorCode = "UNSUPPORTED"
)

// ContinuationError is returned for contract-level continuation failures.
// Executors use ContinuationExpired when their opaque reference is no longer
// recoverable.
type ContinuationError struct {
	Code    ContinuationErrorCode
	Message string
}

func (e *ContinuationError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("continuation %s", e.Code)
	}
	return fmt.Sprintf("continuation %s: %s", e.Code, e.Message)
}

type preparedToolDeclaration struct {
	Name        string
	Description string
	InputSchema any
}

func prepareToolDeclarations(declarations []ToolDeclaration) ([]preparedToolDeclaration, map[string]*compiledStructuredSchema, error) {
	prepared := make([]preparedToolDeclaration, 0, len(declarations))
	schemas := make(map[string]*compiledStructuredSchema, len(declarations))
	names := make(map[string]struct{}, len(declarations))
	for _, declaration := range declarations {
		if declaration.Name == "" {
			return nil, nil, fmt.Errorf("tool name is required")
		}
		if _, exists := names[declaration.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate tool declaration %q", declaration.Name)
		}
		names[declaration.Name] = struct{}{}
		schema, err := compileStructuredSchema(declaration.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("tool %q input schema: %w", declaration.Name, err)
		}
		schemas[declaration.Name] = schema
		prepared = append(prepared, preparedToolDeclaration{
			Name:        declaration.Name,
			Description: declaration.Description,
			InputSchema: schema.value,
		})
	}
	return prepared, schemas, nil
}

func validateToolProposals(tools map[string]*compiledStructuredSchema, proposals []ToolProposal) error {
	if len(proposals) == 0 {
		return fmt.Errorf("tool-call generation requires proposals")
	}
	ids := make(map[string]struct{}, len(proposals))
	for _, proposal := range proposals {
		if proposal.ID == "" {
			return fmt.Errorf("tool proposal id is required")
		}
		if _, exists := ids[proposal.ID]; exists {
			return fmt.Errorf("duplicate tool proposal id %q", proposal.ID)
		}
		ids[proposal.ID] = struct{}{}
		schema, ok := tools[proposal.Name]
		if !ok {
			return fmt.Errorf("tool proposal %q names undeclared tool %q", proposal.ID, proposal.Name)
		}
		if err := validateStructuredJSON(schema, proposal.Arguments); err != nil {
			return fmt.Errorf("tool proposal %q arguments: %w", proposal.ID, err)
		}
	}
	return nil
}

// BindContinuation derives and installs the continuation call's intent digest.
func BindContinuation(request *ContinuationRequest, id string) error {
	if request == nil {
		return continuationError(ContinuationMissing, "request is required")
	}
	request.Invocation.ID = id
	digest, err := ContinuationIntentDigest(request)
	if err != nil {
		return err
	}
	request.Invocation.IntentDigest = digest
	return nil
}

// ContinuationIntentDigest binds the prior proposals, opaque continuation, and
// authorized results so changed tool outcomes cannot reuse an invocation.
func ContinuationIntentDigest(request *ContinuationRequest) (string, error) {
	_, _, value, err := prepareContinuationValue(request, false)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal continuation intent: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c *StructuredClient) Continue(ctx context.Context, request *ContinuationRequest) (*GenerationResult, error) {
	executor, ok := c.exec.(ToolGenerationExecutor)
	if !ok {
		return nil, continuationError(ContinuationUnsupported, "executor does not support tool turns")
	}
	prepared, executorRequest, _, err := prepareContinuationValue(request, true)
	if err != nil {
		return nil, err
	}
	invocation := executorRequest.Invocation
	result, err := executor.Continue(ctx, executorRequest)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return canceledResult(invocation, err)
		}
		return nil, err
	}
	if err := validateGenerationResult(prepared, invocation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func prepareGenerationLookup(lookup *GenerationLookup) (*preparedGenerationRequest, Invocation, *GenerationLookup, error) {
	if (lookup.Request == nil) == (lookup.Continuation == nil) {
		return nil, Invocation{}, nil, fmt.Errorf("generation lookup requires exactly one request kind")
	}
	if lookup.Request != nil {
		prepared, err := prepareGenerationRequest(lookup.Request)
		if err != nil {
			return nil, Invocation{}, nil, err
		}
		return prepared, prepared.invocation, &GenerationLookup{Request: cloneGenerationRequest(lookup.Request), Receipt: lookup.Receipt}, nil
	}
	prepared, executorRequest, _, err := prepareContinuationValue(lookup.Continuation, true)
	if err != nil {
		return nil, Invocation{}, nil, err
	}
	return prepared, executorRequest.Invocation, &GenerationLookup{Continuation: cloneContinuationRequest(lookup.Continuation), Receipt: lookup.Receipt}, nil
}

type continuationIntent struct {
	Request      Invocation
	Previous     Invocation
	Continuation string
	ToolCalls    []canonicalToolProposal
	Results      []canonicalToolResult
}

type canonicalToolProposal struct {
	ID        string
	Name      string
	Arguments any
}

type canonicalToolResult struct {
	CallID  string
	Outcome ToolResultOutcome
	JSON    any
	Error   string
}

func prepareContinuationValue(request *ContinuationRequest, checkDigest bool) (*preparedGenerationRequest, *ToolContinuationRequest, continuationIntent, error) {
	if request == nil || request.Request == nil || request.Previous == nil {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMissing, "request, original request, and previous result are required")
	}
	prepared, err := prepareGenerationRequest(request.Request)
	if err != nil {
		return nil, nil, continuationIntent{}, err
	}
	if request.Previous.Continuation == "" {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMissing, "previous result has no continuation")
	}
	if err := validateGenerationResult(prepared, prepared.invocation, request.Previous); err != nil {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, err.Error())
	}
	if request.Previous.Outcome != GenerationToolCalls {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, "previous result is not a tool-call proposal")
	}
	if request.Invocation.ID == "" {
		return nil, nil, continuationIntent{}, fmt.Errorf("continuation invocation id is required")
	}
	if request.Invocation.ID == request.Previous.Invocation.ID {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, "continuation must use a new invocation id")
	}

	proposals := make(map[string]ToolProposal, len(request.Previous.ToolCalls))
	canonicalCalls := make([]canonicalToolProposal, 0, len(request.Previous.ToolCalls))
	for _, proposal := range request.Previous.ToolCalls {
		arguments, err := decodeStrictJSON(proposal.Arguments)
		if err != nil {
			return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, err.Error())
		}
		proposals[proposal.ID] = proposal
		canonicalCalls = append(canonicalCalls, canonicalToolProposal{ID: proposal.ID, Name: proposal.Name, Arguments: arguments})
	}

	seen := make(map[string]struct{}, len(request.Results))
	canonicalResults := make([]canonicalToolResult, 0, len(request.Results))
	executorResults := make([]ToolResult, 0, len(request.Results))
	for _, authorized := range request.Results {
		result := authorized.Result
		if _, exists := seen[result.CallID]; exists {
			return nil, nil, continuationIntent{}, continuationError(ContinuationDuplicate, fmt.Sprintf("tool result %q", result.CallID))
		}
		seen[result.CallID] = struct{}{}
		if _, exists := proposals[result.CallID]; !exists {
			return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("tool result %q has no proposal", result.CallID))
		}
		if authorized.AuthorizationReference == "" || len(authorized.AuthorizationReference) > 256 {
			return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("tool result %q has invalid authorization reference", result.CallID))
		}
		canonical := canonicalToolResult{CallID: result.CallID, Outcome: result.Outcome, Error: result.Error}
		switch result.Outcome {
		case ToolResultSucceeded:
			if result.Error != "" || len(result.JSON) == 0 {
				return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("successful tool result %q has invalid content", result.CallID))
			}
			canonical.JSON, err = decodeStrictJSON(result.JSON)
			if err != nil {
				return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("tool result %q is not valid JSON: %v", result.CallID, err))
			}
		case ToolResultFailed:
			if result.Error == "" || len(result.JSON) != 0 {
				return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("failed tool result %q has invalid content", result.CallID))
			}
		default:
			return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("tool result %q has unknown outcome", result.CallID))
		}
		canonicalResults = append(canonicalResults, canonical)
		executorResults = append(executorResults, cloneToolResult(result))
	}
	if len(seen) != len(proposals) {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMissing, "every proposal requires one tool result")
	}

	value := continuationIntent{
		Request:      request.Request.Invocation,
		Previous:     request.Previous.Invocation,
		Continuation: request.Previous.Continuation,
		ToolCalls:    canonicalCalls,
		Results:      canonicalResults,
	}
	if checkDigest {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, nil, continuationIntent{}, fmt.Errorf("marshal continuation intent: %w", err)
		}
		sum := sha256.Sum256(encoded)
		if request.Invocation.IntentDigest != "sha256:"+hex.EncodeToString(sum[:]) {
			return nil, nil, continuationIntent{}, fmt.Errorf("continuation invocation intent digest does not match request")
		}
	}
	executorRequest := &ToolContinuationRequest{
		Request:      cloneGenerationRequest(request.Request),
		Previous:     request.Previous.Invocation,
		Continuation: request.Previous.Continuation,
		ToolCalls:    cloneToolProposals(request.Previous.ToolCalls),
		Results:      executorResults,
		Invocation:   request.Invocation,
	}
	return prepared, executorRequest, value, nil
}

func cloneGenerationRequest(request *GenerationRequest) *GenerationRequest {
	if request == nil {
		return nil
	}
	cloned := *request
	cloned.Messages = append([]Message(nil), request.Messages...)
	cloned.Schema = append(json.RawMessage(nil), request.Schema...)
	cloned.Tools = make([]ToolDeclaration, len(request.Tools))
	for i, tool := range request.Tools {
		cloned.Tools[i] = tool
		cloned.Tools[i].InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
	}
	return &cloned
}

func cloneContinuationRequest(request *ContinuationRequest) *ContinuationRequest {
	if request == nil {
		return nil
	}
	cloned := *request
	cloned.Request = cloneGenerationRequest(request.Request)
	cloned.Previous = cloneGenerationResult(request.Previous)
	cloned.Results = make([]AuthorizedToolResult, len(request.Results))
	for i, authorized := range request.Results {
		cloned.Results[i] = authorized
		cloned.Results[i].Result = cloneToolResult(authorized.Result)
		cloned.Results[i].AuthorizationReference = ""
	}
	return &cloned
}

func cloneGenerationResult(result *GenerationResult) *GenerationResult {
	if result == nil {
		return nil
	}
	cloned := *result
	cloned.JSON = append(json.RawMessage(nil), result.JSON...)
	cloned.ToolCalls = cloneToolProposals(result.ToolCalls)
	if result.Usage != nil {
		usage := *result.Usage
		usage.InputTokens = cloneInt64(result.Usage.InputTokens)
		usage.OutputTokens = cloneInt64(result.Usage.OutputTokens)
		usage.TotalTokens = cloneInt64(result.Usage.TotalTokens)
		cloned.Usage = &usage
	}
	return &cloned
}

func cloneToolProposals(proposals []ToolProposal) []ToolProposal {
	cloned := make([]ToolProposal, len(proposals))
	for i, proposal := range proposals {
		cloned[i] = proposal
		cloned[i].Arguments = append(json.RawMessage(nil), proposal.Arguments...)
	}
	return cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneToolResult(result ToolResult) ToolResult {
	result.JSON = append(json.RawMessage(nil), result.JSON...)
	return result
}

func continuationError(code ContinuationErrorCode, message string) error {
	return &ContinuationError{Code: code, Message: message}
}
