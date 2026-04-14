package treesitter

// ARCHITECTURE: Definition and References implement name-based resolution on
// top of the cached parsed trees. This is the tree-sitter equivalent of LSP
// goToDefinition / findReferences, following CIS 7.1 confidence tiers:
//
//   1. Local / same-file scope         → confidence 1.00
//   2. Explicit named import           → confidence 0.95
//   3. Wildcard / package-level match  → confidence 0.75
//
// We deliberately do NOT consult a type-checker. Tree-sitter is the primary
// layer; the compiler runs at commit time for type-aware validation.
//
// This is a deterministic best-effort resolver. Callers that need exact
// resolution build a symbol index on top and cross-check the candidates.

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

// Definition returns the definition location(s) for the identifier at (line, col).
func (c *fileScopedClient) Definition(ctx context.Context, file string, line, col int) ([]LocationResult, error) {
	name, _, err := c.identifierAt(ctx, file, line, col)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, nil
	}

	// 1. Local scope: search the current file first.
	local, err := c.findDefinitionsInFile(ctx, file, name)
	if err != nil {
		return nil, err
	}
	if len(local) > 0 {
		out := make([]LocationResult, 0, len(local))
		for _, l := range local {
			l.Confidence = 1.0
			l.Source = "tree_sitter"
			out = append(out, l)
		}
		return out, nil
	}

	// 2 + 3. Workspace scan. Every match outside the current file is a
	// candidate. We mark them as explicit/wildcard the same way since we
	// don't track per-language imports here — language subpackages that
	// want stronger resolution can replace this method.
	var all []LocationResult
	err = c.walkSourceFiles(func(rel string) error {
		if rel == file {
			return nil
		}
		defs, derr := c.findDefinitionsInFile(ctx, rel, name)
		if derr != nil {
			return nil
		}
		for _, d := range defs {
			d.Confidence = 0.75
			d.Source = "resolved_wildcard"
			all = append(all, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// References returns every usage of the identifier at (line, col) across the
// workspace, including the definition site.
func (c *fileScopedClient) References(ctx context.Context, file string, line, col int) ([]LocationResult, error) {
	name, _, err := c.identifierAt(ctx, file, line, col)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, nil
	}

	var all []LocationResult
	err = c.walkSourceFiles(func(rel string) error {
		refs, rerr := c.findReferencesInFile(ctx, rel, name)
		if rerr != nil {
			return nil
		}
		for _, r := range refs {
			conf := float32(0.75)
			if rel == file {
				conf = 1.0
			}
			r.Confidence = conf
			r.Source = "tree_sitter"
			all = append(all, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// identifierAt returns the identifier name at (line, col) within file.
// Returns ("", nil, nil) if the position is not over an identifier-like node.
func (c *fileScopedClient) identifierAt(ctx context.Context, file string, line, col int) (string, *sitter.Node, error) {
	tree, content, err := c.parseFile(ctx, file)
	if err != nil {
		return "", nil, err
	}
	if line < 1 || col < 1 {
		return "", nil, fmt.Errorf("position must be 1-based; got line=%d col=%d", line, col)
	}
	pt := sitter.Point{Row: uint32(line - 1), Column: uint32(col - 1)}
	n := tree.RootNode().NamedDescendantForPointRange(pt, pt)
	if n == nil {
		return "", nil, nil
	}
	if !isIdentifierLike(n.Type()) {
		return "", nil, nil
	}
	return nodeText(n, content), n, nil
}

// findDefinitionsInFile scans a file for identifier nodes whose parent looks
// like a definition (function_declaration, type_declaration, etc.) matching name.
func (c *fileScopedClient) findDefinitionsInFile(ctx context.Context, file, name string) ([]LocationResult, error) {
	tree, content, err := c.parseFile(ctx, file)
	if err != nil {
		return nil, err
	}
	var out []LocationResult
	walkDefinitions(tree.RootNode(), content, name, file, &out)
	return out, nil
}

// findReferencesInFile scans a file for identifier nodes matching name.
func (c *fileScopedClient) findReferencesInFile(ctx context.Context, file, name string) ([]LocationResult, error) {
	tree, content, err := c.parseFile(ctx, file)
	if err != nil {
		return nil, err
	}
	var out []LocationResult
	walkIdentifiers(tree.RootNode(), content, name, file, &out)
	return out, nil
}

// walkDefinitions collects identifier nodes whose parent is a typical
// declaration node and whose text equals name.
func walkDefinitions(n *sitter.Node, content []byte, name, file string, out *[]LocationResult) {
	if n == nil {
		return
	}
	if isIdentifierLike(n.Type()) && nodeText(n, content) == name {
		if p := n.Parent(); p != nil && isDeclarationLike(p.Type()) {
			*out = append(*out, nodeToLocation(n, file))
		}
	}
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		walkDefinitions(n.Child(i), content, name, file, out)
	}
}

// walkIdentifiers collects every identifier node matching name.
func walkIdentifiers(n *sitter.Node, content []byte, name, file string, out *[]LocationResult) {
	if n == nil {
		return
	}
	if isIdentifierLike(n.Type()) && nodeText(n, content) == name {
		*out = append(*out, nodeToLocation(n, file))
	}
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		walkIdentifiers(n.Child(i), content, name, file, out)
	}
}

func nodeToLocation(n *sitter.Node, file string) LocationResult {
	start := n.StartPoint()
	end := n.EndPoint()
	return LocationResult{
		File:      file,
		Line:      int(start.Row) + 1,
		Column:    int(start.Column) + 1,
		EndLine:   int(end.Row) + 1,
		EndColumn: int(end.Column) + 1,
	}
}

// isIdentifierLike is a conservative predicate for identifier-shaped node types
// across the languages we target. Subpackages may replace this if they want
// stricter language-specific filters.
func isIdentifierLike(kind string) bool {
	switch kind {
	case "identifier",
		"field_identifier",
		"type_identifier",
		"package_identifier",
		"property_identifier",
		"shorthand_property_identifier":
		return true
	}
	return false
}

// isDeclarationLike is a conservative predicate for nodes that introduce
// a named binding across the languages we target.
func isDeclarationLike(kind string) bool {
	switch kind {
	case "function_declaration",
		"method_declaration",
		"type_spec",
		"const_spec",
		"var_spec",
		"short_var_declaration",
		"parameter_declaration",
		"field_declaration",
		"class_definition",
		"function_definition",
		"assignment":
		return true
	}
	return false
}
