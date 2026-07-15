package code

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestSearchReturnsRipgrepPatternErrors(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	_, err := Search(context.Background(), t.TempDir(), SearchOpts{Pattern: "["})
	if err == nil || !strings.Contains(err.Error(), "ripgrep search failed") {
		t.Fatalf("invalid pattern error = %v", err)
	}
}

func TestSearchNoMatchesIsNotAnError(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	result, err := Search(context.Background(), t.TempDir(), SearchOpts{Pattern: "not-present", Literal: true})
	if err != nil {
		t.Fatalf("no-match search returned error: %v", err)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("no-match search returned results: %+v", result.Matches)
	}
}
