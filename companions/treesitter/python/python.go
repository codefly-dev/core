// Package python registers the Python tree-sitter grammar with the treesitter
// registry. Import for side effect:
//
//	_ "github.com/codefly-dev/core/companions/treesitter/python"
//
// ARCHITECTURE: Thin language adapter. Everything else (parsing, caching,
// workspace walking, diagnostics, resolve) is language-agnostic in the parent.
package python

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tspy "github.com/smacker/go-tree-sitter/python"

	"github.com/codefly-dev/core/companions/treesitter"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/languages"
)

func init() {
	treesitter.Register(languages.PYTHON, &treesitter.LanguageConfig{
		LanguageID:     "python",
		FileExtensions: []string{".py"},
		SkipDirs:       []string{".venv", "venv", "__pycache__", "node_modules", ".git", "build", "dist"},
		SkipSuffixes:   []string{"_test.py", "test_.py"},
		Grammar:        tspy.GetLanguage,
		ExtractSymbols: extractSymbols,
	})
}

// extractSymbols walks a parsed Python file and returns top-level symbols.
// Class methods appear as children of their enclosing class.
func extractSymbols(tree *sitter.Tree, content []byte, relPath string) []*codev0.Symbol {
	root := tree.RootNode()
	module := modulePath(relPath)

	var symbols []*codev0.Symbol
	count := int(root.NamedChildCount())
	for i := 0; i < count; i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "function_definition", "async_function_definition":
			if s := funcDef(child, content, relPath, module, ""); s != nil {
				symbols = append(symbols, s)
			}
		case "decorated_definition":
			if inner := child.ChildByFieldName("definition"); inner != nil {
				switch inner.Type() {
				case "function_definition", "async_function_definition":
					if s := funcDef(inner, content, relPath, module, ""); s != nil {
						// Check for @dataclass-style decorators and tag.
						if hasDecoratorName(child, content, "dataclass") {
							s.Kind = codev0.SymbolKind_SYMBOL_KIND_CLASS
						}
						symbols = append(symbols, s)
					}
				case "class_definition":
					if s := classDef(inner, content, relPath, module); s != nil {
						symbols = append(symbols, s)
					}
				}
			}
		case "class_definition":
			if s := classDef(child, content, relPath, module); s != nil {
				symbols = append(symbols, s)
			}
		case "expression_statement":
			// Module-level constant assignments become VARIABLE symbols.
			// Handles plain `X = ...`, tuple `a, b = 1, 2`, and type-annotated.
			symbols = append(symbols, assignedNames(child, content, relPath, module)...)
		case "type_alias_statement":
			// Python 3.12+ `type Alias = ...`.
			if s := typeAlias(child, content, relPath, module); s != nil {
				symbols = append(symbols, s)
			}
		}
	}
	return symbols
}

func funcDef(n *sitter.Node, content []byte, file, module, parent string) *codev0.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := textOf(nameNode, content)
	kind := codev0.SymbolKind_SYMBOL_KIND_FUNCTION
	if parent != "" {
		kind = codev0.SymbolKind_SYMBOL_KIND_METHOD
	}
	return &codev0.Symbol{
		Name:          name,
		Kind:          kind,
		Location:      rangeToLocation(n, file),
		Signature:     signatureLine(n, content),
		Parent:        parent,
		QualifiedName: qualify(module, parent, name),
	}
}

func classDef(n *sitter.Node, content []byte, file, module string) *codev0.Symbol {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := textOf(nameNode, content)
	var children []*codev0.Symbol

	body := n.ChildByFieldName("body")
	if body != nil {
		bc := int(body.NamedChildCount())
		for i := 0; i < bc; i++ {
			c := body.NamedChild(i)
			if c == nil {
				continue
			}
			switch c.Type() {
			case "function_definition":
				if s := funcDef(c, content, file, module, name); s != nil {
					children = append(children, s)
				}
			case "decorated_definition":
				if inner := c.ChildByFieldName("definition"); inner != nil && inner.Type() == "function_definition" {
					if s := funcDef(inner, content, file, module, name); s != nil {
						children = append(children, s)
					}
				}
			}
		}
	}

	return &codev0.Symbol{
		Name:          name,
		Kind:          codev0.SymbolKind_SYMBOL_KIND_CLASS,
		Location:      rangeToLocation(n, file),
		Signature:     firstLine(textOf(n, content)),
		Children:      children,
		QualifiedName: qualify(module, "", name),
	}
}

// assignedNames extracts names from a module-level assignment statement.
// Handles:
//   - Plain:            X = value
//   - Type-annotated:   X: T = value  (node type: assignment with left=identifier)
//   - Tuple:            a, b = 1, 2   (left is a `pattern_list` or `tuple_pattern`)
func assignedNames(stmt *sitter.Node, content []byte, file, module string) []*codev0.Symbol {
	var out []*codev0.Symbol
	nc := int(stmt.NamedChildCount())
	for i := 0; i < nc; i++ {
		c := stmt.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Type() != "assignment" {
			continue
		}
		target := c.ChildByFieldName("left")
		if target == nil {
			continue
		}
		out = append(out, extractAssignTargets(target, c, content, file, module)...)
	}
	return out
}

// extractAssignTargets walks an lhs pattern and emits a VARIABLE symbol
// for each identifier it finds.
func extractAssignTargets(lhs, stmt *sitter.Node, content []byte, file, module string) []*codev0.Symbol {
	switch lhs.Type() {
	case "identifier":
		name := textOf(lhs, content)
		return []*codev0.Symbol{{
			Name:          name,
			Kind:          codev0.SymbolKind_SYMBOL_KIND_VARIABLE,
			Location:      rangeToLocation(lhs, file),
			Signature:     firstLine(textOf(stmt, content)),
			QualifiedName: qualify(module, "", name),
		}}
	case "pattern_list", "tuple_pattern", "list_pattern":
		var out []*codev0.Symbol
		nc := int(lhs.NamedChildCount())
		for i := 0; i < nc; i++ {
			c := lhs.NamedChild(i)
			if c == nil {
				continue
			}
			out = append(out, extractAssignTargets(c, stmt, content, file, module)...)
		}
		return out
	}
	return nil
}

// typeAlias handles Python 3.12+ `type Alias = T` statements.
func typeAlias(n *sitter.Node, content []byte, file, module string) *codev0.Symbol {
	// Shape: `type` IDENTIFIER `=` <type>. Find the identifier child.
	nc := int(n.NamedChildCount())
	for i := 0; i < nc; i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Type() == "type" || c.Type() == "identifier" {
			name := textOf(c, content)
			if name == "" || name == "type" {
				continue
			}
			return &codev0.Symbol{
				Name:          name,
				Kind:          codev0.SymbolKind_SYMBOL_KIND_TYPE_ALIAS,
				Location:      rangeToLocation(c, file),
				Signature:     firstLine(textOf(n, content)),
				QualifiedName: qualify(module, "", name),
			}
		}
	}
	return nil
}

// hasDecoratorName returns true if a decorated_definition has a decorator
// whose root identifier is `name` (or ends in `.name`).
func hasDecoratorName(dec *sitter.Node, content []byte, name string) bool {
	nc := int(dec.NamedChildCount())
	for i := 0; i < nc; i++ {
		c := dec.NamedChild(i)
		if c == nil || c.Type() != "decorator" {
			continue
		}
		// Decorator content is `@X` or `@X.Y(...)`.
		txt := strings.TrimPrefix(textOf(c, content), "@")
		// Strip args.
		if idx := strings.IndexByte(txt, '('); idx >= 0 {
			txt = txt[:idx]
		}
		// Check bare name or trailing `.name`.
		if txt == name || strings.HasSuffix(txt, "."+name) {
			return true
		}
	}
	return false
}

// signatureLine returns the def/class header up to the body (":" delimited).
func signatureLine(n *sitter.Node, content []byte) string {
	body := n.ChildByFieldName("body")
	if body == nil {
		return firstLine(textOf(n, content))
	}
	start := int(n.StartByte())
	end := int(body.StartByte())
	if end <= start || end > len(content) {
		return firstLine(textOf(n, content))
	}
	return firstLine(strings.TrimRight(string(content[start:end]), " \t\n:"))
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

// modulePath turns a relative file path into a Python module path:
//
//	"pkg/auth/login.py" -> "pkg.auth.login"
//	"foo.py"           -> "foo"
func modulePath(rel string) string {
	rel = strings.TrimSuffix(rel, ".py")
	rel = strings.TrimSuffix(rel, "/__init__")
	return strings.ReplaceAll(rel, "/", ".")
}

func qualify(module, parent, name string) string {
	if module == "" {
		if parent != "" {
			return fmt.Sprintf("%s.%s", parent, name)
		}
		return name
	}
	if parent != "" {
		return fmt.Sprintf("%s.%s.%s", module, parent, name)
	}
	return fmt.Sprintf("%s.%s", module, name)
}
