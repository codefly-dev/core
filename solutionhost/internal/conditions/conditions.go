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
		ast.Inspect(file, func(node ast.Node) bool {
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
			})
			return true
		})
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

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

// longestRun is the longest stretch of a format string carrying no verb, with
// the sentinel's own "%w: " prefix dropped. It is what a message naming this
// condition must contain, and being the LONGEST run it is the most
// distinguishing one — two conditions of the same rule differ somewhere in
// their text, or they are one condition.
func longestRun(format string) string {
	format = strings.TrimPrefix(format, "%w: ")
	var longest string
	for _, run := range strings.Split(format, "%") {
		if index := strings.IndexAny(run, "swdqv"); index == 0 && len(run) > 0 {
			run = run[1:]
		}
		if trimmed := strings.TrimSpace(run); len(trimmed) > len(longest) {
			longest = trimmed
		}
	}
	return longest
}
