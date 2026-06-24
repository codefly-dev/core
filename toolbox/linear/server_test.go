package linear

import (
	"strings"
	"testing"

	"github.com/mind-build/gortk"
)

// TestIssuesCompaction is the core proof that gortk compacts Linear's verbose
// GraphQL JSON into one line per issue — offline, no network.
func TestIssuesCompaction(t *testing.T) {
	resp := `{"data":{"issues":{"nodes":[
		{"identifier":"ENG-1","title":"Fix the compressor","url":"https://linear.app/x/ENG-1","priorityLabel":"High","state":{"name":"In Progress"},"assignee":{"displayName":"Alice"}},
		{"identifier":"ENG-2","title":"Add a filter","url":"https://linear.app/x/ENG-2","priorityLabel":"Medium","state":{"name":"Todo"},"assignee":{"displayName":"Bob"}}
	]}}}`

	out := issuesFilter.Apply(gortk.Command{Stdout: []byte(resp)}).Text

	if !strings.Contains(out, "ENG-1 [In Progress] Fix the compressor — Alice (High) https://linear.app/x/ENG-1") {
		t.Errorf("issue 1 not compacted as expected:\n%s", out)
	}
	if !strings.Contains(out, "ENG-2 [Todo] Add a filter — Bob (Medium)") {
		t.Errorf("issue 2 not compacted as expected:\n%s", out)
	}
	if !strings.Contains(out, "linear: 2 issue(s)") {
		t.Errorf("summary missing:\n%s", out)
	}
}

func TestSearchUsesIssueSearchPath(t *testing.T) {
	resp := `{"data":{"issueSearch":{"nodes":[
		{"identifier":"ENG-9","title":"Search hit","url":"u","priorityLabel":"Low","state":{"name":"Done"},"assignee":{"displayName":"Cara"}}
	]}}}`
	out := searchFilter.Apply(gortk.Command{Stdout: []byte(resp)}).Text
	if !strings.Contains(out, "ENG-9 [Done] Search hit — Cara (Low)") {
		t.Errorf("search compaction wrong:\n%s", out)
	}
}

func TestMissingKeyErrors(t *testing.T) {
	s := New("", "test")
	// No network call happens because the key check short-circuits.
	if _, err := s.graphql(nil, "query{}", nil); err == nil {
		t.Error("expected error when LINEAR_API_KEY is unset")
	}
}

func TestToolsWellFormed(t *testing.T) {
	s := New("k", "test")
	tools := s.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.Handler == nil || tool.InputSchema == nil {
			t.Errorf("tool %q is missing name/handler/schema", tool.Name)
		}
	}
}
