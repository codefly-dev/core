package golang

import (
	"fmt"
	"strings"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/testselection"
)

// RenderTestSelection translates a language-neutral selection to the private
// selector inputs consumed by the Go runner. This translation belongs in the
// execution plugin: callers never construct Go package or -run syntax.
func RenderTestSelection(selection *runtimev0.TestSelection) ([]string, error) {
	if err := testselection.Validate(selection); err != nil {
		return nil, err
	}
	switch scope := selection.GetScope().(type) {
	case *runtimev0.TestSelection_Package:
		return []string{scope.Package.GetPackage()}, nil
	case *runtimev0.TestSelection_TestCase:
		testCase := scope.TestCase
		if testCase.GetPackage() == "" {
			return nil, fmt.Errorf("Go test case selection requires package")
		}
		return []string{testCase.GetPackage(), strings.Join(testCase.GetQualifiedName(), "/")}, nil
	case *runtimev0.TestSelection_File:
		return nil, fmt.Errorf("Go runtime cannot guarantee file-only test selection; select a package or exact case")
	case *runtimev0.TestSelection_Suite:
		return nil, fmt.Errorf("Go runtime does not declare named test suites")
	default:
		return nil, fmt.Errorf("unsupported Go test selection %T", scope)
	}
}
