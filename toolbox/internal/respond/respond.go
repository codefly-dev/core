// Package respond holds the small set of response-shaping helpers
// that every toolbox.Toolbox implementation needs: extract
// arguments from a CallToolRequest, build error / structured /
// content responses, and bake JSON-Schema constants into the
// proto Struct shape.
//
// Lives under internal/ so it's import-restricted to core/toolbox/...
// — these are toolbox-implementation utilities, not part of the
// public API surface a third-party toolbox would import (those
// import the proto package directly and roll their own).
//
// Why centralized: drift was already a risk. The git, web, and
// docker toolboxes each had their own copies of `argMap` /
// `errResp` / `structResp` / `mustSchema` — four functions, three
// copies. Any bug in error formatting or schema-panic behavior
// would have to be fixed in three places. This package collapses
// them.
package respond

import (
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

// Args extracts a CallToolRequest's structured arguments as a Go
// map. Returns an empty map (not nil) when no arguments are
// supplied — callers can read keys with a comma-ok idiom without
// nil-checking.
func Args(req *toolboxv0.CallToolRequest) map[string]any {
	if req == nil || req.Arguments == nil {
		return map[string]any{}
	}
	return req.Arguments.AsMap()
}

// Error builds a non-routed error response. The Error field is
// populated; CanonicalRouted stays false. Use this for tool-level
// failures (open repo failed, unknown method, validation fail) —
// canonical-routing decisions live on the bash side, not inside a
// toolbox's own dispatch.
func Error(format string, args ...any) *toolboxv0.CallToolResponse {
	return &toolboxv0.CallToolResponse{Error: fmt.Sprintf(format, args...)}
}

// Struct wraps a Go map as a single structured Content block.
// Returns an Error response (NOT a panic) if the map can't be
// marshaled to a proto Struct — that's a programmer-error path
// (channels, functions, etc. in the payload), but a runtime fault
// shouldn't be a panic at the host level.
func Struct(payload map[string]any) *toolboxv0.CallToolResponse {
	s, err := structpb.NewStruct(payload)
	if err != nil {
		return Error("internal: cannot marshal response: %v", err)
	}
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Structured{Structured: s}},
		},
	}
}

// Text wraps a string as a single text Content block. Useful for
// tools whose natural output is unstructured (man pages, README
// excerpts, raw command stdout).
func Text(text string) *toolboxv0.CallToolResponse {
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Text{Text: text}},
		},
	}
}

// Schema converts a JSON-Schema-shaped Go map to a proto Struct.
// Used at server construction to bake the input/output schemas
// into Tool definitions; a failure here is a programmer typo and
// MUST surface immediately — schemas are constants and a malformed
// one means the binary is shipping a broken contract. Hence panic
// rather than returning an error.
func Schema(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("respond.Schema: bad input schema: %v", err))
	}
	return s
}
