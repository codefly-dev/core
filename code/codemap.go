package code

import (
	"fmt"
	"sort"
	"strings"
)

// CodeMap is a structured representation of all symbols in a codebase,
// organized by file and hierarchy. This is used by Mind to give the LLM
// a compressed overview of the project's code structure.
type CodeMap struct {
	Language string
	Files    []FileMap
}

// FileMap represents the symbols in a single file.
type FileMap struct {
	Path    string
	Symbols []SymbolEntry
}

// SymbolEntry is a flattened symbol record for the code map.
type SymbolEntry struct {
	Name      string
	Kind      string // "function", "struct", "method", "interface", "class", "constant", "variable"
	Signature string
	Line      int
	Parent    string
	Children  []SymbolEntry
}

// BuildCodeMap constructs a CodeMap from raw symbol data (as returned by ListSymbols).
// Each SymbolInput mirrors the proto Symbol message but in plain Go types.
type SymbolInput struct {
	Name      string
	Kind      string
	Signature string
	File      string
	Line      int
	Parent    string
	Children  []SymbolInput
}

// BuildCodeMap groups symbols by file and sorts them by line number.
func BuildCodeMap(language string, symbols []SymbolInput) *CodeMap {
	byFile := map[string][]SymbolEntry{}

	for _, s := range symbols {
		entry := toEntry(s)
		byFile[s.File] = append(byFile[s.File], entry)
	}

	var files []FileMap
	for path, entries := range byFile {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Line < entries[j].Line
		})
		files = append(files, FileMap{Path: path, Symbols: entries})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	return &CodeMap{Language: language, Files: files}
}

func toEntry(s SymbolInput) SymbolEntry {
	entry := SymbolEntry{
		Name:      s.Name,
		Kind:      s.Kind,
		Signature: s.Signature,
		Line:      s.Line,
		Parent:    s.Parent,
	}
	for _, child := range s.Children {
		entry.Children = append(entry.Children, toEntry(child))
	}
	return entry
}

// Format produces a compact text representation suitable for LLM system prompts.
// It uses ~1 line per symbol to stay within context budgets.
func (cm *CodeMap) Format() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Code Map (%s)\n\n", cm.Language))

	for _, f := range cm.Files {
		b.WriteString(fmt.Sprintf("## %s\n", f.Path))
		for _, s := range f.Symbols {
			formatSymbol(&b, s, 0)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func formatSymbol(b *strings.Builder, s SymbolEntry, depth int) {
	indent := strings.Repeat("  ", depth)
	if s.Signature != "" {
		b.WriteString(fmt.Sprintf("%s  L%d %s %s\n", indent, s.Line, s.Kind, s.Signature))
	} else {
		b.WriteString(fmt.Sprintf("%s  L%d %s %s\n", indent, s.Line, s.Kind, s.Name))
	}
	for _, child := range s.Children {
		formatSymbol(b, child, depth+1)
	}
}

// Stats returns summary statistics about the code map.
func (cm *CodeMap) Stats() CodeMapStats {
	stats := CodeMapStats{}
	for _, f := range cm.Files {
		stats.Files++
		stats.Symbols += countSymbols(f.Symbols)
	}
	return stats
}

type CodeMapStats struct {
	Files   int
	Symbols int
}

func countSymbols(entries []SymbolEntry) int {
	n := len(entries)
	for _, e := range entries {
		n += countSymbols(e.Children)
	}
	return n
}
