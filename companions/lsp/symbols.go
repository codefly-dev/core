package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/wool"
)

// lspDocumentSymbol represents the LSP DocumentSymbol structure.
type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail,omitempty"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// ListSymbols returns symbols for a single file or the entire workspace.
func (c *companionClient) ListSymbols(ctx context.Context, file string) ([]*codev0.Symbol, error) {
	w := wool.Get(ctx).In("lsp.ListSymbols")

	if file != "" {
		return c.documentSymbols(ctx, file)
	}

	// Walk the source directory on the HOST.
	var allSymbols []*codev0.Symbol
	skipDirs := make(map[string]bool)
	for _, d := range c.cfg.SkipDirs {
		skipDirs[d] = true
	}

	extMatch := make(map[string]bool)
	for _, ext := range c.cfg.FileExtensions {
		extMatch[ext] = true
	}

	err := filepath.Walk(c.rootDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		if !extMatch[ext] {
			return nil
		}
		// Skip test files for Go (convention: _test.go).
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(c.rootDir, p)
		symbols, err := c.documentSymbols(ctx, rel)
		if err != nil {
			w.Warn("cannot get symbols for file", wool.FileField(rel), wool.ErrField(err))
			return nil
		}
		allSymbols = append(allSymbols, symbols...)
		return nil
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot walk directory")
	}
	return allSymbols, nil
}

// documentSymbols queries the LSP server for symbols in a specific file.
func (c *companionClient) documentSymbols(ctx context.Context, relPath string) ([]*codev0.Symbol, error) {
	fileURI, err := c.openFile(relPath)
	if err != nil {
		return nil, err
	}

	result, err := c.tp.call(ctx, "textDocument/documentSymbol", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("documentSymbol: %w", err)
	}

	var lspSymbols []lspDocumentSymbol
	if err := json.Unmarshal(result, &lspSymbols); err != nil {
		return nil, fmt.Errorf("cannot parse symbols response: %w", err)
	}

	var symbols []*codev0.Symbol
	for _, s := range lspSymbols {
		symbols = append(symbols, convertSymbol(relPath, "", s))
	}

	return symbols, nil
}

// convertSymbol converts an LSP DocumentSymbol to our proto Symbol, recursively.
func convertSymbol(file string, parent string, s lspDocumentSymbol) *codev0.Symbol {
	sym := &codev0.Symbol{
		Name: s.Name,
		Kind: lspKindToProto(s.Kind),
		Location: &codev0.Location{
			File:      file,
			Line:      int32(s.Range.Start.Line + 1),
			Column:    int32(s.Range.Start.Character + 1),
			EndLine:   int32(s.Range.End.Line + 1),
			EndColumn: int32(s.Range.End.Character + 1),
		},
		Signature:     s.Detail,
		Documentation: "",
		Parent:        parent,
	}

	for _, child := range s.Children {
		sym.Children = append(sym.Children, convertSymbol(file, s.Name, child))
	}

	return sym
}

// lspKindToProto maps LSP SymbolKind numbers to our proto enum.
func lspKindToProto(kind int) codev0.SymbolKind {
	switch kind {
	case 5: // Class
		return codev0.SymbolKind_SYMBOL_KIND_CLASS
	case 6: // Method
		return codev0.SymbolKind_SYMBOL_KIND_METHOD
	case 8: // Field
		return codev0.SymbolKind_SYMBOL_KIND_FIELD
	case 10: // Enum
		return codev0.SymbolKind_SYMBOL_KIND_ENUM
	case 11: // Interface
		return codev0.SymbolKind_SYMBOL_KIND_INTERFACE
	case 12: // Function
		return codev0.SymbolKind_SYMBOL_KIND_FUNCTION
	case 13: // Variable
		return codev0.SymbolKind_SYMBOL_KIND_VARIABLE
	case 14: // Constant
		return codev0.SymbolKind_SYMBOL_KIND_CONSTANT
	case 23: // Struct
		return codev0.SymbolKind_SYMBOL_KIND_STRUCT
	case 26: // TypeParameter
		return codev0.SymbolKind_SYMBOL_KIND_TYPE_ALIAS
	default:
		return codev0.SymbolKind_SYMBOL_KIND_UNKNOWN
	}
}
