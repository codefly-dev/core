package modulecontract

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestEveryCustomDecoderReadsMappingsThroughTheOnePath holds the decoding to
// ONE path.
//
// A type with its own UnmarshalYAML does not get yaml's KnownFields, so each
// one re-implemented the same strictness: decode the key with its type rather
// than reading Node.Value, report an unknown field, keep a value that will not
// decode. Each was fixed separately, in a different decoder, a review round
// apart — an alias key accepted here, a tagged scalar accepted there, an
// unknown field ignored in a third.
//
// So a decoder that handles a MappingNode must read it with wire.ReadMapping.
// Adding a mapping branch that walks Content itself fails here, naming the
// function, rather than being found by a reviewer three rounds later.
func TestEveryCustomDecoderReadsMappingsThroughTheOnePath(t *testing.T) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	decoders := 0
	for _, pkg := range packages {
		for path, file := range pkg.Files {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name != "UnmarshalYAML" {
					continue
				}
				decoders++
				var body strings.Builder
				if err := printer.Fprint(&body, set, function.Body); err != nil {
					t.Fatal(err)
				}
				source := body.String()
				if !strings.Contains(source, "yaml.MappingNode") {
					continue
				}
				if !strings.Contains(source, "wire.ReadMapping") {
					t.Errorf("%s:%d: %s handles a mapping without wire.ReadMapping; a key's type, an unknown field and a malformed value are handled there, once, and a decoder that walks Content itself has to repeat all three",
						path, set.Position(function.Pos()).Line, receiverOf(function))
				}
			}
		}
	}
	if decoders == 0 {
		t.Fatal("no custom decoder found: the enumeration itself is broken")
	}
	t.Logf("%d custom decoders, every mapping read through wire.ReadMapping", decoders)
}

func receiverOf(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	switch receiver := function.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if name, ok := receiver.X.(*ast.Ident); ok {
			return name.Name + "." + function.Name.Name
		}
	case *ast.Ident:
		return receiver.Name + "." + function.Name.Name
	}
	return function.Name.Name
}
