package code

// ARCHITECTURE: tree-sitter stays inside the Codefly source boundary. Parse
// diagnostics may expose stable body-free coordinates and node kinds, but
// never source text or agent-local filesystem paths.

import (
	"context"
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// parseSyntaxTree owns the maintained Tree-sitter binding lifecycle and turns
// parser cancellation or grammar ABI failures into explicit agent-boundary
// errors. Language-specific callers only choose a grammar and inspect the
// resulting tree.
func parseSyntaxTree(ctx context.Context, grammar *sitter.Language, source []byte) (*sitter.Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammar); err != nil {
		return nil, fmt.Errorf("configure tree-sitter grammar: %w", err)
	}
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if offset < 0 || offset >= len(source) {
			return nil
		}
		return source[offset:]
	}, nil, &sitter.ParseOptions{
		ProgressCallback: func(sitter.ParseState) bool { return ctx.Err() != nil },
	})
	if err := ctx.Err(); err != nil {
		if tree != nil {
			tree.Close()
		}
		return nil, err
	}
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter parser returned no syntax tree")
	}
	return tree, nil
}

// treeSitterSyntaxDiagnostic identifies the first recoverable syntax error in
// source order so callers can act on a degraded parse without receiving source
// bytes. Tree-sitter points are zero-based; diagnostics use one-based positions.
func treeSitterSyntaxDiagnostic(root *sitter.Node) string {
	node := firstTreeSitterSyntaxError(root)
	if node == nil {
		return "syntax tree contains errors"
	}
	start, end := node.StartPosition(), node.EndPosition()
	kind := node.Kind()
	switch {
	case node.IsMissing():
		kind = fmt.Sprintf("missing %q", kind)
	case node.IsError():
		kind = "ERROR"
	default:
		kind = fmt.Sprintf("error in %q", kind)
	}
	return fmt.Sprintf(
		"syntax tree contains errors: first %s at %d:%d-%d:%d",
		kind, start.Row+1, start.Column+1, end.Row+1, end.Column+1,
	)
}

func firstTreeSitterSyntaxError(node *sitter.Node) *sitter.Node {
	if node == nil || (!node.HasError() && !node.IsError() && !node.IsMissing()) {
		return nil
	}
	for index := uint(0); index < node.ChildCount(); index++ {
		if found := firstTreeSitterSyntaxError(node.Child(index)); found != nil {
			return found
		}
	}
	return node
}
