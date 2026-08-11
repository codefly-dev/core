package code

// ARCHITECTURE: tree-sitter stays inside the Codefly source boundary. Parse
// diagnostics may expose stable body-free coordinates and node kinds, but
// never source text or agent-local filesystem paths.

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

// treeSitterSyntaxDiagnostic identifies the first recoverable syntax error in
// source order so callers can act on a degraded parse without receiving source
// bytes. Tree-sitter points are zero-based; diagnostics use one-based positions.
func treeSitterSyntaxDiagnostic(root *sitter.Node) string {
	node := firstTreeSitterSyntaxError(root)
	if node == nil {
		return "syntax tree contains errors"
	}
	start, end := node.StartPoint(), node.EndPoint()
	kind := node.Type()
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
	if node == nil || node.IsNull() || (!node.HasError() && !node.IsError() && !node.IsMissing()) {
		return nil
	}
	for index := range int(node.ChildCount()) {
		if found := firstTreeSitterSyntaxError(node.Child(index)); found != nil {
			return found
		}
	}
	return node
}
