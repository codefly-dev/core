package python

import (
	"strings"
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

func TestCommandForExactSelectionReplacesBroadPytestDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command []string
		want    string
	}{
		{
			name:    "direct pytest",
			command: []string{"pytest", "--pyargs", "astropy", "docs"},
			want:    "pytest",
		},
		{
			name:    "python module",
			command: []string{"python", "-X", "dev", "-m", "pytest", "-q", "tests"},
			want:    "python -X dev -m pytest",
		},
		{
			name:    "coverage module",
			command: []string{"coverage", "run", "-m", "pytest", "--doctest-modules", "src"},
			want:    "coverage run -m pytest",
		},
		{
			name:    "windows executable",
			command: []string{`C:\\venv\\Scripts\\pytest.exe`, "tests"},
			want:    `C:\\venv\\Scripts\\pytest.exe`,
		},
		{
			name:    "non pytest runner remains project owned",
			command: []string{"python", "runtests.py", "--verbosity=2"},
			want:    "python runtests.py --verbosity=2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(CommandForExactSelection(tc.command), " ")
			if got != tc.want {
				t.Fatalf("exact-selection command = %q, want %q", got, tc.want)
			}
		})
	}
}

func pythonCaseSelection() *runtimev0.TestSelection {
	return &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_TestCase{TestCase: &runtimev0.TestCaseSelection{
		Path:          "tests/admin_docs/test_utils.py",
		QualifiedName: []string{"WidgetTests", "test_empty"},
	}}}
}
