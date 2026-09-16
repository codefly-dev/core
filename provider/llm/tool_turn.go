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

// AuthorizedToolResult keeps the caller's opaque authorization evidence
// separate from both the model proposal and the committed result.
type AuthorizedToolResult struct {
	Authorization string
	Result        ToolResult
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

func prepareToolDeclarations(declarations []ToolDeclaration) ([]preparedToolDeclaration, error) {
	prepared := make([]preparedToolDeclaration, 0, len(declarations))
	names := make(map[string]struct{}, len(declarations))
	for _, declaration := range declarations {
		if declaration.Name == "" {
			return nil, fmt.Errorf("tool name is required")
		}
		if _, exists := names[declaration.Name]; exists {
			return nil, fmt.Errorf("duplicate tool declaration %q", declaration.Name)
		}
		names[declaration.Name] = struct{}{}
		schema, err := compileStructuredSchema(declaration.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q input schema: %w", declaration.Name, err)
		}
		prepared = append(prepared, preparedToolDeclaration{
			Name:        declaration.Name,
			Description: declaration.Description,
			InputSchema: schema.value,
		})
	}
	return prepared, nil
}

func validateToolProposals(declarations []ToolDeclaration, proposals []ToolProposal) error {
	if len(proposals) == 0 {
		return fmt.Errorf("tool-call generation requires proposals")
	}
	tools := make(map[string]*compiledStructuredSchema, len(declarations))
	for _, declaration := range declarations {
		schema, err := compileStructuredSchema(declaration.InputSchema)
		if err != nil {
			return fmt.Errorf("tool %q input schema: %w", declaration.Name, err)
		}
		tools[declaration.Name] = schema
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
	generation, schema, _, err := prepareContinuationValue(request, true)
	if err != nil {
		return nil, err
	}
	result, err := executor.Continue(ctx, request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return canceledResult(request.Invocation, err)
		}
		return nil, err
	}
	if err := validateGenerationResult(generation, request.Invocation, schema, result); err != nil {
		return nil, err
	}
	return result, nil
}

func prepareGenerationLookup(lookup *GenerationLookup) (*GenerationRequest, Invocation, *compiledStructuredSchema, error) {
	if (lookup.Request == nil) == (lookup.Continuation == nil) {
		return nil, Invocation{}, nil, fmt.Errorf("generation lookup requires exactly one request kind")
	}
	if lookup.Request != nil {
		schema, err := prepareGenerationRequest(lookup.Request)
		return lookup.Request, lookup.Request.Invocation, schema, err
	}
	request, schema, _, err := prepareContinuationValue(lookup.Continuation, true)
	if err != nil {
		return nil, Invocation{}, nil, err
	}
	return request, lookup.Continuation.Invocation, schema, nil
}

type continuationIntent struct {
	Request      Invocation
	Previous     Invocation
	Receipt      string
	Continuation string
	ToolCalls    []canonicalToolProposal
	Results      []canonicalAuthorizedToolResult
}

type canonicalToolProposal struct {
	ID        string
	Name      string
	Arguments any
}

type canonicalAuthorizedToolResult struct {
	Authorization string
	CallID        string
	Outcome       ToolResultOutcome
	JSON          any
	Error         string
}

func prepareContinuationValue(request *ContinuationRequest, checkDigest bool) (*GenerationRequest, *compiledStructuredSchema, continuationIntent, error) {
	if request == nil || request.Request == nil || request.Previous == nil {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMissing, "request, original request, and previous result are required")
	}
	schema, err := prepareGenerationRequest(request.Request)
	if err != nil {
		return nil, nil, continuationIntent{}, err
	}
	if request.Previous.Continuation == "" {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMissing, "previous result has no continuation")
	}
	if err := validateGenerationResult(request.Request, request.Request.Invocation, schema, request.Previous); err != nil {
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
	canonicalResults := make([]canonicalAuthorizedToolResult, 0, len(request.Results))
	for _, authorized := range request.Results {
		result := authorized.Result
		if _, exists := seen[result.CallID]; exists {
			return nil, nil, continuationIntent{}, continuationError(ContinuationDuplicate, fmt.Sprintf("tool result %q", result.CallID))
		}
		seen[result.CallID] = struct{}{}
		if _, exists := proposals[result.CallID]; !exists {
			return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("tool result %q has no proposal", result.CallID))
		}
		if authorized.Authorization == "" {
			return nil, nil, continuationIntent{}, continuationError(ContinuationMismatched, fmt.Sprintf("tool result %q has no authorization", result.CallID))
		}
		canonical := canonicalAuthorizedToolResult{Authorization: authorized.Authorization, CallID: result.CallID, Outcome: result.Outcome, Error: result.Error}
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
	}
	if len(seen) != len(proposals) {
		return nil, nil, continuationIntent{}, continuationError(ContinuationMissing, "every proposal requires one tool result")
	}

	value := continuationIntent{
		Request:      request.Request.Invocation,
		Previous:     request.Previous.Invocation,
		Receipt:      request.Previous.Receipt,
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
	return request.Request, schema, value, nil
}

func continuationError(code ContinuationErrorCode, message string) error {
	return &ContinuationError{Code: code, Message: message}
}
