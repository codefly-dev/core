package toolbox_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/codefly-dev/core/toolbox"
)

func TestRedactWithSchemaUsesClassificationAndFailsClosedOnUnknownFields(t *testing.T) {
	schema, err := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"visible":  map[string]any{"type": "string"},
			"password": map[string]any{"type": "string", "writeOnly": true},
			"tenant":   map[string]any{"type": "string", "x-codefly-classification": "tenant"},
			"nested": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token": map[string]any{"type": "string", "x-codefly-sensitive": true},
				},
			},
		},
	})
	require.NoError(t, err)
	redacted := toolbox.RedactWithSchema(schema, map[string]any{
		"visible": "safe", "password": "hunter2", "tenant": "tenant-secret",
		"unknown": "fail-closed", "nested": map[string]any{"token": "bearer-secret"},
	})
	require.Equal(t, map[string]any{
		"visible": "safe", "password": toolbox.RedactedValue, "tenant": toolbox.RedactedValue,
		"unknown": toolbox.RedactedValue, "nested": map[string]any{"token": toolbox.RedactedValue},
	}, redacted)
	require.NotContains(t, redacted, "hunter2")
}

func TestRedactWithSchemaWithoutClassificationContractFailsClosed(t *testing.T) {
	require.Equal(t, toolbox.RedactedValue, toolbox.RedactWithSchema(nil, map[string]any{"token": "secret"}))
}

func TestRedactWithSchemaFailsClosedOnUnsupportedOrAmbiguousSchemas(t *testing.T) {
	schema, err := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"composite": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "string", "x-codefly-sensitive": true},
				},
			},
			"typeless":       map[string]any{"description": "ambiguous primitive"},
			"classification": map[string]any{"type": "string", "x-codefly-classification": "future-private"},
			"mismatch":       map[string]any{"type": "boolean"},
		},
	})
	require.NoError(t, err)

	require.Equal(t, map[string]any{
		"composite":      toolbox.RedactedValue,
		"typeless":       toolbox.RedactedValue,
		"classification": toolbox.RedactedValue,
		"mismatch":       toolbox.RedactedValue,
	}, toolbox.RedactWithSchema(schema, map[string]any{
		"composite": "secret-in-one-branch", "typeless": "unknown",
		"classification": "future-secret", "mismatch": "not-a-bool",
	}))
}
