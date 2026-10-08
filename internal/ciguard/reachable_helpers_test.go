package ciguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every helper in this package must be reached by something.
//
// This exists because of a specific, expensive failure, and the failure was not
// in the guards -- it was in the comments around them. When the credential
// decision was replaced by a file digest, the machinery it had replaced was left
// in the tree with its explanations intact: `cannotRunOnAPullRequest`,
// `mustNotRunUnder`, `provablyUnreachable`, `reachabilityNote`,
// `reachableFromAPullRequest` and `reachableOnATagPush` all had ZERO callers,
// while the file headers, the rule statements and
// `docs/ci-credentials.md` went on describing reachability as the thing being
// evaluated. A reader -- including a reviewer reading carefully -- concluded the
// evaluation was happening. It was not. The whole verdict was a constant.
//
// Go does not report an uncalled package-level function, and `golangci-lint` is
// commented out in `go.yml`, so nothing reported any of it for a release. This
// does, under `Build`, which the `protect main` ruleset actually requires --
// which the lint job never was.
//
// WHAT IT CHECKS, narrowly: every function and package-level variable declared
// in this package appears as an identifier somewhere other than its own
// declaration. That is a weak sufficient condition and it is deliberately the
// weak direction: it over-counts uses (a struct field or a map key of the same
// name satisfies it), so it can miss dead code, but it cannot fail on code that
// is live. It would have caught all six functions above, which is what it is
// for.
//
// WHAT IT DOES NOT CHECK: whether a caller is itself reached, whether the thing
// a comment describes is what the code does, or whether a reference is real
// rather than a `_ = helper()` written to silence a linter. The last one was
// here too -- `sortedTemplateNames` was kept alive by exactly that line, with a
// comment claiming it was "used by the message above". It is now actually used
// by that message.
func TestEveryHelperInThisPackageIsReachable(t *testing.T) {
	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "ciguard", "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, sources, "no source read, so this guard proves nothing")

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(sources))
	for _, source := range sources {
		parsed, err := parser.ParseFile(fset, source, nil, parser.SkipObjectResolution)
		require.NoError(t, err, source)
		files = append(files, parsed)
	}

	// Declared: every plain function, and every package-level var or const.
	// Methods are left out -- a method can be reached through an interface this
	// walk does not resolve, so naming one dead would be a false failure.
	declared := map[string]token.Pos{}
	declaringIdent := map[ast.Node]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			switch node := decl.(type) {
			case *ast.FuncDecl:
				if node.Recv != nil || isTestEntryPoint(node.Name.Name) {
					continue
				}
				declared[node.Name.Name] = node.Name.Pos()
				declaringIdent[node.Name] = true
			case *ast.GenDecl:
				if node.Tok != token.VAR && node.Tok != token.CONST {
					continue
				}
				for _, spec := range node.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range value.Names {
						if name.Name == "_" {
							continue
						}
						declared[name.Name] = name.Pos()
						declaringIdent[name] = true
					}
				}
			}
		}
	}
	require.NotEmpty(t, declared, "no declaration found, so this guard proves nothing")

	used := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok || declaringIdent[ident] {
				return true
			}
			used[ident.Name] = true
			return true
		})
	}

	unreached := make([]string, 0)
	for name := range declared {
		if !used[name] {
			unreached = append(unreached, name)
		}
	}
	sort.Strings(unreached)
	require.Empty(t, unreached,
		"these are declared in internal/ciguard and reached by nothing: %s.\n\n"+
			"Either make each one load-bearing or delete it TOGETHER WITH the comments "+
			"that describe it. An uncalled helper beside a comment explaining what it "+
			"enforces is how this package's credential decision became a constant for "+
			"a release while six functions and three file headers said it was an "+
			"evaluation.", strings.Join(unreached, ", "))
}

// isTestEntryPoint reports whether a function is one `go test` calls itself,
// and which therefore needs no caller in the package.
func isTestEntryPoint(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return name == "TestMain"
}
