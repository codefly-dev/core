// Package conditions enumerates a wire package's refusal CONDITIONS from its
// own source, so "every condition is protected by a fixture" is a property
// checked against the code rather than against a list someone maintains.
//
// Eight review rounds found the same shape of defect eight times: a rule had a
// fixture, and a CONDITION inside that rule did not. A Kubernetes length
// bound, an action list reached by one spelling but not the other, one
// vocabulary value out of two, a control character that was also whitespace, a
// null as a key but not in a list, an identical CIDR pair where the fixtures
// nested their ranges. Each was added by hand and the next round found the
// next one, because nothing could see the difference between a condition with
// a counterexample and a condition without one.
//
// A refusal site — one fmt.Errorf carrying the package's sentinel — is exactly
// one condition. Enumerating them from the AST means a condition added to the
// code without a fixture that reaches it is a test failure at the line that
// added it, and no reviewer has to find it.
package conditions

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A Site is one refusal in the source: where it is, and the longest literal
// run of its message, which is what a refusal naming it must contain.
type Site struct {
	File    string
	Line    int
	Literal string
	// Func is the function the refusal is written in. It is what makes
	// "one rule holds one condition" answerable: a rule is a function, so
	// two sites in one function are two conditions sharing one witness,
	// which is how a weakened predicate kept a green scan while its
	// sibling's fixture went on refusing.
	Func string
}

// Sites returns every refusal site in dir's non-test Go files whose
// fmt.Errorf wraps one of the named sentinels.
func Sites(dir string, sentinels ...string) ([]Site, error) {
	set := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*ast.File, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(set, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		files[name] = parsed
	}
	wanted := make(map[string]bool, len(sentinels))
	for _, sentinel := range sentinels {
		wanted[sentinel] = true
	}
	var sites []Site
	for path, file := range files {
		// Walked declaration by declaration, so every site carries the
		// function it is written in. ast.Inspect reports the end of EVERY
		// node, not only of a declaration, so a stack popped on nil empties
		// itself at the first leaf and names nothing.
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(function, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 || !isErrorf(call.Fun) {
					return true
				}
				format, ok := stringLiteral(call.Args[0])
				if !ok || !strings.Contains(format, "%w") {
					return true
				}
				if name, ok := call.Args[1].(*ast.Ident); !ok || !wanted[name.Name] {
					return true
				}
				sites = append(sites, Site{
					File:    filepath.Base(path),
					Line:    set.Position(call.Pos()).Line,
					Literal: longestRun(format),
					Func:    function.Name.Name,
				})
				return true
			})
		}
	}
	return sites, nil
}

func isErrorf(fun ast.Expr) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Errorf" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "fmt"
}

// stringLiteral is the text of a string expression, INCLUDING one written as
// a concatenation. A refusal whose message is spelled across two lines --
// "%w: ... " + "add the slot, or ..." -- is one condition like any other, and
// while this read only a single literal every such refusal was invisible to
// the enumeration: not a condition without a witness, but a condition the
// check for witnesses could not see at all. The one in this repository was
// found by the rule-to-condition pairing, not by review.
func stringLiteral(expr ast.Expr) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(node.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, ok := stringLiteral(node.X)
		if !ok {
			return "", false
		}
		right, ok := stringLiteral(node.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return stringLiteral(node.X)
	}
	return "", false
}

// longestRun is the longest stretch of a format string carrying no verb, with
// the sentinel's own "%w: " prefix dropped. It is what a message naming this
// condition must contain, and being the LONGEST run it is the most
// distinguishing one — two conditions of the same rule differ somewhere in
// their text, or they are one condition.
func longestRun(format string) string {
	format = strings.TrimPrefix(format, "%w: ")
	var longest string
	for index, run := range strings.Split(format, "%") {
		// Every segment but the FIRST follows a "%", so its leading
		// character is the verb and is dropped. The first segment follows
		// nothing, and stripping a leading "s", "w", "d", "q" or "v" from it
		// ate the first letter of any message beginning with one -- "written
		// ..." was enumerated as "ritten ...". Harmless to a substring match,
		// which is why it survived; wrong in the text a failure prints, and
		// wrong in what "this literal identifies this condition" means.
		if index > 0 && len(run) > 0 && strings.IndexAny(run[:1], "swdqv") == 0 {
			run = run[1:]
		}
		if trimmed := strings.TrimSpace(run); len(trimmed) > len(longest) {
			longest = trimmed
		}
	}
	return longest
}
