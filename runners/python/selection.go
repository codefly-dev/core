package python

import (
	"fmt"
	"strings"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/testselection"
)

// RenderTestSelection translates a language-neutral selection to the private
// selector syntax consumed by the Python runner selected by command. Pytest
// node delimiters and Django dotted labels never cross the Codefly boundary.
func RenderTestSelection(selection *runtimev0.TestSelection, command []string, cwd string) ([]string, error) {
	if err := testselection.Validate(selection); err != nil {
		return nil, err
	}
	switch scope := selection.GetScope().(type) {
	case *runtimev0.TestSelection_File:
		return selectorsForCommand(command, cwd, []string{scope.File.GetPath()}), nil
	case *runtimev0.TestSelection_TestCase:
		testCase := scope.TestCase
		if testCase.GetPath() == "" {
			return nil, fmt.Errorf("Python test case selection requires path")
		}
		if commandIsDjangoRuntests(command) {
			base := djangoTestLabel(testCase.GetPath(), djangoTestRoot(command, cwd))
			return []string{base + "." + strings.Join(testCase.GetQualifiedName(), ".")}, nil
		}
		return []string{testCase.GetPath() + "::" + strings.Join(testCase.GetQualifiedName(), "::")}, nil
	case *runtimev0.TestSelection_Package:
		return nil, fmt.Errorf("Python runtime cannot map a package identity to an exact collection path; select a file or case")
	case *runtimev0.TestSelection_Suite:
		return nil, fmt.Errorf("Python runtime does not declare named test suites")
	default:
		return nil, fmt.Errorf("unsupported Python test selection %T", scope)
	}
}

// CommandForExactSelection returns the runner invocation that an authoritative
// typed selector may safely extend. Pytest positional operands are discovery
// roots, so retaining a derived command such as `pytest --pyargs astropy docs`
// and appending one node ID still collects the broad roots. Unrelated
// collection errors can then masquerade as the selected test's result.
//
// The selected pytest node ID is a complete collection target. Preserve the
// real runner prefix (direct pytest, python -m pytest, coverage run -m pytest)
// and replace every later discovery/option operand. Pytest still loads the
// project's own configuration from the workspace. Other runners keep their
// project-declared argv because their selection grammar is runner-specific.
func CommandForExactSelection(command []string) []string {
	for index, argument := range command {
		if pythonExecutableName(argument) == "pytest" || pythonExecutableName(argument) == "py.test" {
			return append([]string(nil), command[:index+1]...)
		}
	}
	return append([]string(nil), command...)
}

func pythonExecutableName(argument string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(argument), `\`, "/")
	if slash := strings.LastIndexByte(normalized, '/'); slash >= 0 {
		normalized = normalized[slash+1:]
	}
	normalized = strings.ToLower(normalized)
	return strings.TrimSuffix(normalized, ".exe")
}
