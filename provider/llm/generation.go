package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// GenerationExecutor is the transport-neutral port for a single structured,
// non-streaming model generation. Implementations own provider translation,
// credentials, dispatch, and durable invocation storage.
type GenerationExecutor interface {
	Generate(context.Context, *GenerationRequest) (*GenerationResult, error)
	Lookup(context.Context, *GenerationLookup) (*GenerationResult, error)
}

// StructuredClient validates the shared structured-generation contract around
// an injected executor. It has no provider or module dependency.
type StructuredClient struct {
	exec GenerationExecutor
}

// NewStructuredClient wraps a structured-generation executor.
func NewStructuredClient(exec GenerationExecutor) *StructuredClient {
	return &StructuredClient{exec: exec}
}

// GenerationRequest describes one no-tool, non-streaming structured generation.
type GenerationRequest struct {
	Model       ModelIdentity
	Messages    []Message
	System      string
	MaxTokens   int64
	Temperature string
	Schema      json.RawMessage
	Invocation  Invocation
}

// ModelIdentity identifies the selected model and optional deployment profile.
// A profile is adapter-defined and is never a provider URL.
type ModelIdentity struct {
	Model   string
	Profile string
}

// Invocation binds an idempotent invocation identifier to the exact caller
// intent. IntentDigest must change whenever messages, schema, or generation
// settings change.
type Invocation struct {
	ID           string
	IntentDigest string
}

// GenerationLookup recovers a prior invocation without dispatching another
// sample. Receipt is an opaque adapter reference when an adapter has one.
type GenerationLookup struct {
	Invocation Invocation
	Receipt    string
	Schema     json.RawMessage
}

// GenerationOutcome separates model content from dispatch settlement.
type GenerationOutcome string

const (
	GenerationCompleted  GenerationOutcome = "COMPLETED"
	GenerationRefused    GenerationOutcome = "REFUSED"
	GenerationIncomplete GenerationOutcome = "INCOMPLETE"
	GenerationCanceled   GenerationOutcome = "CANCELED"
	GenerationUncertain  GenerationOutcome = "UNCERTAIN"
)

// GenerationUsage is optional token accounting. A nil Usage means the adapter
// does not know usage; a zero token count is therefore an explicit value.
type GenerationUsage struct {
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
}

// GenerationResult is the settled result of a structured generation. JSON is
// populated only for a completed generation and is validated against Schema by
// StructuredClient. A JSON null is a valid completed abstention when Schema
// permits null.
type GenerationResult struct {
	Invocation Invocation
	Receipt    string
	Outcome    GenerationOutcome
	JSON       json.RawMessage
	Refusal    string
	Usage      *GenerationUsage
}

// Generate dispatches one generation and validates its returned contract.
func (c *StructuredClient) Generate(ctx context.Context, request *GenerationRequest) (*GenerationResult, error) {
	if err := validateGenerationRequest(request); err != nil {
		return nil, err
	}
	result, err := c.exec.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationResult(request.Invocation, request.Schema, result); err != nil {
		return nil, err
	}
	return result, nil
}

// Lookup recovers a prior result. It never calls Generate; executors must use
// Invocation and Receipt only to locate their durable invocation record.
func (c *StructuredClient) Lookup(ctx context.Context, lookup *GenerationLookup) (*GenerationResult, error) {
	if err := validateGenerationLookup(lookup); err != nil {
		return nil, err
	}
	result, err := c.exec.Lookup(ctx, lookup)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationResult(lookup.Invocation, lookup.Schema, result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateGenerationRequest(request *GenerationRequest) error {
	if request == nil {
		return fmt.Errorf("generation request is required")
	}
	if request.Model.Model == "" {
		return fmt.Errorf("generation model is required")
	}
	if err := validateInvocation(request.Invocation); err != nil {
		return err
	}
	return ValidateStructuredSchema(request.Schema)
}

func validateGenerationLookup(lookup *GenerationLookup) error {
	if lookup == nil {
		return fmt.Errorf("generation lookup is required")
	}
	if err := validateInvocation(lookup.Invocation); err != nil {
		return err
	}
	return ValidateStructuredSchema(lookup.Schema)
}

func validateInvocation(invocation Invocation) error {
	if invocation.ID == "" {
		return fmt.Errorf("invocation id is required")
	}
	if invocation.IntentDigest == "" {
		return fmt.Errorf("invocation intent digest is required")
	}
	return nil
}

func validateGenerationResult(invocation Invocation, schema json.RawMessage, result *GenerationResult) error {
	if result == nil {
		return fmt.Errorf("generation result is required")
	}
	if result.Invocation != invocation {
		return fmt.Errorf("generation result invocation does not match request")
	}
	switch result.Outcome {
	case GenerationCompleted:
		if result.Refusal != "" {
			return fmt.Errorf("completed generation cannot include a refusal")
		}
		if len(result.JSON) == 0 {
			return fmt.Errorf("completed generation requires structured JSON")
		}
		return ValidateStructuredJSON(schema, result.JSON)
	case GenerationRefused:
		if result.Refusal == "" {
			return fmt.Errorf("refused generation requires a refusal")
		}
	case GenerationIncomplete, GenerationCanceled, GenerationUncertain:
		if len(result.JSON) != 0 || result.Refusal != "" {
			return fmt.Errorf("%s generation cannot include model content", strings.ToLower(string(result.Outcome)))
		}
	default:
		return fmt.Errorf("unknown generation outcome %q", result.Outcome)
	}
	return nil
}

// ValidateStructuredSchema accepts the portable output-schema profile. The
// profile supports type (including a nullable type array), description,
// properties, required, additionalProperties, items, enum, and const. Other
// JSON Schema keywords are rejected so adapters cannot silently lose meaning.
func ValidateStructuredSchema(schema json.RawMessage) error {
	var value any
	if len(schema) == 0 || json.Unmarshal(schema, &value) != nil {
		return fmt.Errorf("structured schema must be valid JSON")
	}
	if err := validateSchemaNode(value); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("structured-generation.json", value); err != nil {
		return fmt.Errorf("structured schema is malformed: %w", err)
	}
	if _, err := compiler.Compile("structured-generation.json"); err != nil {
		return fmt.Errorf("structured schema cannot compile: %w", err)
	}
	return nil
}

// ValidateStructuredJSON validates one completed JSON value against the exact
// installed output schema.
func ValidateStructuredJSON(schema, content json.RawMessage) error {
	if err := ValidateStructuredSchema(schema); err != nil {
		return err
	}
	var schemaValue, contentValue any
	_ = json.Unmarshal(schema, &schemaValue)
	if err := json.Unmarshal(content, &contentValue); err != nil {
		return fmt.Errorf("structured response must be valid JSON: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("structured-generation.json", schemaValue); err != nil {
		return fmt.Errorf("structured schema is malformed: %w", err)
	}
	compiled, err := compiler.Compile("structured-generation.json")
	if err != nil {
		return fmt.Errorf("structured schema cannot compile: %w", err)
	}
	if err := compiled.Validate(contentValue); err != nil {
		return fmt.Errorf("structured response does not match schema: %w", err)
	}
	return nil
}

func validateSchemaNode(value any) error {
	node, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("structured schema nodes must be objects")
	}
	allowed := map[string]bool{
		"type": true, "description": true, "properties": true, "required": true,
		"additionalProperties": true, "items": true, "enum": true, "const": true,
	}
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
