package treesitter

// ARCHITECTURE: Diagnostics walks the parsed tree and emits one DiagnosticResult
// for every ERROR or MISSING node. Tree-sitter's error recovery keeps parsing
// through syntax errors, so the resulting tree may have multiple ERROR nodes.
// Type-level diagnostics come from the compiler at commit time, NOT here.

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/codefly-dev/core/wool"
)

// Diagnostics returns SYNTAX diagnostics for a file or the entire workspace.
func (c *fileScopedClient) Diagnostics(ctx context.Context, file string) ([]DiagnosticResult, error) {
	w := wool.Get(ctx).In("treesitter.Diagnostics")

	if file != "" {
		return c.diagsInFile(ctx, file)
	}

	var all []DiagnosticResult
	err := c.walkSourceFiles(func(rel string) error {
		d, derr := c.diagsInFile(ctx, rel)
		if derr != nil {
			w.Warn("cannot parse for diagnostics", wool.FileField(rel), wool.ErrField(derr))
			return nil
		}
		all = append(all, d...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk source files: %w", err)
	}
	return all, nil
}

// diagsInFile parses one file and extracts ERROR/MISSING node diagnostics.
func (c *fileScopedClient) diagsInFile(ctx context.Context, relPath string) ([]DiagnosticResult, error) {
	tree, _, err := c.parseFile(ctx, relPath)
	if err != nil {
		return nil, err
	}
	root := tree.RootNode()
	var out []DiagnosticResult
	walkErrorNodes(root, relPath, &out)
	return out, nil
}

// walkErrorNodes recursively collects ERROR and MISSING nodes as diagnostics.
func walkErrorNodes(n *sitter.Node, file string, out *[]DiagnosticResult) {
	if n == nil {
		return
	}
	if n.IsError() {
		start := n.StartPoint()
		end := n.EndPoint()
		*out = append(*out, DiagnosticResult{
			File:      file,
			Line:      int32(start.Row) + 1,
			Column:    int32(start.Column) + 1,
			EndLine:   int32(end.Row) + 1,
			EndColumn: int32(end.Column) + 1,
			Message:   "syntax error",
			Severity:  "error",
			Source:    "tree-sitter",
		})
	} else if n.IsMissing() {
		start := n.StartPoint()
		end := n.EndPoint()
		*out = append(*out, DiagnosticResult{
			File:      file,
			Line:      int32(start.Row) + 1,
			Column:    int32(start.Column) + 1,
			EndLine:   int32(end.Row) + 1,
			EndColumn: int32(end.Column) + 1,
			Message:   fmt.Sprintf("missing %s", n.Type()),
			Severity:  "error",
			Source:    "tree-sitter",
		})
	}
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		walkErrorNodes(n.Child(i), file, out)
	}
}
