//go:build !codefly_nosemantic

package codeserver_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/code/codeserver"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestNewInstallsSemanticAnalyzer verifies the default (CGO) build wires the
// tree-sitter analyzer into the server: a non-Go source tree produces a complete
// semantic index instead of the unsupported-operation failure a plain
// DefaultCodeServer returns. The codefly_nosemantic variant is exercised by the
// CGO-free build guard (scripts/check_cgo_free.sh), not here.
func TestNewInstallsSemanticAnalyzer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("import requests\nclass Client: pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	response, err := codeserver.New(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	index := response.GetGetSemanticIndex()
	if index == nil || index.GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_COMPLETE {
		encoded, _ := protojson.Marshal(index)
		t.Fatalf("semantic index = %s, failure = %#v", encoded, response.GetFailure())
	}
}
