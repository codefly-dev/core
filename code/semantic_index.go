package code

import (
	"context"
	"fmt"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	codev1 "github.com/codefly-dev/core/generated/go/codefly/services/code/v1"
)

// getSemanticIndex delegates to the installed semantic analyzer. The base
// server carries no tree-sitter grammars, so without an analyzer the operation
// is explicitly unsupported rather than an empty index a caller could mistake
// for complete coverage.
func (s *DefaultCodeServer) getSemanticIndex(ctx context.Context, _ *codev1.GetSemanticIndexRequest) (*codev1.CodeResponse, error) {
	if s.semantic == nil {
		return codeFailure(
			&codev1.CodeResponse{Result: &codev1.CodeResponse_GetSemanticIndex{GetSemanticIndex: &basev1.SemanticIndex{
				State: basev1.SemanticIndexState_SEMANTIC_INDEX_STATE_NOT_ATTEMPTED,
			}}},
			basev1.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION,
			"code.get-semantic-index",
			"semantic index requires a source-semantics analyzer",
		), nil
	}
	index, err := s.semantic.SemanticIndex(ctx, s.SourceDir, s.FS)
	if err != nil {
		return nil, fmt.Errorf("semantic index: %w", err)
	}
	return &codev1.CodeResponse{Result: &codev1.CodeResponse_GetSemanticIndex{GetSemanticIndex: index}}, nil
}
