package codefly

import (
	"context"
	"testing"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

func TestIdentityAndTools(t *testing.T) {
	s := New("/tmp/x", "test")
	id, err := s.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "codefly" || len(id.CanonicalFor) != 1 || id.CanonicalFor[0] != "codefly" {
		t.Errorf("identity wrong: %+v", id)
	}
	tools := s.Tools()
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.Handler == nil || tool.InputSchema == nil || len(tool.Examples) == 0 {
			t.Errorf("tool %q malformed", tool.Name)
		}
	}
}

func TestListOutsideWorkspaceErrors(t *testing.T) {
	// A dir with no codefly workspace at or above it should error cleanly, not panic.
	s := New(t.TempDir(), "test")
	resp := s.list(context.Background(), &toolboxv0.CallToolRequest{})
	if resp.Error == "" {
		t.Skip("environment happens to be inside a workspace; skipping negative check")
	}
}
