package toolbox

import (
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/types/known/structpb"
)

// ValidateArguments compiles the approved JSON Schema and validates arguments.
// Missing or malformed schemas fail closed: every production tool contract must
// describe its input, even when that input is an empty object.
func ValidateArguments(schema, arguments *structpb.Struct) error {
	compiled, err := compileSchema(schema)
	if err != nil {
		return err
	}
	var value any = map[string]any{}
	if arguments != nil {
		value = arguments.AsMap()
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), "\n", " | "))
	}
	return nil
}

// ValidateSchema checks only the descriptor contract, without requiring that
// an empty argument object satisfy fields required at invocation time.
func ValidateSchema(schema *structpb.Struct) error {
	_, err := compileSchema(schema)
	return err
}

func compileSchema(schema *structpb.Struct) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, fmt.Errorf("input schema is required")
	}
	schemaMap := schema.AsMap()
	if len(schemaMap) == 0 {
		return nil, fmt.Errorf("input schema must not be empty")
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("inline.json", schemaMap); err != nil {
		return nil, fmt.Errorf("schema definition malformed: %w", err)
	}
	compiled, err := compiler.Compile("inline.json")
	if err != nil {
		return nil, fmt.Errorf("schema compile failed: %w", err)
	}
	return compiled, nil
}
