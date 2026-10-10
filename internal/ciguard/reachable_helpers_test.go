package ciguard

import (
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Reachability starts at test entry points and init, and follows resolved Go
// objects through declarations. Dead cycles do not create roots; a local or
// field with the same spelling is not a use of a package helper. Methods of a
// reached type are conservatively reached too, for interface dispatch.
func TestEveryHelperInThisPackageIsReachable(t *testing.T) {
	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "internal/ciguard/*.go"))
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []*ast.File
	imports := map[string]bool{}
	for _, source := range sources {
		file, err := parser.ParseFile(fset, source, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		files = append(files, file)
		for _, spec := range file.Imports {
			imports[strings.Trim(spec.Path.Value, `"`)] = true
		}
	}
	// Ask Go for the actual module-selected export data; importer.Default alone
	// cannot resolve modules. This compiles dependencies, never runs another test.
	args := []string{"list", "-deps", "-export", "-json"}
	for path := range imports {
		args = append(args, path)
	}
	command := exec.Command("go", args...)
	command.Dir = repoRoot(t)
	output, err := command.Output()
	require.NoError(t, err)
	exports := map[string]string{}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var pkg struct{ ImportPath, Export string }
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		exports[pkg.ImportPath] = pkg.Export
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
	unreached := unreachableDeclarations(t, fset, files, imp)
	require.Empty(t, unreached, "unreachable ciguard declarations: %s; make them load-bearing or remove them and their promises", strings.Join(unreached, ", "))
}

func unreachableDeclarations(t *testing.T, fset *token.FileSet, files []*ast.File, imp types.Importer) []string {
	t.Helper()
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check("ciguard", fset, files, info)
	require.NoError(t, err)
	nodes := map[types.Object]ast.Node{}
	report := map[types.Object]bool{}
	roots := map[types.Object]bool{}
	methods := map[types.Object][]types.Object{}
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				obj := info.Defs[d.Name]
				nodes[obj] = d
				if d.Recv == nil {
					if isTestEntryPoint(d.Name.Name) || d.Name.Name == "init" {
						roots[obj] = true
					} else {
						report[obj] = true
					}
				} else {
					recv := obj.Type().(*types.Signature).Recv().Type()
					if pointer, ok := recv.(*types.Pointer); ok {
						recv = pointer.Elem()
					}
					if named, ok := recv.(*types.Named); ok {
						methods[named.Obj()] = append(methods[named.Obj()], obj)
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch value := spec.(type) {
					case *ast.ValueSpec:
						for _, name := range value.Names {
							if name.Name != "_" {
								obj := info.Defs[name]
								nodes[obj] = value
								report[obj] = true
							}
						}
					case *ast.TypeSpec:
						nodes[info.Defs[value.Name]] = value
					}
				}
			}
		}
	}
	edges := map[types.Object][]types.Object{}
	for obj, node := range nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				if used := info.Uses[ident]; used != nil && used.Pkg() == pkg {
					edges[obj] = append(edges[obj], used)
				}
			}
			return true
		})
		edges[obj] = append(edges[obj], methods[obj]...)
	}
	var visit func(types.Object)
	seen := map[types.Object]bool{}
	visit = func(obj types.Object) {
		if seen[obj] {
			return
		}
		seen[obj] = true
		for _, dep := range edges[obj] {
			visit(dep)
		}
	}
	for root := range roots {
		visit(root)
	}
	var dead []string
	for obj := range report {
		if !seen[obj] {
			dead = append(dead, obj.Name())
		}
	}
	sort.Strings(dead)
	return dead
}

func isTestEntryPoint(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func TestReachabilityDoesNotCountDeadCyclesOrShadowedNames(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", `package probe
func deadA(){deadB()}
func deadB(){deadA(); deadLeaf()}
func deadLeaf(){}
func shadowed(){}
func fieldOnly(){}
var deadValue = deadA
var liveValue = live
func live(){}
func TestRoot(){shadowed:=1;_ = shadowed; s:=struct{fieldOnly int}{fieldOnly:2};_ = s.fieldOnly;liveValue()}
`, parser.SkipObjectResolution)
	require.NoError(t, err)
	require.Equal(t, []string{"deadA", "deadB", "deadLeaf", "deadValue", "fieldOnly", "shadowed"}, unreachableDeclarations(t, fset, []*ast.File{file}, nil))
}
