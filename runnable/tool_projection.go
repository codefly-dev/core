package runnable

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

const (
	// MaxToolProjectionBytes bounds a projection before decoding it.
	MaxToolProjectionBytes = 1 << 20
	// MaxProjectedTools bounds one projection's declaration count.
	MaxProjectedTools = 256
	// MaxToolSchemaBytes bounds each declared JSON Schema's canonical form.
	MaxToolSchemaBytes = 64 << 10
)

var (
	toolDigestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	toolSelectorFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// ValidateToolProjection validates a discovery declaration, not its publisher
// or any caller's authority. ToolsFromProjection additionally checks it against
// the complete prepared binding. A digest of an external declaration is checked
// for syntax only; admission freezes all fields, not just the stated digests.
func ValidateToolProjection(projection *runnablev0.ToolProjection) error {
	if projection == nil {
		return fmt.Errorf("%w: tool projection is required", ErrInvalid)
	}
	if !validToolProjectionText(projection.GetBindingId()) || strings.IndexFunc(projection.GetBindingId(), unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: projection binding_id must contain 1..512 UTF-8 bytes without whitespace or controls", ErrInvalid)
	}
	if !toolDigestPattern.MatchString(projection.GetContractDigest()) {
		return fmt.Errorf("%w: projection contract_digest must be sha256:<lowercase hex>", ErrInvalid)
	}
	if len(projection.GetTools()) == 0 || len(projection.GetTools()) > MaxProjectedTools {
		return fmt.Errorf("%w: projection must declare 1..%d tools", ErrInvalid, MaxProjectedTools)
	}
	// CanonicalJSON refuses unknown fields recursively and malformed protobuf
	// JSON values. Bound the whole document before compiling any schemas.
	encoded, err := CanonicalJSON(projection)
	if err != nil {
		return err
	}
	if len(encoded) > MaxToolProjectionBytes {
		return fmt.Errorf("%w: tool projection exceeds %d bytes", ErrInvalid, MaxToolProjectionBytes)
	}
	names := make(map[string]bool, len(projection.GetTools()))
	for _, tool := range projection.GetTools() {
		if tool == nil {
			return fmt.Errorf("%w: projected tool is required", ErrInvalid)
		}
		if err := validateToolMetadata(tool.GetName(), tool.GetDescription(), tool.GetEffect()); err != nil {
			return err
		}
		if names[tool.GetName()] {
			return fmt.Errorf("%w: duplicate projected tool name %q", ErrInvalid, tool.GetName())
		}
		names[tool.GetName()] = true
		if !toolDigestPattern.MatchString(tool.GetDigest()) {
			return fmt.Errorf("%w: tool %q declaration digest must be sha256:<lowercase hex>", ErrInvalid, tool.GetName())
		}
		selector := tool.GetSelector()
		if !toolSelectorFieldPattern.MatchString(selector.GetField()) || !validToolProjectionText(selector.GetValue()) {
			return fmt.Errorf("%w: tool %q requires a top-level selector field and bounded fixed string value", ErrInvalid, tool.GetName())
		}
		if err := validateToolSchema(tool.GetInputSchema(), true); err != nil {
			return fmt.Errorf("%w: tool %q input_schema: %w", ErrInvalid, tool.GetName(), err)
		}
		if err := validateToolSchema(tool.GetOutputSchema(), false); err != nil {
			return fmt.Errorf("%w: tool %q output_schema: %w", ErrInvalid, tool.GetName(), err)
		}
	}
	return nil
}

func validToolProjectionText(value string) bool {
	return len(value) > 0 && len(value) <= 512 && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validateToolSchema(schema *structpb.Struct, input bool) error {
	if schema == nil || len(schema.GetFields()) == 0 {
		return fmt.Errorf("nonempty JSON Schema object is required")
	}
	encoded, err := CanonicalJSON(schema)
	if err != nil {
		return err
	}
	if len(encoded) > MaxToolSchemaBytes {
		return fmt.Errorf("JSON Schema exceeds %d bytes", MaxToolSchemaBytes)
	}
	if input && schema.GetFields()["type"].GetStringValue() != "object" {
		return fmt.Errorf("input JSON Schema must declare root type object")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// An empty loader refuses both network and filesystem references. The
	// declaration carries its complete schema; admission fetches nothing.
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	const location = "https://projection.invalid/schema.json"
	if err := compiler.AddResource(location, schema.AsMap()); err != nil {
		return err
	}
	_, err = compiler.Compile(location)
	return err
}

// EncodeToolProjection validates and writes canonical proto3 JSON. It neither
// fills nor repairs digests, whose declaration content belongs to the publisher.
func EncodeToolProjection(projection *runnablev0.ToolProjection) ([]byte, error) {
	if err := ValidateToolProjection(projection); err != nil {
		return nil, err
	}
	return CanonicalJSON(projection)
}

// DecodeToolProjection accepts protobuf's snake_case and lowerCamelCase JSON
// spellings, refusing unknown fields and malformed or oversized declarations.
func DecodeToolProjection(value []byte) (*runnablev0.ToolProjection, error) {
	if len(value) == 0 || len(value) > MaxToolProjectionBytes {
		return nil, fmt.Errorf("%w: tool projection of %d bytes is outside (0, %d]", ErrInvalid, len(value), MaxToolProjectionBytes)
	}
	projection := &runnablev0.ToolProjection{}
	if err := protojson.Unmarshal(value, projection); err != nil {
		return nil, fmt.Errorf("%w: invalid tool projection JSON: %v", ErrInvalid, err)
	}
	if err := ValidateToolProjection(projection); err != nil {
		return nil, err
	}
	return projection, nil
}

// ToolsFromProjection verifies the complete binding and projection and returns
// detached tool declarations. The consumer must resolve projection.binding_id
// to this exact admitted binding: PreparedBinding has no installation id. A
// missing policy.tool hides direct exposure only; this separate declaration may
// still project tools. Neither this check nor an effect grants authority.
func ToolsFromProjection(binding *runnablev0.PreparedBinding, projection *runnablev0.ToolProjection) ([]*runnablev0.ProjectedTool, error) {
	if err := VerifyPrepared(binding); err != nil {
		return nil, err
	}
	if err := ValidateToolProjection(projection); err != nil {
		return nil, err
	}
	if projection.GetContractDigest() != binding.GetContractDigest() {
		return nil, fmt.Errorf("%w: projection contract_digest differs from prepared binding", ErrInvalid)
	}
	for _, tool := range projection.GetTools() {
		found := false
		for _, field := range binding.GetContract().GetInput().GetFields() {
			if field.GetName() == tool.GetSelector().GetField() && field.GetType() == basev0.RunnableField_STRING {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: tool %q selector must name a top-level string field in the prepared input", ErrInvalid, tool.GetName())
		}
	}
	return proto.CloneOf(projection).GetTools(), nil
}
