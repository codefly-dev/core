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
//     deleted API, not a wrong one. This one is now PARTLY closed, one file
//     down: TestDocumentedCallsHaveTheRightArity checks that a documented
//     `pkg.Function(...)` has the arity the function takes, because this guard
//     passed `solutionhost.Activate(authority, presence, build)` for as long
//     as Activate existed under any signature. Argument types and order are
//     still unchecked.
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

// signatureOf is what the arity guard knows about one documented function:
// how many parameters it takes, whether the last is variadic, and whether the
// name resolves to more than one declaration — in which case it is skipped
// rather than guessed at.
type signatureOf struct {
	params    int
	variadic  bool
	ambiguous bool
}

// documentedCall is a `pkg.Function(...)` found in a fenced Go block.
type documentedCall struct {
	pkg      string
	function string
	args     int
}

// A documented CALL must have the arity the function actually takes.
//
// This closes the second of the three blind spots named above, and it is named
// there because it let a false statement through: after Activate changed to
// take a single ActivationRequest, docs/solution-host-binding.md went on
// showing `solutionhost.Activate(authority, presence, build)` through a green
// suite. The guard above resolves that name happily, because Activate still
// exists — it catches a DELETED api, not a wrong one. A reader copying the
// line gets code that does not compile; a reader reading it carefully
// concludes the three-argument shape is current, which for an authorization
// call means concluding the signer policy and the envelope are not arguments
// at all.
//
// It is deliberately narrow, because a Markdown block is not a Go file and a
// guard that tried to typecheck one would be wrong more often than the docs
// are:
//
//   - Only top-level FUNCTIONS of the known packages are checked. Methods are
//     skipped: `pkg.Value.Method(...)` cannot be resolved to one declaration
//     without type information.
//   - A name declared more than once with differing arity is skipped.
//   - Variadic functions are checked as a MINIMUM, never an exact count.
//   - Commas are counted at depth zero, so a composite literal or a nested
//     call spanning lines counts as the one argument it is.
//   - Only fenced go blocks are read. Prose naming a call in backticks is not
//     a line a reader copies.
//
// What it still does not check is whether the arguments are the right types or
// in the right order. That remains a thing only reading the diff catches.
func TestDocumentedCallsHaveTheRightArity(t *testing.T) {
	root := repoRoot(t)

	packages := map[string]string{
		"solutionhost": "solutionhost",
		"workcontext":  "workcontext",
		"conformance":  filepath.Join("workcontext", "conformance"),
		"readiness":    "readiness",
		"network":      "network",
	}
	signatures := map[string]map[string]signatureOf{}
	for name, directory := range packages {
		signatures[name] = functionSignatures(t, filepath.Join(root, directory))
	}

	documents := []string{
		filepath.Join("docs", "solution-host-binding.md"),
		filepath.Join("docs", "work-context.md"),
		filepath.Join("docs", "readiness.md"),
		filepath.Join("workcontext", "README.md"),
		filepath.Join("solutionhost", "testdata", "README.md"),
	}

	checked := 0
	for _, document := range documents {
		body, err := os.ReadFile(filepath.Join(root, document))
		require.NoError(t, err, "reading %s", document)

		for _, block := range goCodeBlocks(string(body)) {
			for _, call := range callsIn(block, signatures) {
				known := signatures[call.pkg][call.function]
				if known.ambiguous {
					continue
				}
				checked++
				if known.variadic {
					require.GreaterOrEqual(t, call.args, known.params-1,
						"%s documents %s.%s with %d arguments; it takes at least %d",
						document, call.pkg, call.function, call.args, known.params-1)
					continue
				}
				require.Equal(t, known.params, call.args,
					"%s documents %s.%s with %d arguments; it takes %d",
					document, call.pkg, call.function, call.args, known.params)
			}
		}
	}
	// A guard that silently matched nothing would pass forever.
	require.Positive(t, checked, "no documented calls resolved, so this guard checked nothing")
}

// functionSignatures maps each exported top-level function to its arity,
// marking a name declared more than once with differing arity as ambiguous
// rather than guessing which one a document meant.
func functionSignatures(t *testing.T, directory string) map[string]signatureOf {
	t.Helper()
	set := token.NewFileSet()
	parsed, err := parser.ParseDir(set, directory, func(file os.FileInfo) bool {
		return !strings.HasSuffix(file.Name(), "_test.go")
	}, 0)
	require.NoError(t, err, "parsing %s", directory)

	found := map[string]signatureOf{}
	for _, pkg := range parsed {
		for _, file := range pkg.Files {
			for _, declaration := range file.Decls {
				function, isFunction := declaration.(*ast.FuncDecl)
				if !isFunction || function.Recv != nil || !function.Name.IsExported() {
					continue
				}
				count, variadic := 0, false
				if function.Type.Params != nil {
					for _, field := range function.Type.Params.List {
						names := len(field.Names)
						if names == 0 {
							names = 1
						}
						count += names
						if _, isEllipsis := field.Type.(*ast.Ellipsis); isEllipsis {
							variadic = true
						}
					}
				}
				if existing, seen := found[function.Name.Name]; seen && existing.params != count {
					found[function.Name.Name] = signatureOf{ambiguous: true}
					continue
				}
				found[function.Name.Name] = signatureOf{params: count, variadic: variadic}
			}
		}
	}
	return found
}

// goCodeBlocks returns the contents of each fenced go block.
func goCodeBlocks(body string) []string {
	var blocks []string
	lines := strings.Split(body, "\n")
	for index := 0; index < len(lines); index++ {
		fence := strings.TrimSpace(lines[index])
		if fence != "```go" {
			continue
		}
		var block []string
		for index++; index < len(lines) && strings.TrimSpace(lines[index]) != "```"; index++ {
			block = append(block, lines[index])
		}
		blocks = append(blocks, strings.Join(block, "\n"))
	}
	return blocks
}

var documentedCallPattern = regexp.MustCompile(`\b([a-z][a-zA-Z0-9]*)\.([A-Z][A-Za-z0-9]*)\(`)

// callsIn finds each resolvable pkg.Function( call in a block and counts its
// top-level arguments. Depth-zero counting is what makes a composite-literal
// argument count as one.
func callsIn(block string, signatures map[string]map[string]signatureOf) []documentedCall {
	var calls []documentedCall
	for _, match := range documentedCallPattern.FindAllStringSubmatchIndex(block, -1) {
		pkg := block[match[2]:match[3]]
		function := block[match[4]:match[5]]
		known, isKnown := signatures[pkg]
		if !isKnown {
			continue
		}
		if _, declared := known[function]; !declared {
			continue
		}
		// A method call on a value of the same name — pkg.Thing.Method( — is
		// not this function being called.
		if openParen := match[1]; openParen > 0 && block[match[0]] == '.' {
			continue
		}
		args, closed := countArguments(block[match[1]:])
		if !closed {
			continue
		}
		calls = append(calls, documentedCall{pkg: pkg, function: function, args: args})
	}
	return calls
}

// countArguments counts commas at depth zero after an opening paren, and
// reports whether the call was closed within the block. An unclosed call is
// skipped rather than counted: an elided example is not a wrong one.
func countArguments(after string) (int, bool) {
	depth, args, seen := 1, 1, false
	for _, character := range after {
		switch character {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
			if depth == 0 {
				if !seen {
					return 0, true
				}
				return args, true
			}
		case ',':
			if depth == 1 {
				args++
			}
		default:
			if depth == 1 && character != ' ' && character != '\n' && character != '\t' {
				seen = true
			}
		}
	}
	return 0, false
}
