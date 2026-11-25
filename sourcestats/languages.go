package sourcestats

import (
	"path"
	"strings"
)

// block is one block-comment syntax.
type block struct{ open, close string }

// syntax is how one language's files are recognised and how its comments
// begin.
type syntax struct {
	name       string
	extensions []string
	line       []string
	blocks     []block
}

var (
	slashes = []string{"//"}
	cBlock  = []block{{"/*", "*/"}}
	hash    = []string{"#"}
)

// languages is every language counted. A language is here because services
// are written in it; a file in any other is not code this package counts.
var languages = []syntax{
	{name: "c", extensions: []string{".c", ".h"}, line: slashes, blocks: cBlock},
	{name: "cpp", extensions: []string{".cc", ".cpp", ".cxx", ".hpp", ".hh"}, line: slashes, blocks: cBlock},
	{name: "csharp", extensions: []string{".cs"}, line: slashes, blocks: cBlock},
	{name: "css", extensions: []string{".css", ".scss", ".less"}, line: slashes, blocks: cBlock},
	{name: "dart", extensions: []string{".dart"}, line: slashes, blocks: cBlock},
	{name: "go", extensions: []string{".go"}, line: slashes, blocks: cBlock},
	{name: "html", extensions: []string{".html", ".htm"}, blocks: []block{{"<!--", "-->"}}},
	{name: "java", extensions: []string{".java"}, line: slashes, blocks: cBlock},
	{name: "javascript", extensions: []string{".js", ".jsx", ".mjs", ".cjs"}, line: slashes, blocks: cBlock},
	{name: "kotlin", extensions: []string{".kt", ".kts"}, line: slashes, blocks: cBlock},
	{name: "proto", extensions: []string{".proto"}, line: slashes, blocks: cBlock},
	{name: "python", extensions: []string{".py", ".pyi"}, line: hash},
	{name: "ruby", extensions: []string{".rb"}, line: hash, blocks: []block{{"=begin", "=end"}}},
	{name: "rust", extensions: []string{".rs"}, line: slashes, blocks: cBlock},
	{name: "shell", extensions: []string{".sh", ".bash", ".zsh"}, line: hash},
	{name: "sql", extensions: []string{".sql"}, line: []string{"--"}, blocks: cBlock},
	{name: "swift", extensions: []string{".swift"}, line: slashes, blocks: cBlock},
	{name: "typescript", extensions: []string{".ts", ".tsx", ".mts", ".cts"}, line: slashes, blocks: cBlock},
}

var byExtension = func() map[string]syntax {
	out := map[string]syntax{}
	for _, l := range languages {
		for _, extension := range l.extensions {
			out[extension] = l
		}
	}
	return out
}()

func languageOf(name string) (syntax, bool) {
	l, known := byExtension[strings.ToLower(path.Ext(name))]
	return l, known
}

// lineComment is whether a trimmed, non-empty line is only a comment.
func (l syntax) lineComment(line string) bool {
	for _, prefix := range l.line {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// opens is whether a trimmed line begins a block comment, and which.
func (l syntax) opens(line string) (bool, block) {
	for _, b := range l.blocks {
		if strings.HasPrefix(line, b.open) {
			return true, b
		}
	}
	return false, block{}
}
