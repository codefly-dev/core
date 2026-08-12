package code

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSearchBoundsLongMatchingLinesByUTF8Bytes(t *testing.T) {
	dir := t.TempDir()
	line := "needle " + strings.Repeat("é", 200)
	if err := os.WriteFile(filepath.Join(dir, "generated.txt"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Search(context.Background(), dir, SearchOpts{
		Pattern: "needle", Literal: true, MaxResults: 50, MaxBytes: 65,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want one bounded match", len(result.Matches))
	}
	match := result.Matches[0]
	if !result.Truncated || result.TruncationReason != SearchTruncationMaxBytes {
		t.Fatalf("truncation = (%v, %v), want max-bytes", result.Truncated, result.TruncationReason)
	}
	if result.ReturnedTextBytes > 65 || len(match.Text) != result.ReturnedTextBytes {
		t.Fatalf("returned text bytes = %d (match=%d), want <= 65", result.ReturnedTextBytes, len(match.Text))
	}
	if !match.TextTruncated || match.OriginalTextBytes != len(line) {
		t.Fatalf("match truncation metadata = %+v", match)
	}
	if !utf8.ValidString(match.Text) {
		t.Fatalf("bounded match is not valid UTF-8: %q", match.Text)
	}
}
