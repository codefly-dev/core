package treesitter

// Hover returns the source text of the node at (line, col). Tree-sitter alone
// cannot produce semantic hover (types, docs). Callers layer the symbol index
// and summary store above for rich hover content.

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

func (c *fileScopedClient) Hover(ctx context.Context, file string, line, col int) (*HoverResult, error) {
	tree, content, err := c.parseFile(ctx, file)
	if err != nil {
		return nil, err
	}
	if line < 1 || col < 1 {
		return nil, fmt.Errorf("position must be 1-based; got line=%d col=%d", line, col)
	}
	pt := sitter.Point{Row: uint32(line - 1), Column: uint32(col - 1)}
	n := tree.RootNode().NamedDescendantForPointRange(pt, pt)
	if n == nil {
		return &HoverResult{Language: c.cfg.LanguageID}, nil
	}
	return &HoverResult{
		Content:  nodeText(n, content),
		Language: c.cfg.LanguageID,
	}, nil
}
