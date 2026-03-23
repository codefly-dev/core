package code

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// ParseGoTree parses a Go source tree and builds a CodeGraph using the local
// filesystem. For VFS-backed parsing, use ParseGoTreeVFS.
func ParseGoTree(dir string) (*CodeGraph, error) {
	return ParseGoTreeVFS(LocalVFS{}, dir)
}

// ParseGoTreeVFS parses a Go source tree through a VFS and builds a CodeGraph.
// It extracts functions, methods, types, file nodes, imports, and call edges.
func ParseGoTreeVFS(vfs VFS, dir string) (*CodeGraph, error) {
	g := NewCodeGraph()
	fset := token.NewFileSet()

	err := vfs.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		_ = parseGoFile(g, fset, vfs, dir, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}

	resolveGoCallEdges(g)
	return g, nil
}

// parseGoFile parses a single Go source file into the graph.
func parseGoFile(g *CodeGraph, fset *token.FileSet, vfs VFS, root, rel string) error {
	absPath := filepath.Join(root, rel)
	src, err := vfs.ReadFile(absPath)
	if err != nil {
		return err
	}

	f, err := parser.ParseFile(fset, absPath, src, parser.ParseComments)
	if err != nil {
		return err
	}

	pkgName := ""
	if f.Name != nil {
		pkgName = f.Name.Name
	}

	fileNode := &CodeNode{
		ID:      rel,
		Kind:    NodeFile,
		Name:    filepath.Base(rel),
		File:    rel,
		Line:    1,
		Body:    goTruncate(string(src), 4000),
		Imports: goExtractImports(f),
	}
	g.AddNode(fileNode)

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			goAddFunc(g, fset, src, rel, pkgName, d, fileNode)
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						goAddType(g, fset, src, rel, ts, d, fileNode)
					}
				}
			}
		}
	}
	return nil
}

func goAddFunc(g *CodeGraph, fset *token.FileSet, src []byte, file, pkg string, d *ast.FuncDecl, fileNode *CodeNode) {
	name := d.Name.Name
	kind := NodeFunction
	receiver := ""

	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = NodeMethod
		receiver = goExprStr(d.Recv.List[0].Type)
	}

	id := goNodeID(file, receiver, name)
	pos := fset.Position(d.Pos())
	end := fset.Position(d.End())

	node := &CodeNode{
		ID:        id,
		Kind:      kind,
		Name:      name,
		File:      file,
		Line:      pos.Line,
		EndLine:   end.Line,
		Signature: goFuncSig(fset, d),
		Doc:       goDocText(d.Doc),
		Body:      goTruncate(goSourceRange(src, fset, d.Pos(), d.End()), 3000),
		Calls:     goExtractCalls(d),
	}
	g.AddNode(node)

	fileNode.Children = append(fileNode.Children, id)
	g.AddEdge(Edge{From: fileNode.ID, To: id, Kind: EdgeContains})
}

func goAddType(g *CodeGraph, fset *token.FileSet, src []byte, file string, ts *ast.TypeSpec, gd *ast.GenDecl, fileNode *CodeNode) {
	name := ts.Name.Name
	id := goNodeID(file, "", name)
	pos := fset.Position(ts.Pos())
	end := fset.Position(gd.End())

	sig := "type " + name
	switch ts.Type.(type) {
	case *ast.StructType:
		sig += " struct"
	case *ast.InterfaceType:
		sig += " interface"
	}

	node := &CodeNode{
		ID:        id,
		Kind:      NodeType,
		Name:      name,
		File:      file,
		Line:      pos.Line,
		EndLine:   end.Line,
		Signature: sig,
		Doc:       goDocText(gd.Doc),
		Body:      goTruncate(goSourceRange(src, fset, gd.Pos(), gd.End()), 3000),
	}
	g.AddNode(node)

	fileNode.Children = append(fileNode.Children, id)
	g.AddEdge(Edge{From: fileNode.ID, To: id, Kind: EdgeContains})
}

// goExtractCalls walks a function body and collects called function names.
func goExtractCalls(d *ast.FuncDecl) []string {
	if d.Body == nil {
		return nil
	}
	seen := make(map[string]bool)
	var calls []string

	ast.Inspect(d.Body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fn := ce.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			if ident, ok := fn.X.(*ast.Ident); ok {
				name = ident.Name + "." + fn.Sel.Name
			}
		}
		if name != "" && !seen[name] {
			seen[name] = true
			calls = append(calls, name)
		}
		return true
	})
	return calls
}

// resolveGoCallEdges creates EdgeCalls between nodes by matching identifiers.
func resolveGoCallEdges(g *CodeGraph) {
	lookup := make(map[string]string)
	for id, n := range g.Nodes {
		if n.Kind == NodeFunction || n.Kind == NodeMethod {
			lookup[n.Name] = id
			dir := filepath.Dir(n.File)
			pkg := filepath.Base(dir)
			if pkg != "." && pkg != "" {
				lookup[pkg+"."+n.Name] = id
			}
		}
	}

	for _, n := range g.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		for _, callName := range n.Calls {
			if targetID, ok := lookup[callName]; ok && targetID != n.ID {
				g.AddEdge(Edge{From: n.ID, To: targetID, Kind: EdgeCalls})
			}
		}
	}

	// Rebuild CalledBy from the callers index.
	for id := range g.Nodes {
		if callers := g.GetCallers(id); len(callers) > 0 {
			g.Nodes[id].CalledBy = callers
		}
	}
}

// ─── Go AST helpers ──────────────────────────────────────────

func goNodeID(file, receiver, name string) string {
	if receiver != "" {
		return fmt.Sprintf("%s::%s.%s", file, receiver, name)
	}
	return fmt.Sprintf("%s::%s", file, name)
}

func goFuncSig(fset *token.FileSet, d *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if d.Recv != nil && len(d.Recv.List) > 0 {
		b.WriteString("(")
		b.WriteString(goExprStr(d.Recv.List[0].Type))
		b.WriteString(") ")
	}
	b.WriteString(d.Name.Name)

	if d.Type.Params != nil {
		b.WriteString("(")
		goFieldList(&b, d.Type.Params)
		b.WriteString(")")
	} else {
		b.WriteString("()")
	}
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		b.WriteString(" ")
		if len(d.Type.Results.List) == 1 && len(d.Type.Results.List[0].Names) == 0 {
			b.WriteString(goExprStr(d.Type.Results.List[0].Type))
		} else {
			b.WriteString("(")
			goFieldList(&b, d.Type.Results)
			b.WriteString(")")
		}
	}
	return b.String()
}

func goFieldList(b *strings.Builder, fl *ast.FieldList) {
	for i, f := range fl.List {
		if i > 0 {
			b.WriteString(", ")
		}
		for j, name := range f.Names {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(name.Name)
		}
		if len(f.Names) > 0 {
			b.WriteString(" ")
		}
		b.WriteString(goExprStr(f.Type))
	}
}

func goExprStr(e ast.Expr) string {
	var b strings.Builder
	printer.Fprint(&b, token.NewFileSet(), e)
	return b.String()
}

func goDocText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return strings.TrimSpace(cg.Text())
}

func goExtractImports(f *ast.File) []string {
	var imports []string
	for _, imp := range f.Imports {
		imports = append(imports, strings.Trim(imp.Path.Value, `"`))
	}
	return imports
}

func goSourceRange(src []byte, fset *token.FileSet, start, end token.Pos) string {
	s := fset.Position(start).Offset
	e := fset.Position(end).Offset
	if s < 0 || e > len(src) || s >= e {
		return ""
	}
	return string(src[s:e])
}

func goTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n// ... truncated ..."
}
