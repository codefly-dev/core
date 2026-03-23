package code

import (
	"strings"
	"testing"
)

func TestBuildCodeMap_GroupsByFile(t *testing.T) {
	symbols := []SymbolInput{
		{Name: "main", Kind: "function", Signature: "func main()", File: "main.go", Line: 5},
		{Name: "Server", Kind: "struct", Signature: "type Server struct", File: "server.go", Line: 10},
		{Name: "Start", Kind: "method", Signature: "func (s *Server) Start()", File: "server.go", Line: 15, Parent: "Server"},
	}

	cm := BuildCodeMap("go", symbols)

	if cm.Language != "go" {
		t.Errorf("expected go, got %s", cm.Language)
	}
	if len(cm.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(cm.Files))
	}
	if cm.Files[0].Path != "main.go" {
		t.Errorf("expected first file main.go, got %s", cm.Files[0].Path)
	}
	if len(cm.Files[0].Symbols) != 1 {
		t.Errorf("expected 1 symbol in main.go, got %d", len(cm.Files[0].Symbols))
	}
	if len(cm.Files[1].Symbols) != 2 {
		t.Errorf("expected 2 symbols in server.go, got %d", len(cm.Files[1].Symbols))
	}
}

func TestBuildCodeMap_SortsByLine(t *testing.T) {
	symbols := []SymbolInput{
		{Name: "Z", Kind: "function", File: "app.go", Line: 50},
		{Name: "A", Kind: "function", File: "app.go", Line: 10},
		{Name: "M", Kind: "function", File: "app.go", Line: 30},
	}

	cm := BuildCodeMap("go", symbols)
	entries := cm.Files[0].Symbols
	if entries[0].Name != "A" || entries[1].Name != "M" || entries[2].Name != "Z" {
		t.Errorf("expected A, M, Z but got %s, %s, %s", entries[0].Name, entries[1].Name, entries[2].Name)
	}
}

func TestCodeMap_Format(t *testing.T) {
	cm := BuildCodeMap("go", []SymbolInput{
		{Name: "main", Kind: "function", Signature: "func main()", File: "main.go", Line: 5},
	})
	output := cm.Format()
	if !strings.Contains(output, "# Code Map (go)") {
		t.Error("expected header")
	}
	if !strings.Contains(output, "## main.go") {
		t.Error("expected file section")
	}
	if !strings.Contains(output, "func main()") {
		t.Error("expected signature")
	}
}

func TestCodeMap_Stats(t *testing.T) {
	cm := BuildCodeMap("go", []SymbolInput{
		{Name: "A", Kind: "function", File: "a.go", Line: 1},
		{Name: "B", Kind: "function", File: "b.go", Line: 1},
		{Name: "C", Kind: "function", File: "b.go", Line: 10},
	})
	stats := cm.Stats()
	if stats.Files != 2 {
		t.Errorf("expected 2 files, got %d", stats.Files)
	}
	if stats.Symbols != 3 {
		t.Errorf("expected 3 symbols, got %d", stats.Symbols)
	}
}

func TestCodeMap_Children(t *testing.T) {
	cm := BuildCodeMap("go", []SymbolInput{
		{Name: "Server", Kind: "struct", File: "server.go", Line: 5, Children: []SymbolInput{
			{Name: "host", Kind: "field", File: "server.go", Line: 6},
			{Name: "port", Kind: "field", File: "server.go", Line: 7},
		}},
	})
	stats := cm.Stats()
	if stats.Symbols != 3 {
		t.Errorf("expected 3 symbols (1 struct + 2 fields), got %d", stats.Symbols)
	}
}

func TestBuildCodeMap_Empty(t *testing.T) {
	cm := BuildCodeMap("go", nil)
	if len(cm.Files) != 0 {
		t.Errorf("expected no files, got %d", len(cm.Files))
	}
	stats := cm.Stats()
	if stats.Files != 0 || stats.Symbols != 0 {
		t.Error("expected zero stats")
	}
}
