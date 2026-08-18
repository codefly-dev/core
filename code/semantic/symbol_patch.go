package semantic

// ARCHITECTURE: typed symbol mutation parses beside semantic projection inside
// Codefly. The same parser owns declaration identity, byte ranges, and syntax
// validation; the base server applies the resulting spans without a parser.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/code"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Language returns the semantic language name for path and whether typed symbol
// mutation supports it.
func (a *Analyzer) Language(path string) (string, bool) {
	definition, ok := semanticLanguageForExtension(strings.ToLower(filepath.Ext(path)))
	return definition.name, ok
}

// DeclarationSpans returns every declaration span in body, keyed for typed
// symbol mutation.
func (a *Analyzer) DeclarationSpans(ctx context.Context, path string, body []byte) ([]code.SymbolSpan, error) {
	definition, ok := semanticLanguageForExtension(strings.ToLower(filepath.Ext(path)))
	if !ok {
		return nil, fmt.Errorf("semantic symbol mutation is unsupported for %s", path)
	}
	tree, err := parseSyntaxTree(ctx, definition.grammar, body)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return nil, fmt.Errorf("syntax tree contains errors")
	}
	packageName := semanticPackage(definition.name, path, root, body)
	var symbols []*basev0.SemanticSymbol
	var projections []semanticSymbolProjection
	projectSemanticDeclarations(definition.name, path, packageName, "", false, root, body, &symbols, &projections)
	spans := make([]code.SymbolSpan, 0, len(projections))
	for _, projection := range projections {
		spans = append(spans, code.SymbolSpan{
			Symbol:         projection.symbol,
			StartByte:      projection.startByte,
			EndByte:        projection.endByte,
			DeclarationKey: projection.declarationKey,
		})
	}
	return spans, nil
}

// ValidateSyntax reports whether body parses as valid source for path.
func (a *Analyzer) ValidateSyntax(ctx context.Context, path string, body []byte) error {
	definition, ok := semanticLanguageForExtension(strings.ToLower(filepath.Ext(path)))
	if !ok {
		return fmt.Errorf("semantic syntax validation is unsupported for %s", path)
	}
	tree, err := parseSyntaxTree(ctx, definition.grammar, body)
	if err != nil {
		return err
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return fmt.Errorf("syntax tree contains errors")
	}
	return nil
}
