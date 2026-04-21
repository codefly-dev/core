package code

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrigramIndex_AddAndQuery(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("main.go", []byte("package main\nfunc main() {}\n"))
	idx.AddFile("handler.go", []byte("package handler\nfunc HandleUser() {}\n"))

	candidates := idx.Query("main")
	if len(candidates) != 1 || candidates[0] != "main.go" {
		t.Errorf("Query('main') = %v, want [main.go]", candidates)
	}
}

func TestTrigramIndex_QueryIntersection(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("a.go", []byte("func SetupRoutes() {}"))
	idx.AddFile("b.go", []byte("func SetupHandlers() {}"))
	idx.AddFile("c.go", []byte("func HandleRequest() {}"))

	// "SetupR" should match only a.go (has "Set", "etu", "tup", "upR")
	candidates := idx.Query("SetupR")
	if len(candidates) != 1 || candidates[0] != "a.go" {
		t.Errorf("Query('SetupR') = %v, want [a.go]", candidates)
	}
}

func TestTrigramIndex_QueryNoMatch(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("a.go", []byte("func hello() {}"))

	candidates := idx.Query("zzzzz")
	if len(candidates) != 0 {
		t.Errorf("Query('zzzzz') = %v, want []", candidates)
	}
}

func TestTrigramIndex_ShortPattern(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("a.go", []byte("hello"))
	idx.AddFile("b.go", []byte("world"))

	// Pattern < 3 chars: returns all files
	candidates := idx.Query("he")
	if len(candidates) != 2 {
		t.Errorf("short pattern should return all files, got %d", len(candidates))
	}
}

func TestTrigramIndex_RemoveFile(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("a.go", []byte("func hello() {}"))
	idx.AddFile("b.go", []byte("func hello() {}"))

	idx.RemoveFile("a.go")

	candidates := idx.Query("hello")
	if len(candidates) != 1 || candidates[0] != "b.go" {
		t.Errorf("after remove, Query('hello') = %v, want [b.go]", candidates)
	}

	if idx.Size() != 1 {
		t.Errorf("Size = %d, want 1", idx.Size())
	}
}

func TestTrigramIndex_UpdateFile(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("a.go", []byte("func hello() {}"))

	candidates := idx.Query("hello")
	if len(candidates) != 1 {
		t.Fatalf("initial query failed")
	}

	// Update content — old trigrams should be gone
	idx.AddFile("a.go", []byte("func world() {}"))

	candidates = idx.Query("hello")
	if len(candidates) != 0 {
		t.Errorf("after update, Query('hello') = %v, want []", candidates)
	}

	candidates = idx.Query("world")
	if len(candidates) != 1 {
		t.Errorf("after update, Query('world') = %v, want [a.go]", candidates)
	}
}

func TestTrigramIndex_RegexPattern(t *testing.T) {
	idx := NewTrigramIndex()
	idx.AddFile("a.go", []byte("func SetupRoutes() {}"))
	idx.AddFile("b.go", []byte("func other() {}"))

	// Regex with literal fragment "Setup"
	candidates := idx.Query("Setup.*Routes")
	if len(candidates) != 1 || candidates[0] != "a.go" {
		t.Errorf("Query('Setup.*Routes') = %v, want [a.go]", candidates)
	}
}

func TestExtractLiterals(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{"hello", []string{"hello"}},
		{"hello.*world", []string{"hello", "world"}},
		{"func\\(", []string{"func("}},
		{"[a-z]+foo", []string{"foo"}},
		{"^start$", []string{"start"}},
		{"a|b", []string{"a", "b"}},
		{"", nil},
		{"ab", []string{"ab"}},
	}
	for _, tt := range tests {
		got := extractLiterals(tt.pattern)
		if fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("extractLiterals(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestTrigramIndex_LargeFile(t *testing.T) {
	idx := NewTrigramIndex()

	// 100KB file
	content := []byte(strings.Repeat("func handler() { return nil }\n", 3000))
	idx.AddFile("big.go", content)

	candidates := idx.Query("handler")
	if len(candidates) != 1 {
		t.Errorf("Query on large file failed: got %d candidates", len(candidates))
	}
}

func BenchmarkTrigramIndex_AddFile(b *testing.B) {
	content := []byte(strings.Repeat("func handler() { return nil }\n", 1000))
	b.ResetTimer()
	for b.Loop() {
		idx := NewTrigramIndex()
		idx.AddFile("bench.go", content)
	}
}

func BenchmarkTrigramIndex_Query(b *testing.B) {
	idx := NewTrigramIndex()
	for i := range 1000 {
		idx.AddFile(fmt.Sprintf("file%d.go", i),
			[]byte(fmt.Sprintf("package pkg%d\nfunc handler%d() {}\n", i, i)))
	}
	b.ResetTimer()
	for b.Loop() {
		idx.Query("handler500")
	}
}
