//go:build codefly_nosemantic

package codeserver_test

import (
	"testing"

	"github.com/codefly-dev/core/code"
	"github.com/codefly-dev/core/code/codeserver"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestSemanticOptionsWithoutCGO(t *testing.T) {
	options := codeserver.SemanticOptions()
	if len(options) != 0 {
		t.Fatal("SemanticOptions installed an analyzer in a no-semantic build")
	}
	for _, server := range []*code.DefaultCodeServer{
		codeserver.New(t.TempDir()),
		code.NewDefaultCodeServer(t.TempDir(), options...),
	} {
		response, err := server.Execute(t.Context(), &codev0.CodeRequest{
			Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if response.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION {
			t.Fatalf("expected unsupported semantics, got %v", response)
		}
	}
}
