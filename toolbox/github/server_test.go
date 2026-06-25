package github

import (
	"context"
	"testing"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

func TestIdentity(t *testing.T) {
	id, err := New("/tmp/x", "", "test").Identity(context.Background(), &toolboxv0.IdentityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "github" {
		t.Errorf("name = %q, want github", id.Name)
	}
}

func TestParseGitHubRemote(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:mind-build/gortk.git":     {"mind-build", "gortk"},
		"https://github.com/codefly-dev/core.git": {"codefly-dev", "core"},
		"https://github.com/owner/repo":           {"owner", "repo"},
	}
	for url, want := range cases {
		o, r, err := parseGitHubRemote(url)
		if err != nil || o != want[0] || r != want[1] {
			t.Errorf("%s -> (%q,%q,%v), want %v", url, o, r, err, want)
		}
	}
	if _, _, err := parseGitHubRemote("https://gitlab.com/x/y"); err == nil {
		t.Error("expected error for non-github remote")
	}
}

func TestToolsWellFormed(t *testing.T) {
	tools := New("/tmp/x", "", "test").Tools()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.Handler == nil || tool.InputSchema == nil {
			t.Errorf("tool %q missing name/handler/schema", tool.Name)
		}
		if len(tool.Examples) == 0 {
			t.Errorf("tool %q has no examples", tool.Name)
		}
	}
}

func TestPrViewRequiresNumber(t *testing.T) {
	s := New("/tmp/x", "", "test")
	// owner/repo provided so resolveRepo succeeds; number missing -> error.
	resp := s.prView(context.Background(), &toolboxv0.CallToolRequest{
		Arguments: mustStruct(map[string]any{"owner": "o", "repo": "r"}),
	})
	if resp.Error == "" {
		t.Error("expected error when number is missing")
	}
}
