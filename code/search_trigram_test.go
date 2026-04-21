package code

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchTrigram_BasicMatch(t *testing.T) {
	idx := NewTrigramIndex()
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/main.go":    "package main\nfunc main() { fmt.Println(\"hello\") }\n",
		"/repo/handler.go": "package handler\nfunc HandleUser() {}\n",
	})
	idx.AddFile("main.go", []byte("package main\nfunc main() { fmt.Println(\"hello\") }\n"))
	idx.AddFile("handler.go", []byte("package handler\nfunc HandleUser() {}\n"))

	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern: "HandleUser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
	if result.Matches[0].File != "handler.go" {
		t.Errorf("match file = %q, want handler.go", result.Matches[0].File)
	}
	if result.Matches[0].Line != 2 {
		t.Errorf("match line = %d, want 2", result.Matches[0].Line)
	}
}

func TestSearchTrigram_RegexPattern(t *testing.T) {
	idx := NewTrigramIndex()
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/a.go": "func SetupRoutes() {}\nfunc SetupHandlers() {}\n",
		"/repo/b.go": "func other() {}\n",
	})
	idx.AddFile("a.go", []byte("func SetupRoutes() {}\nfunc SetupHandlers() {}\n"))
	idx.AddFile("b.go", []byte("func other() {}\n"))

	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern: "Setup.*Routes",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}
}

func TestSearchTrigram_CaseInsensitive(t *testing.T) {
	idx := NewTrigramIndex()
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/a.go": "func HandleUser() {}\n",
	})
	idx.AddFile("a.go", []byte("func HandleUser() {}\n"))

	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern:         "handleuser",
		CaseInsensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match (case-insensitive), got %d", len(result.Matches))
	}
}

func TestSearchTrigram_LiteralPattern(t *testing.T) {
	idx := NewTrigramIndex()
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/a.go": "func main() { x := a.b(c) }\n",
	})
	idx.AddFile("a.go", []byte("func main() { x := a.b(c) }\n"))

	// "a.b(c)" as literal — dots and parens should NOT be regex metacharacters.
	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern: "a.b(c)",
		Literal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 literal match, got %d", len(result.Matches))
	}
}

func TestSearchTrigram_NoMatch(t *testing.T) {
	idx := NewTrigramIndex()
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/a.go": "func hello() {}\n",
	})
	idx.AddFile("a.go", []byte("func hello() {}\n"))

	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern: "zzzzNotHere",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(result.Matches))
	}
}

func TestSearchTrigram_MaxResults(t *testing.T) {
	idx := NewTrigramIndex()
	content := "match\nmatch\nmatch\nmatch\nmatch\n"
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/a.go": content,
	})
	idx.AddFile("a.go", []byte(content))

	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern:    "match",
		MaxResults: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 3 {
		t.Errorf("expected 3 matches (max), got %d", len(result.Matches))
	}
	if !result.Truncated {
		t.Error("expected Truncated=true")
	}
}

func TestSearchTrigram_ExtensionFilter(t *testing.T) {
	idx := NewTrigramIndex()
	mem := NewMemoryVFSFrom(map[string]string{
		"/repo/a.go": "func hello() {}\n",
		"/repo/b.py": "def hello(): pass\n",
	})
	idx.AddFile("a.go", []byte("func hello() {}\n"))
	idx.AddFile("b.py", []byte("def hello(): pass\n"))

	result, err := SearchTrigram(context.Background(), mem, idx, "/repo", SearchOpts{
		Pattern:    "hello",
		Extensions: []string{".go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match (.go only), got %d", len(result.Matches))
	}
	if result.Matches[0].File != "a.go" {
		t.Errorf("match file = %q, want a.go", result.Matches[0].File)
	}
}

func TestServerWithTrigramIndex_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc SetupRoutes() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "handler.go"), []byte("package handler\nfunc HandleUser() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "utils.go"), []byte("package utils\nfunc FormatDate() {}\n"), 0644)

	srv := NewDefaultCodeServer(dir, WithTrigramIndex())
	defer srv.Close()

	// Verify trigram index was populated.
	if srv.trigramIdx == nil {
		t.Fatal("expected trigram index to be active")
	}
	if srv.trigramIdx.Size() < 3 {
		t.Errorf("trigram index has %d files, want >= 3", srv.trigramIdx.Size())
	}

	// Verify content cache was also enabled (WithTrigramIndex implies it).
	if srv.cachedFS == nil || srv.cachedFS.contentCache == nil {
		t.Fatal("WithTrigramIndex should imply content cache")
	}

	// Search via SearchTrigram directly (same path the server dispatch uses).
	ctx := context.Background()
	result, err := SearchTrigram(ctx, srv.FS, srv.trigramIdx, dir, SearchOpts{
		Pattern: "HandleUser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match via trigram search, got %d", len(result.Matches))
	}
	if result.Matches[0].File != "handler.go" {
		t.Errorf("match file = %q, want handler.go", result.Matches[0].File)
	}

	// Search for something that doesn't exist.
	result2, err := SearchTrigram(ctx, srv.FS, srv.trigramIdx, dir, SearchOpts{
		Pattern: "NonExistentSymbol",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Matches) != 0 {
		t.Errorf("expected 0 matches for non-existent, got %d", len(result2.Matches))
	}
}
