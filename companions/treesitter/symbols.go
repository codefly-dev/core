package treesitter

// ARCHITECTURE: ListSymbols walks the workspace (or a single file), parses
// each source file with tree-sitter, and delegates language-specific extraction
// to LanguageConfig.ExtractSymbols. The core package has no per-language code.

import (
	"context"
	"fmt"
	"github.com/codefly-dev/core/wool"
)

// ListSymbols returns symbols for a single file (relative path) or the whole
// workspace when file is empty.
func (c *fileScopedClient) ListSymbols(ctx context.Context, file string) ([]*Symbol, error) {
	w := wool.Get(ctx).In("treesitter.ListSymbols")

	if file != "" {
		return c.symbolsInFile(ctx, file)
	}

	var all []*Symbol
	err := c.walkSourceFiles(func(rel string) error {
		syms, serr := c.symbolsInFile(ctx, rel)
		if serr != nil {
			w.Warn("cannot extract symbols", wool.FileField(rel), wool.ErrField(serr))
			return nil
		}
		all = append(all, syms...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk source files: %w", err)
	}
	return all, nil
}

// symbolsInFile parses one file and runs the language extractor.
func (c *fileScopedClient) symbolsInFile(ctx context.Context, relPath string) ([]*Symbol, error) {
	tree, content, err := c.parseFile(ctx, relPath)
	if err != nil {
		return nil, err
	}
	return c.cfg.ExtractSymbols(tree, content, relPath), nil
}
