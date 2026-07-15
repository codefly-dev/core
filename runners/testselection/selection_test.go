package testselection

import (
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func TestTypedSelectionHandshake(t *testing.T) {
	req := exactCaseRequest()
	if err := ValidateRequest(req); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
	resp := &runtimev0.TestResponse{Run: &runtimev0.TestRun{Runner: "go-test"}}
	if err := Acknowledge(req, resp); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if err := VerifyAcknowledgement(req, resp); err != nil {
		t.Fatalf("VerifyAcknowledgement: %v", err)
	}
	if resp.GetRun().GetRequestedSelection() == req.GetSelection() {
		t.Fatal("acknowledgement must clone the request selection")
	}
}

func TestTypedSelectionHandshakeFailsClosed(t *testing.T) {
	req := exactCaseRequest()
	cases := map[string]*runtimev0.TestResponse{
		"old peer omitted acknowledgement": {Run: &runtimev0.TestRun{}},
		"missing run":                      {},
		"wrong identity":                   {Run: &runtimev0.TestRun{SelectionId: "other", RequestedSelection: req.GetSelection()}},
		"changed selection": {Run: &runtimev0.TestRun{
			SelectionId: req.GetSelectionId(),
			RequestedSelection: &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_File{
				File: &runtimev0.TestFileSelection{Path: "pkg/other_test.go"},
			}},
		}},
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifyAcknowledgement(req, resp); err == nil {
				t.Fatal("VerifyAcknowledgement succeeded, want fail-closed error")
			}
		})
	}
}

func TestValidateRejectsAmbiguousOrNonCanonicalSelection(t *testing.T) {
	cases := map[string]*runtimev0.TestRequest{
		"identity without selection":   {SelectionId: "binding"},
		"selection without identity":   {Selection: exactCaseRequest().GetSelection()},
		"selection with string target": {SelectionId: "binding", Selection: exactCaseRequest().GetSelection(), Target: "TestX"},
		"selection with formula": {SelectionId: "binding", Selection: exactCaseRequest().GetSelection(), Formula: &runtimev0.TestFormula{
			Command: []string{"go", "test"},
		}},
		"scope missing": {SelectionId: "binding", Selection: &runtimev0.TestSelection{}},
		"case location missing": {SelectionId: "binding", Selection: &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_TestCase{
			TestCase: &runtimev0.TestCaseSelection{QualifiedName: []string{"TestX"}},
		}}},
		"case name missing": {SelectionId: "binding", Selection: &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_TestCase{
			TestCase: &runtimev0.TestCaseSelection{Package: "./pkg"},
		}}},
		"parent path": {SelectionId: "binding", Selection: &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_File{
			File: &runtimev0.TestFileSelection{Path: "../outside_test.go"},
		}}},
		"noncanonical path": {SelectionId: "binding", Selection: &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_File{
			File: &runtimev0.TestFileSelection{Path: "pkg/../outside_test.go"},
		}}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateRequest(req); err == nil {
				t.Fatal("ValidateRequest succeeded, want validation error")
			}
		})
	}
}

func exactCaseRequest() *runtimev0.TestRequest {
	return &runtimev0.TestRequest{
		SelectionId: "binding-sha256",
		Selection: &runtimev0.TestSelection{Scope: &runtimev0.TestSelection_TestCase{
			TestCase: &runtimev0.TestCaseSelection{
				Package:       "./pkg/widgets",
				Path:          "pkg/widgets/widget_test.go",
				QualifiedName: []string{"TestWidget", "empty input"},
			},
		}},
	}
}
