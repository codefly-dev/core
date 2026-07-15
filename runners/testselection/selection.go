package testselection

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"google.golang.org/protobuf/proto"
)

const (
	maxSelectionValueBytes = 4096
	maxSelectionIDBytes    = 256
)

// ValidateRequest enforces the typed-selection handshake. Unscoped requests
// remain valid, but a selection and its identity must always travel together.
func ValidateRequest(req *runtimev0.TestRequest) error {
	if req == nil {
		return fmt.Errorf("test request is required")
	}
	selection := req.GetSelection()
	selectionID := req.GetSelectionId()
	if selection == nil {
		if selectionID != "" {
			return fmt.Errorf("test selection_id requires selection")
		}
		return nil
	}
	if err := validateValue("selection_id", selectionID, maxSelectionIDBytes); err != nil {
		return err
	}
	if req.GetTarget() != "" || len(req.GetFilters()) != 0 || req.GetSuite() != "" || len(req.GetExtraArgs()) != 0 || req.GetFormula() != nil {
		return fmt.Errorf("typed test selection cannot be combined with target, filters, suite, extra_args, or formula")
	}
	return Validate(selection)
}

// Validate rejects ambiguous or non-canonical selections before a runtime
// plugin attempts to render them for its native test runner.
func Validate(selection *runtimev0.TestSelection) error {
	if selection == nil {
		return fmt.Errorf("test selection is required")
	}
	switch scope := selection.GetScope().(type) {
	case *runtimev0.TestSelection_Package:
		if scope.Package == nil {
			return fmt.Errorf("test package selection is required")
		}
		return validateValue("test package", scope.Package.GetPackage(), maxSelectionValueBytes)
	case *runtimev0.TestSelection_File:
		if scope.File == nil {
			return fmt.Errorf("test file selection is required")
		}
		return validatePath(scope.File.GetPath())
	case *runtimev0.TestSelection_Suite:
		if scope.Suite == nil {
			return fmt.Errorf("test suite selection is required")
		}
		return validateValue("test suite", scope.Suite.GetName(), maxSelectionValueBytes)
	case *runtimev0.TestSelection_TestCase:
		return validateCase(scope.TestCase)
	case nil:
		return fmt.Errorf("test selection scope is required")
	default:
		return fmt.Errorf("unsupported test selection scope %T", scope)
	}
}

func validateCase(testCase *runtimev0.TestCaseSelection) error {
	if testCase == nil {
		return fmt.Errorf("test case selection is required")
	}
	if testCase.GetPackage() == "" && testCase.GetPath() == "" {
		return fmt.Errorf("test case selection requires package or path")
	}
	if testCase.GetPackage() != "" {
		if err := validateValue("test case package", testCase.GetPackage(), maxSelectionValueBytes); err != nil {
			return err
		}
	}
	if testCase.GetPath() != "" {
		if err := validatePath(testCase.GetPath()); err != nil {
			return err
		}
	}
	if testCase.GetSuite() != "" {
		if err := validateValue("test case suite", testCase.GetSuite(), maxSelectionValueBytes); err != nil {
			return err
		}
	}
	if len(testCase.GetQualifiedName()) == 0 {
		return fmt.Errorf("test case qualified_name requires at least one segment")
	}
	for i, segment := range testCase.GetQualifiedName() {
		if err := validateValue(fmt.Sprintf("test case qualified_name[%d]", i), segment, maxSelectionValueBytes); err != nil {
			return err
		}
	}
	return nil
}

func validatePath(value string) error {
	if err := validateValue("test path", value, maxSelectionValueBytes); err != nil {
		return err
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("test path must use forward slashes")
	}
	clean := path.Clean(value)
	if path.IsAbs(value) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("test path must be workspace-relative")
	}
	if clean != value {
		return fmt.Errorf("test path must be canonical: got %q, want %q", value, clean)
	}
	return nil
}

func validateValue(name, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maxBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain surrounding whitespace", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s contains control characters", name)
	}
	return nil
}

// Acknowledge records that a runtime applied the request's exact selection.
// Call this only after native selector translation succeeded. It deliberately
// refuses to manufacture a TestRun because the run metadata belongs to the
// runtime that executed the tests.
func Acknowledge(req *runtimev0.TestRequest, resp *runtimev0.TestResponse) error {
	if err := ValidateRequest(req); err != nil {
		return err
	}
	if req.GetSelection() == nil {
		return nil
	}
	if resp == nil || resp.GetRun() == nil {
		return fmt.Errorf("typed test selection response requires run metadata")
	}
	resp.Run.RequestedSelection = proto.Clone(req.GetSelection()).(*runtimev0.TestSelection)
	resp.Run.SelectionId = req.GetSelectionId()
	return nil
}

// VerifyAcknowledgement rejects broad/default runs and older peers that did
// not honor a scoped request.
func VerifyAcknowledgement(req *runtimev0.TestRequest, resp *runtimev0.TestResponse) error {
	if err := ValidateRequest(req); err != nil {
		return err
	}
	if req.GetSelection() == nil {
		return nil
	}
	if resp == nil || resp.GetRun() == nil {
		return fmt.Errorf("typed test selection response is missing run metadata")
	}
	if resp.GetRun().GetSelectionId() != req.GetSelectionId() {
		return fmt.Errorf("typed test selection acknowledgement mismatch: got %q, want %q", resp.GetRun().GetSelectionId(), req.GetSelectionId())
	}
	if !proto.Equal(resp.GetRun().GetRequestedSelection(), req.GetSelection()) {
		return fmt.Errorf("typed test selection acknowledgement changed the requested selection")
	}
	return nil
}
