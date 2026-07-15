package python

import (
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func TestRenderTestSelectionOwnsPythonSelectorGrammar(t *testing.T) {
	selection := pythonCaseSelection()
	pytest, err := RenderTestSelection(selection, []string{"pytest"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pytest) != 1 || pytest[0] != "tests/admin_docs/test_utils.py::WidgetTests::test_empty" {
		t.Fatalf("pytest selectors = %#v", pytest)
	}

	django, err := RenderTestSelection(selection, []string{"python", "runtests.py"}, "tests")
	if err != nil {
		t.Fatal(err)
	}
	if len(django) != 1 || django[0] != "admin_docs.test_utils.WidgetTests.test_empty" {
		t.Fatalf("django selectors = %#v", django)
	}
}

func pythonCaseSelection() *runtimev0.TestSelection {
	return &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_TestCase{TestCase: &runtimev0.TestCaseSelection{
		Path:          "tests/admin_docs/test_utils.py",
		QualifiedName: []string{"WidgetTests", "test_empty"},
	}}}
}
