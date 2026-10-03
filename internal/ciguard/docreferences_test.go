package ciguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Prose that names a Go identifier must name one that exists.
//
// This guard exists because of a specific escape. A review found that the
// documented way for a host to remove a binding was unsound — it told the host
// to treat absence as removal — and the function it named was deleted. The
// prose telling hosts to call it was not, so `docs/solution-host-binding.md`
// went on instructing consumers to call `solutionhost.ByDomain(docs...)`, a
// function that no longer existed, through a green suite: compilation does not
// read Markdown, and nothing else did either.
//
// That is the shape worth catching rather than the one instance. A document
// that names a deleted API is not merely stale: a consumer reading it writes
// code that does not build, and a consumer reading it CAREFULLY concludes the
// API is there and the prose around it is current — which is how a withdrawn
// rule gets re-implemented by hand.
//
// WHAT IT CHECKS, stated narrowly because a reviewer was right that the first
// version of this comment claimed more than the code does: every
// `package.Identifier` in the documents below, for the packages it knows how
// to resolve, must be a name that package exports.
//
// THREE THINGS IT DOES NOT CHECK, each of which has let a false statement
// through:
//
//   - It does not read Go doc comments, so a comment in the SOURCE that
//     describes a mechanism which does not exist passes it. That is not
//     hypothetical: `AuthorityDocument.Generation` carried "a replayed older
//     document is detectable the same way a replayed presence generation is"
//     while nothing detected it, and `OwnershipDomain` claimed `Host.Admit`
//     refused a straddling set after that refusal had been removed. Both were
//     found by human review, not here.
//   - Any exported METHOD or FIELD name satisfies `pkg.X`, so `pkg.Anything`
//     resolves if some unrelated type has a field of that name. It catches a
//     deleted API, not a wrong one.
//   - It says nothing about whether the prose around the name is true.
//
// So this guard's whole value is one narrow thing: a document cannot go on
// naming an API that no longer exists. That is worth having — it is the escape
// this was written for — and it is not a substitute for reading the diff.
func TestDocumentsNameIdentifiersThatExist(t *testing.T) {
	root := repoRoot(t)

	// The packages whose surface the documentation describes in detail, and
	// which are therefore worth resolving. A package absent from here is not
	// checked, so adding a doc for a new package means adding it.
	packages := map[string]string{
		"solutionhost": "solutionhost",
		"workcontext":  "workcontext",
		"conformance":  filepath.Join("workcontext", "conformance"),
		"readiness":    "readiness",
		"network":      "network",
	}
	exported := map[string]map[string]bool{}
	for name, directory := range packages {
		exported[name] = exportedNames(t, filepath.Join(root, directory))
	}

	documents := []string{
		filepath.Join("docs", "solution-host-binding.md"),
		filepath.Join("docs", "work-context.md"),
		filepath.Join("docs", "readiness.md"),
		filepath.Join("workcontext", "README.md"),
		filepath.Join("solutionhost", "testdata", "README.md"),
	}
	// `package.Identifier` where Identifier starts with a capital. Reached
	// through a code span or bare prose alike; the pattern does not care.
	reference := regexp.MustCompile(`\b(` + strings.Join(sortedKeys(packages), "|") + `)\.([A-Z][A-Za-z0-9_]*)`)

	for _, document := range documents {
		path := filepath.Join(root, document)
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			// A document this guard names must exist, or the guard is
			// silently covering nothing.
			t.Errorf("%s: this guard names a document that is not there; update the list or restore the file", document)
			continue
		}
		require.NoError(t, err)
		seen := map[string]bool{}
		for _, match := range reference.FindAllStringSubmatch(string(body), -1) {
			pkg, identifier := match[1], match[2]
			if seen[pkg+"."+identifier] {
				continue
			}
			seen[pkg+"."+identifier] = true
			require.True(t, exported[pkg][identifier],
				"%s names %s.%s, which package %s does not export. A document that points at a deleted API tells a "+
					"consumer to write code that does not build, and tells a careful one that the prose around it is current.",
				document, pkg, identifier, pkg)
		}
	}
}

// exportedNames collects every exported top-level declaration of a package,
// including methods — a method is reached as package.Type.Method in prose, and
// the regexp above sees its type name, so types and methods both matter.
func exportedNames(t *testing.T, directory string) map[string]bool {
	t.Helper()
	set := token.NewFileSet()
	parsed, err := parser.ParseDir(set, directory, func(file os.FileInfo) bool {
		return !strings.HasSuffix(file.Name(), "_test.go")
	}, 0)
	require.NoError(t, err, "parsing %s", directory)

	names := map[string]bool{}
	for _, pkg := range parsed {
		for _, file := range pkg.Files {
			for _, declaration := range file.Decls {
				switch typed := declaration.(type) {
				case *ast.FuncDecl:
					if typed.Name.IsExported() {
						names[typed.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range typed.Specs {
						switch named := spec.(type) {
						case *ast.TypeSpec:
							if named.Name.IsExported() {
								names[named.Name.Name] = true
								collectFieldNames(named.Type, names)
							}
						case *ast.ValueSpec:
							for _, name := range named.Names {
								if name.IsExported() {
									names[name.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	return names
}

// collectFieldNames adds a struct's exported field names, because prose
// describes a field as package.Type.Field and the regexp sees the type.
func collectFieldNames(expression ast.Expr, into map[string]bool) {
	structure, isStruct := expression.(*ast.StructType)
	if !isStruct || structure.Fields == nil {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.IsExported() {
				into[name.Name] = true
			}
		}
	}
}

func sortedKeys(from map[string]string) []string {
	keys := make([]string, 0, len(from))
	for key := range from {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
