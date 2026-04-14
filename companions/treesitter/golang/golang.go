// Package golang registers the Go tree-sitter grammar with the treesitter
// registry. Import for side effect:
//
//	_ "github.com/codefly-dev/core/companions/treesitter/golang"
//
// ARCHITECTURE: This package is intentionally thin. It provides:
//   - The Go grammar via github.com/smacker/go-tree-sitter/golang
//   - A SymbolExtractor that walks the Go syntax tree and produces codev0.Symbol
//
// Everything else (parsing, caching, workspace walking, diagnostics, resolve)
// lives in the parent package and is language-agnostic.
package golang

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsgo "github.com/smacker/go-tree-sitter/golang"

	"github.com/codefly-dev/core/companions/treesitter"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/languages"
)

func init() {
	treesitter.Register(languages.GO, &treesitter.LanguageConfig{
		LanguageID:     "go",
		FileExtensions: []string{".go"},
		SkipDirs:       []string{"vendor", "node_modules", ".git", "testdata"},
		SkipSuffixes:   []string{"_test.go"},
		Grammar:        tsgo.GetLanguage,
		ExtractSymbols: extractSymbols,
	})
}

// extractSymbols walks a parsed Go file and returns top-level symbols.
// Nested struct fields and interface methods are attached as Children.
func extractSymbols(tree *sitter.Tree, content []byte, relPath string) []*codev0.Symbol {
	root := tree.RootNode()
	pkg := findPackageName(root, content)

	var symbols []*codev0.Symbol
	count := int(root.ChildCount())
	for i := 0; i < count; i++ {
		child := root.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "function_declaration":
			if s := funcDecl(child, content, relPath, pkg); s != nil {
				symbols = append(symbols, s)
			}
		case "method_declaration":
			if s := methodDecl(child, content, relPath, pkg); s != nil {
				symbols = append(symbols, s)
			}
		case "type_declaration":
			symbols = append(symbols, typeDecls(child, content, relPath, pkg)...)
		case "const_declaration":
			symbols = append(symbols, valueDecls(child, content, relPath, pkg, codev0.SymbolKind_SYMBOL_KIND_CONSTANT)...)
		case "var_declaration":
			symbols = append(symbols, valueDecls(child, content, relPath, pkg, codev0.SymbolKind_SYMBOL_KIND_VARIABLE)...)
		}
	}
	return symbols
}

// findPackageName returns the package identifier for a Go file, or "".
func findPackageName(root *sitter.Node, content []byte) string {
	count := int(root.ChildCount())
	for i := 0; i < count; i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		if n.Type() != "package_clause" {
			continue
		}
		idn := n.ChildByFieldName("name")
		if idn == nil {
			// Fallback: first named child.
			nc := int(n.NamedChildCount())
			for j := 0; j < nc; j++ {
				c := n.NamedChild(j)
				if c != nil && c.Type() == "package_identifier" {
					idn = c
					break
				}
			}
		}
		if idn != nil {
			return textOf(idn, content)
		}
	}
	return ""
}

func funcDecl(n *sitter.Node, content []byte, file, pkg string) *codev0.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := textOf(nameNode, content)
	sig := signatureLine(n, content)
	return &codev0.Symbol{
		Name:          name,
		Kind:          codev0.SymbolKind_SYMBOL_KIND_FUNCTION,
		Location:      rangeToLocation(n, file),
		Signature:     sig,
		QualifiedName: qualify(pkg, "", name),
	}
}

func methodDecl(n *sitter.Node, content []byte, file, pkg string) *codev0.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := textOf(nameNode, content)
	recv := receiverType(n, content)
	sig := signatureLine(n, content)
	return &codev0.Symbol{
		Name:          name,
		Kind:          codev0.SymbolKind_SYMBOL_KIND_METHOD,
		Location:      rangeToLocation(n, file),
		Signature:     sig,
		Parent:        recv,
		QualifiedName: qualify(pkg, recv, name),
	}
}

// typeDecls handles `type ( ... )` groups and single `type Foo ...`.
func typeDecls(n *sitter.Node, content []byte, file, pkg string) []*codev0.Symbol {
	var out []*codev0.Symbol
	nc := int(n.NamedChildCount())
	for i := 0; i < nc; i++ {
		spec := n.NamedChild(i)
		if spec == nil || spec.Type() != "type_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		typeNode := spec.ChildByFieldName("type")
		if nameNode == nil {
			continue
		}
		name := textOf(nameNode, content)
		kind := codev0.SymbolKind_SYMBOL_KIND_TYPE_ALIAS
		var children []*codev0.Symbol
		if typeNode != nil {
			switch typeNode.Type() {
			case "struct_type":
				kind = codev0.SymbolKind_SYMBOL_KIND_STRUCT
				children = structFields(typeNode, content, file, name)
			case "interface_type":
				kind = codev0.SymbolKind_SYMBOL_KIND_INTERFACE
				children = interfaceMethods(typeNode, content, file, name)
			}
		}
		out = append(out, &codev0.Symbol{
			Name:          name,
			Kind:          kind,
			Location:      rangeToLocation(spec, file),
			Signature:     firstLine(textOf(spec, content)),
			Children:      children,
			QualifiedName: qualify(pkg, "", name),
		})
	}
	return out
}

func structFields(structNode *sitter.Node, content []byte, file, parent string) []*codev0.Symbol {
	// struct_type -> field_declaration_list -> field_declaration.
	// The list is not exposed via a field name in tree-sitter-go, so walk
	// named children to find it.
	var fdl *sitter.Node
	snc := int(structNode.NamedChildCount())
	for i := 0; i < snc; i++ {
		c := structNode.NamedChild(i)
		if c != nil && c.Type() == "field_declaration_list" {
			fdl = c
			break
		}
	}
	if fdl == nil {
		return nil
	}
	var out []*codev0.Symbol
	count := int(fdl.NamedChildCount())
	for i := 0; i < count; i++ {
		fd := fdl.NamedChild(i)
		if fd == nil || fd.Type() != "field_declaration" {
			continue
		}
		// A field_declaration may have multiple `name` fields (e.g. `X, Y int`).
		// Iterate ALL children and look for field_identifier nodes whose field
		// name is "name" (skipping the type_identifier on the `type` field).
		cc := int(fd.ChildCount())
		for j := 0; j < cc; j++ {
			c := fd.Child(j)
			if c == nil {
				continue
			}
			if fd.FieldNameForChild(j) != "name" {
				continue
			}
			out = append(out, &codev0.Symbol{
				Name:      textOf(c, content),
				Kind:      codev0.SymbolKind_SYMBOL_KIND_FIELD,
				Location:  rangeToLocation(c, file),
				Signature: firstLine(textOf(fd, content)),
				Parent:    parent,
			})
		}
	}
	return out
}

func interfaceMethods(ifaceNode *sitter.Node, content []byte, file, parent string) []*codev0.Symbol {
	var out []*codev0.Symbol
	count := int(ifaceNode.NamedChildCount())
	for i := 0; i < count; i++ {
		c := ifaceNode.NamedChild(i)
		if c == nil {
			continue
		}
		// tree-sitter-go exposes interface methods as "method_elem" (newer) or
		// "method_spec" (older). Handle both for compatibility.
		if c.Type() != "method_elem" && c.Type() != "method_spec" {
			continue
		}
		nameNode := c.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		out = append(out, &codev0.Symbol{
			Name:      textOf(nameNode, content),
			Kind:      codev0.SymbolKind_SYMBOL_KIND_METHOD,
			Location:  rangeToLocation(c, file),
			Signature: firstLine(textOf(c, content)),
			Parent:    parent,
		})
	}
	return out
}

// valueDecls handles const/var declarations (grouped or single).
func valueDecls(n *sitter.Node, content []byte, file, pkg string, kind codev0.SymbolKind) []*codev0.Symbol {
	var out []*codev0.Symbol
	specType := "const_spec"
	if kind == codev0.SymbolKind_SYMBOL_KIND_VARIABLE {
		specType = "var_spec"
	}
	nc := int(n.NamedChildCount())
	for i := 0; i < nc; i++ {
		spec := n.NamedChild(i)
		if spec == nil || spec.Type() != specType {
			continue
		}
		snc := int(spec.NamedChildCount())
		for j := 0; j < snc; j++ {
			c := spec.NamedChild(j)
			if c == nil || c.Type() != "identifier" {
				continue
			}
			name := textOf(c, content)
			out = append(out, &codev0.Symbol{
				Name:          name,
				Kind:          kind,
				Location:      rangeToLocation(c, file),
				Signature:     firstLine(textOf(spec, content)),
				QualifiedName: qualify(pkg, "", name),
			})
		}
	}
	return out
}

// receiverType returns the bare type name from a method receiver, stripping
// pointers and generic type params: `(s *Server[T])` → "Server".
func receiverType(method *sitter.Node, content []byte) string {
	recv := method.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	count := int(recv.NamedChildCount())
	for i := 0; i < count; i++ {
		pd := recv.NamedChild(i)
		if pd == nil || pd.Type() != "parameter_declaration" {
			continue
		}
		typeNode := pd.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		return stripPointerAndGenerics(textOf(typeNode, content))
	}
	return ""
}

func stripPointerAndGenerics(s string) string {
	s = strings.TrimPrefix(s, "*")
	if idx := strings.IndexByte(s, '['); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// signatureLine returns the declaration up to (but not including) the body.
func signatureLine(n *sitter.Node, content []byte) string {
	body := n.ChildByFieldName("body")
	if body == nil {
		return firstLine(textOf(n, content))
	}
	start := n.StartByte()
	end := body.StartByte()
	if end <= start || int(end) > len(content) {
		return firstLine(textOf(n, content))
	}
	return firstLine(strings.TrimRight(string(content[start:end]), " \t\n"))
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimRight(s[:idx], " \t\r")
	}
	return strings.TrimRight(s, " \t\r")
}

func textOf(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	start := int(n.StartByte())
	end := int(n.EndByte())
	if start < 0 || end > len(content) || start > end {
		return ""
	}
	return string(content[start:end])
}

func rangeToLocation(n *sitter.Node, file string) *codev0.Location {
	start := n.StartPoint()
	end := n.EndPoint()
	return &codev0.Location{
		File:      file,
		Line:      int32(start.Row) + 1,
		Column:    int32(start.Column) + 1,
		EndLine:   int32(end.Row) + 1,
		EndColumn: int32(end.Column) + 1,
	}
}

// qualify builds a fully-qualified symbol name: "pkg.Type.Method" or "pkg.Name".
func qualify(pkg, parent, name string) string {
	if pkg == "" {
		if parent != "" {
			return fmt.Sprintf("%s.%s", parent, name)
		}
		return name
	}
	if parent != "" {
		return fmt.Sprintf("%s.%s.%s", pkg, parent, name)
	}
	return fmt.Sprintf("%s.%s", pkg, name)
}
