package golang

import (
	"reflect"
	"testing"

	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
)

func TestRenderTestSelectionOwnsGoSelectorGrammar(t *testing.T) {
	selection := &runtimev1.TestSelection{Scope: &runtimev1.TestSelection_TestCase{TestCase: &runtimev1.TestCaseSelection{
		Package:       "./pkg/widgets",
		Path:          "pkg/widgets/widget_test.go",
		QualifiedName: []string{"TestWidget", "empty input"},
	}}}
	got, err := RenderTestSelection(selection)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./pkg/widgets", "TestWidget/empty input"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectors = %#v, want %#v", got, want)
	}
}

func TestRenderTestSelectionRejectsUnsupportedGoScope(t *testing.T) {
	selection := &runtimev1.TestSelection{Scope: &runtimev1.TestSelection_File{File: &runtimev1.TestFileSelection{Path: "pkg/widgets/widget_test.go"}}}
	if _, err := RenderTestSelection(selection); err == nil {
		t.Fatal("file-only selection succeeded, want exactness error")
	}
}
