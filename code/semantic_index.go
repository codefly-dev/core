package code

import (
	"context"
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// getSemanticIndex delegates to the installed semantic analyzer. The base
// server carries no tree-sitter grammars, so without an analyzer the operation
// is explicitly unsupported rather than an empty index a caller could mistake
// for complete coverage.
func (s *DefaultCodeServer) getSemanticIndex(ctx context.Context, _ *codev0.GetSemanticIndexRequest) (*codev0.CodeResponse, error) {
	if s.semantic == nil {
		return codeFailure(
			&codev0.CodeResponse{Result: &codev0.CodeResponse_GetSemanticIndex{GetSemanticIndex: &basev0.SemanticIndex{
				State: basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_NOT_ATTEMPTED,
			}}},
			basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION,
			"code.get-semantic-index",
			"semantic index requires a source-semantics analyzer",
		), nil
	}
	index, err := s.semantic.SemanticIndex(ctx, s.SourceDir, s.FS)
	if err != nil {
		return nil, fmt.Errorf("semantic index: %w", err)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetSemanticIndex{GetSemanticIndex: index}}, nil
}
