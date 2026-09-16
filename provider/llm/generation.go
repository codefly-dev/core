package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrInvocationConflict reports an invocation ID already bound to a different
// intent digest.
var ErrInvocationConflict = errors.New("invocation id is already bound to different intent")

// GenerationExecutor durably binds each invocation ID to its intent before
// dispatch. Repeating the same ID and digest recovers the existing invocation;
// repeating an ID with another digest returns ErrInvocationConflict. Lookup
// observes existing state and never dispatches generation.
type GenerationExecutor interface {
	Generate(context.Context, *GenerationRequest) (*GenerationResult, error)
	Lookup(context.Context, *GenerationLookup) (*GenerationResult, error)
}

// ToolGenerationExecutor extends structured generation with caller-driven
// continuations. Continue verifies that the opaque continuation belongs to
// Previous, then atomically consumes it for one invocation; another invocation
// using it returns ContinuationDuplicate. The executor proposes tools but never
// runs them.
type ToolGenerationExecutor interface {
	GenerationExecutor
	Continue(context.Context, *ToolContinuationRequest) (*GenerationResult, error)
}

type StructuredClient struct{ exec GenerationExecutor }

func NewStructuredClient(exec GenerationExecutor) *StructuredClient {
	return &StructuredClient{exec: exec}
}

type GenerationRequest struct {
	Model       ModelIdentity
	Messages    []Message
	System      string
	MaxTokens   int64
	Temperature string
	Schema      json.RawMessage
	Tools       []ToolDeclaration
	Invocation  Invocation
}

type ModelIdentity struct {
	Model   string
	Profile string
}

// Invocation is a stable caller identifier bound by Core to the exact request intent.
type Invocation struct {
	ID           string
	IntentDigest string
}

// GenerationLookup requires the original generation or continuation request so
// Core can verify its intent without issuing another model call.
type GenerationLookup struct {
	Request      *GenerationRequest
	Continuation *ContinuationRequest
	Receipt      string
}

type GenerationOutcome string

const (
	GenerationCompleted  GenerationOutcome = "COMPLETED"
	GenerationToolCalls  GenerationOutcome = "TOOL_CALLS"
	GenerationRefused    GenerationOutcome = "REFUSED"
	GenerationIncomplete GenerationOutcome = "INCOMPLETE"
	GenerationCanceled   GenerationOutcome = "CANCELED"
	GenerationUncertain  GenerationOutcome = "UNCERTAIN"
)

// GenerationDelivery records dispatch settlement independently from model content.
type GenerationDelivery string

const (
	GenerationNotSent            GenerationDelivery = "NOT_SENT"
	GenerationResponseReceived   GenerationDelivery = "RESPONSE_RECEIVED"
	GenerationSentOutcomeUnknown GenerationDelivery = "SENT_OUTCOME_UNKNOWN"
)

// GenerationUsage uses nil for unknown fields; zero is an explicit count.
type GenerationUsage struct {
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
}

// GenerationResult preserves partial output separately from valid completed JSON.
type GenerationResult struct {
	Invocation   Invocation
	Receipt      string
	Outcome      GenerationOutcome
	Delivery     GenerationDelivery
	JSON         json.RawMessage
	PartialText  string
	Refusal      string
	ToolCalls    []ToolProposal
	Continuation string
	Usage        *GenerationUsage
}

// BindInvocation derives and installs the request intent digest.
func BindInvocation(request *GenerationRequest, id string) error {
	if request == nil {
		return fmt.Errorf("generation request is required")
	}
	request.Invocation.ID = id
	digest, err := GenerationIntentDigest(request)
	if err != nil {
		return err
	}
	request.Invocation.IntentDigest = digest
	return nil
}

// GenerationIntentDigest derives a stable digest from every model-request field.
func GenerationIntentDigest(request *GenerationRequest) (string, error) {
	if request == nil {
		return "", fmt.Errorf("generation request is required")
	}
	compiled, err := compileStructuredSchema(request.Schema)
	if err != nil {
		return "", err
	}
	tools, _, err := prepareToolDeclarations(request.Tools)
	if err != nil {
		return "", err
	}
	return generationIntentDigest(request, compiled.value, tools)
}

func (c *StructuredClient) Generate(ctx context.Context, request *GenerationRequest) (*GenerationResult, error) {
	prepared, err := prepareGenerationRequest(request)
	if err != nil {
		return nil, err
	}
	result, err := c.exec.Generate(ctx, cloneGenerationRequest(request))
	if err != nil {
		return canceledResult(prepared.invocation, err)
	}
	if err := validateGenerationResult(prepared, prepared.invocation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *StructuredClient) Lookup(ctx context.Context, lookup *GenerationLookup) (*GenerationResult, error) {
	if lookup == nil {
		return nil, fmt.Errorf("generation lookup is required")
	}
	prepared, invocation, executorLookup, err := prepareGenerationLookup(lookup)
	if err != nil {
		return nil, err
	}
	result, err := c.exec.Lookup(ctx, executorLookup)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationResult(prepared, invocation, result); err != nil {
		return nil, err
	}
	return result, nil
}

func canceledResult(invocation Invocation, err error) (*GenerationResult, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &GenerationResult{Invocation: invocation, Outcome: GenerationCanceled, Delivery: GenerationSentOutcomeUnknown}, nil
	}
	return nil, err
}

type compiledStructuredSchema struct {
	value  any
	schema *jsonschema.Schema
}

type preparedGenerationRequest struct {
	schema     *compiledStructuredSchema
	tools      map[string]*compiledStructuredSchema
	invocation Invocation
}

func prepareGenerationRequest(request *GenerationRequest) (*preparedGenerationRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("generation request is required")
	}
	if request.Model.Model == "" {
		return nil, fmt.Errorf("generation model is required")
	}
	if request.Invocation.ID == "" {
		return nil, fmt.Errorf("invocation id is required")
	}
	compiled, err := compileStructuredSchema(request.Schema)
	if err != nil {
		return nil, err
	}
	tools, toolSchemas, err := prepareToolDeclarations(request.Tools)
	if err != nil {
		return nil, err
	}
	digest, err := generationIntentDigest(request, compiled.value, tools)
	if err != nil {
		return nil, err
	}
	if request.Invocation.IntentDigest != digest {
		return nil, fmt.Errorf("invocation intent digest does not match request")
	}
	return &preparedGenerationRequest{schema: compiled, tools: toolSchemas, invocation: request.Invocation}, nil
}

func generationIntentDigest(request *GenerationRequest, schema any, tools []preparedToolDeclaration) (string, error) {
	intent := struct {
		Model       ModelIdentity
		Messages    []Message
		System      string
		MaxTokens   int64
		Temperature string
		Schema      any
		Tools       []preparedToolDeclaration `json:",omitempty"`
	}{request.Model, request.Messages, request.System, request.MaxTokens, request.Temperature, schema, tools}
	encoded, err := json.Marshal(intent)
	if err != nil {
		return "", fmt.Errorf("marshal generation intent: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateGenerationResult(prepared *preparedGenerationRequest, invocation Invocation, result *GenerationResult) error {
	if result == nil {
		return fmt.Errorf("generation result is required")
	}
	if result.Invocation != invocation {
		return fmt.Errorf("generation result invocation does not match request")
	}
	if err := validateGenerationUsage(result.Usage); err != nil {
		return err
	}
	switch result.Outcome {
	case GenerationCompleted:
		if result.Delivery != GenerationResponseReceived || result.Refusal != "" || result.PartialText != "" || len(result.ToolCalls) != 0 || result.Continuation != "" {
			return fmt.Errorf("completed generation has invalid settlement or content")
		}
		if len(result.JSON) == 0 {
			return fmt.Errorf("completed generation requires structured JSON")
		}
		return validateStructuredJSON(prepared.schema, result.JSON)
	case GenerationToolCalls:
		if result.Delivery != GenerationResponseReceived || len(result.JSON) != 0 || result.Refusal != "" || result.PartialText != "" || result.Continuation == "" {
			return fmt.Errorf("tool-call generation has invalid settlement or content")
		}
		return validateToolProposals(prepared.tools, result.ToolCalls)
	case GenerationRefused:
		if result.Delivery != GenerationResponseReceived || result.Refusal == "" || len(result.JSON) != 0 || result.PartialText != "" || len(result.ToolCalls) != 0 || result.Continuation != "" {
			return fmt.Errorf("refused generation has invalid settlement or content")
		}
	case GenerationIncomplete:
		if result.Delivery != GenerationResponseReceived || len(result.JSON) != 0 || result.Refusal != "" || len(result.ToolCalls) != 0 || result.Continuation != "" {
			return fmt.Errorf("incomplete generation has invalid settlement or content")
		}
	case GenerationCanceled:
		if result.Delivery != GenerationNotSent && result.Delivery != GenerationSentOutcomeUnknown {
			return fmt.Errorf("canceled generation has invalid delivery")
		}
		if len(result.JSON) != 0 || result.PartialText != "" || result.Refusal != "" || len(result.ToolCalls) != 0 || result.Continuation != "" {
			return fmt.Errorf("canceled generation cannot include model output")
		}
	case GenerationUncertain:
		if result.Delivery != GenerationSentOutcomeUnknown || len(result.JSON) != 0 || result.PartialText != "" || result.Refusal != "" || len(result.ToolCalls) != 0 || result.Continuation != "" {
			return fmt.Errorf("uncertain generation has invalid settlement or content")
		}
	default:
		return fmt.Errorf("unknown generation outcome %q", result.Outcome)
	}
	return nil
}

func validateGenerationUsage(usage *GenerationUsage) error {
	if usage == nil {
		return nil
	}
	if usage.InputTokens == nil && usage.OutputTokens == nil && usage.TotalTokens == nil {
		return fmt.Errorf("generation usage without counts must be nil")
	}
	for _, count := range []*int64{usage.InputTokens, usage.OutputTokens, usage.TotalTokens} {
		if count != nil && *count < 0 {
			return fmt.Errorf("generation usage cannot be negative")
		}
	}
	if usage.InputTokens != nil && usage.OutputTokens != nil && usage.TotalTokens != nil && *usage.TotalTokens != *usage.InputTokens+*usage.OutputTokens {
		return fmt.Errorf("generation usage total does not match input and output")
	}
	return nil
}

func ValidateStructuredSchema(schema json.RawMessage) error {
	_, err := compileStructuredSchema(schema)
	return err
}

func ValidateStructuredJSON(schema, content json.RawMessage) error {
	compiled, err := compileStructuredSchema(schema)
	if err != nil {
		return err
	}
	return validateStructuredJSON(compiled, content)
}

func compileStructuredSchema(schema json.RawMessage) (*compiledStructuredSchema, error) {
	value, err := decodeStrictJSON(schema)
	if err != nil {
		return nil, fmt.Errorf("structured schema must be valid JSON: %w", err)
	}
	if err := validateSchemaNode(value); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("structured-generation.json", value); err != nil {
		return nil, fmt.Errorf("structured schema is malformed: %w", err)
	}
	compiled, err := compiler.Compile("structured-generation.json")
	if err != nil {
		return nil, fmt.Errorf("structured schema cannot compile: %w", err)
	}
	return &compiledStructuredSchema{value: value, schema: compiled}, nil
}

func validateStructuredJSON(schema *compiledStructuredSchema, content json.RawMessage) error {
	value, err := decodeStrictJSON(content)
	if err != nil {
		return fmt.Errorf("structured response must be valid JSON: %w", err)
	}
	if err := schema.schema.Validate(value); err != nil {
		return fmt.Errorf("structured response does not match schema: %w", err)
	}
	return nil
}

func decodeStrictJSON(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("object key is not a string")
				}
				if _, duplicate := object[name]; duplicate {
					return nil, fmt.Errorf("duplicate object key %q", name)
				}
				value, err := decodeJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				object[name] = value
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			var list []any
			for decoder.More() {
				value, err := decodeJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return list, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", token)
		}
	default:
		return token, nil
	}
}

func validateSchemaNode(value any) error {
	node, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("structured schema nodes must be objects")
	}
	allowed := map[string]bool{"type": true, "description": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "enum": true, "const": true}
	for key := range node {
		if !allowed[key] {
			return fmt.Errorf("structured schema keyword %q is unsupported", key)
		}
	}
	if description, ok := node["description"]; ok {
		if _, ok := description.(string); !ok {
			return fmt.Errorf("structured schema description must be a string")
		}
	}
	if typ, ok := node["type"]; ok {
		if err := validateSchemaType(typ); err != nil {
			return err
		}
	}
	if properties, ok := node["properties"]; ok {
		fields, ok := properties.(map[string]any)
		if !ok {
			return fmt.Errorf("structured schema properties must be an object")
		}
		for _, field := range fields {
			if err := validateSchemaNode(field); err != nil {
				return err
			}
		}
	}
	if items, ok := node["items"]; ok {
		if err := validateSchemaNode(items); err != nil {
			return err
		}
	}
	if required, ok := node["required"]; ok {
		values, ok := required.([]any)
		if !ok {
			return fmt.Errorf("structured schema required must be an array")
		}
		for _, value := range values {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("structured schema required values must be strings")
			}
		}
	}
	if additional, ok := node["additionalProperties"]; ok {
		if _, ok := additional.(bool); !ok {
			return fmt.Errorf("structured schema additionalProperties must be a boolean")
		}
	}
	if enum, ok := node["enum"]; ok {
		if _, ok := enum.([]any); !ok {
			return fmt.Errorf("structured schema enum must be an array")
		}
	}
	return nil
}

func validateSchemaType(value any) error {
	valid := func(value string) bool {
		return value == "object" || value == "array" || value == "string" || value == "number" || value == "integer" || value == "boolean" || value == "null"
	}
	switch value := value.(type) {
	case string:
		if !valid(value) {
			return fmt.Errorf("structured schema type %q is unsupported", value)
		}
	case []any:
		if len(value) == 0 {
			return fmt.Errorf("structured schema type array must not be empty")
		}
		for _, item := range value {
			typ, ok := item.(string)
			if !ok || !valid(typ) {
				return fmt.Errorf("structured schema type contains an unsupported value")
			}
		}
	default:
		return fmt.Errorf("structured schema type must be a string or array")
	}
	return nil
}
