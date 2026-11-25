// Package sourcestats counts the lines of code in a source tree and says how
// many of them a person wrote. It is the one implementation of
// codefly/source-stats/v1, the figure a release carries so that whoever
// installs it can see its size without having its source.
//
// One number would mislead. A unit built on generated clients is mostly code
// nobody wrote, so every language is counted three ways: handwritten,
// generated and tests. Handwritten is the figure that says how much there is
// to read and maintain.
//
// # What is counted
//
// A line of code is a line that is neither blank nor only a comment, in a
// file whose extension names a programming language (languages.go).
// Configuration, data and prose — YAML, JSON, Markdown, lockfiles — are not
// code and are not counted. A file is one of three things, decided in this
// order:
//
//   - generated, when its name follows a generator's convention, its head
//     carries a generated-code marker, or the caller says so (Options.Generated);
//   - tests, when its name or a directory above it follows its language's
//     test convention;
//   - handwritten, otherwise.
//
// # What the count is not
//
// It is a size, not a measure of quality or effort, and it is approximate by
// design: comments are recognised by how a line begins, without parsing the
// language, so a comment marker inside a string literal and a Python
// docstring are read the way their first characters look. The same tree
// always counts the same, which is what a figure bound to a release needs.
package sourcestats

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// SchemaV1 is the only source-statistics schema this package writes or reads.
const SchemaV1 = "codefly/source-stats/v1"

// ErrInvalid means a Stats document violates its schema.
var ErrInvalid = errors.New("source statistics are invalid")

// Stats is the size of one source tree.
type Stats struct {
	// Schema is SchemaV1.
	Schema string `yaml:"schema" json:"schema"`
	// Languages is one entry per language with at least one counted line,
	// sorted by name.
	Languages []Language `yaml:"languages" json:"languages"`
}

// Language is one language's lines of code.
type Language struct {
	// Language is the language's name, as the agents spell it.
	Language string `yaml:"language" json:"language"`
	// Handwritten is lines a person wrote that are not tests.
	Handwritten int `yaml:"handwritten" json:"handwritten"`
	// Generated is lines a tool wrote.
	Generated int `yaml:"generated" json:"generated"`
	// Tests is lines a person wrote to test the rest.
	Tests int `yaml:"tests" json:"tests"`
}

// Handwritten is the lines a person wrote that are not tests, over every
// language: the figure to show when one is shown.
func (stats *Stats) Handwritten() int {
	total := 0
	if stats != nil {
		for _, language := range stats.Languages {
			total += language.Handwritten
		}
	}
	return total
}

var languageName = regexp.MustCompile(`^[a-z][a-z0-9+#-]*$`)

// Validate holds a Stats document to its schema: a reader of a figure that
// arrived in a release calls it before showing anything.
func (stats *Stats) Validate() error {
	if stats == nil {
		return fmt.Errorf("%w: statistics are required", ErrInvalid)
	}
	if stats.Schema != SchemaV1 {
		return fmt.Errorf("%w: schema %q (this reader reads %q)", ErrInvalid, stats.Schema, SchemaV1)
	}
	previous := ""
	for _, language := range stats.Languages {
		switch {
		case !languageName.MatchString(language.Language):
			return fmt.Errorf("%w: %q is not a language name", ErrInvalid, language.Language)
		case language.Language <= previous:
			return fmt.Errorf("%w: languages are listed once each, sorted by name; %q follows %q", ErrInvalid, language.Language, previous)
		case language.Handwritten < 0 || language.Generated < 0 || language.Tests < 0:
			return fmt.Errorf("%w: %s has a negative count", ErrInvalid, language.Language)
		case language.Handwritten+language.Generated+language.Tests == 0:
			return fmt.Errorf("%w: %s counts no line; a language with nothing counted is left out", ErrInvalid, language.Language)
		}
		previous = language.Language
	}
	return nil
}

// Options are what a caller knows about a tree that the tree does not say.
type Options struct {
	// Skip reports a path to leave out: a file, or a directory with
	// everything under it. A caller that knows what the repository tracks or
	// ignores says so here; this package reads no ignore file itself. Paths
	// are slash-separated and relative to the tree.
	Skip func(name string, dir bool) bool
	// Generated reports a file the caller knows a tool wrote, beyond what
	// its name and its head already say — a path marked linguist-generated,
	// or one a generator's own configuration lists.
	Generated func(name string) bool
}

// skippedDirs are directories that never hold a unit's own source in any
// ecosystem this package counts. They are skipped whatever
// the caller says, so two callers cannot disagree about a vendored tree.
//
// The list is only the unambiguous ones. Build output (dist, build, target)
// is left to Skip, because those are also ordinary package names and only
// the repository's ignore rules know which a given directory is.
var skippedDirs = []string{".git", "node_modules", "vendor", "__pycache__", ".venv"}

// headBytes is how much of a file is read to tell text from binary and to
// look for a generated-code marker.
const headBytes = 4 << 10

// Count walks the tree and counts its lines of code. The result is the same
// for the same tree: files are visited in name order and nothing but their
// names and bytes is read.
func Count(tree fs.FS, options Options) (*Stats, error) {
	counts := map[string]*Language{}
	err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() {
			if slices.Contains(skippedDirs, entry.Name()) || (options.Skip != nil && options.Skip(name, true)) {
				return fs.SkipDir
			}
			return nil
		}
		// Only a regular file is source: a symlink is somebody else's file
		// or the same file twice.
		if !entry.Type().IsRegular() || (options.Skip != nil && options.Skip(name, false)) {
			return nil
		}
		spoken, known := languageOf(name)
		if !known {
			return nil
		}
		file, err := tree.Open(name)
		if err != nil {
			return fmt.Errorf("source statistics: %w", err)
		}
		defer func() { _ = file.Close() }()
		reader := bufio.NewReaderSize(file, headBytes)
		head, err := reader.Peek(headBytes)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
			return fmt.Errorf("source statistics: %s: %w", name, err)
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil
		}
		// The head is the reader's own buffer and is only valid until the
		// next read, so the file is classified here, before its lines are
		// counted: read afterwards, a long file's head is whatever the
		// reader last held.
		generated := generatedName(name) || generatedHead(head) || (options.Generated != nil && options.Generated(name))
		lines, err := codeLines(reader, spoken)
		if err != nil {
			return fmt.Errorf("source statistics: %s: %w", name, err)
		}
		if lines == 0 {
			return nil
		}
		count := counts[spoken.name]
		if count == nil {
			count = &Language{Language: spoken.name}
			counts[spoken.name] = count
		}
		switch {
		case generated:
			count.Generated += lines
		case testFile(name):
			count.Tests += lines
		default:
			count.Handwritten += lines
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	stats := &Stats{Schema: SchemaV1, Languages: make([]Language, 0, len(counts))}
	for _, count := range counts {
		stats.Languages = append(stats.Languages, *count)
	}
	sort.Slice(stats.Languages, func(i, j int) bool { return stats.Languages[i].Language < stats.Languages[j].Language })
	return stats, nil
}

// codeLines counts the lines that are neither blank nor only a comment.
func codeLines(reader *bufio.Reader, spoken syntax) (int, error) {
	count, closing := 0, ""
	for {
		raw, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		line := strings.TrimSpace(raw)
		for line != "" {
			if closing != "" {
				// Inside a block comment: what follows its end is read as
				// the rest of the line.
				_, after, found := strings.Cut(line, closing)
				if !found {
					line = ""
					break
				}
				closing, line = "", strings.TrimSpace(after)
				continue
			}
			if opened, block := spoken.opens(line); opened {
				closing, line = block.close, strings.TrimSpace(line[len(block.open):])
				continue
			}
			break
		}
		// A block comment opened after code on the same line is not followed:
		// "/*" is far more often part of a glob in a string ("**/*.go") than
		// the start of a trailing comment, and following it would swallow the
		// rest of the file.
		if line != "" && !spoken.lineComment(line) {
			count++
		}
		if errors.Is(err, io.EOF) {
			return count, nil
		}
	}
}

// generatedMarkers are what generators write at the head of a file, matched
// without regard to case. A bare "do not edit" is not one: people write that
// about files they maintain by hand.
var generatedMarkers = []string{"code generated", "@generated", "autogenerated", "auto-generated", "automatically generated", "generated by"}

// markerLines is how many non-blank lines at the head of a file may carry a
// marker. A generator announces itself at the top; the same words further
// down are prose.
const markerLines = 5

func generatedHead(head []byte) bool {
	seen := 0
	for _, line := range strings.Split(string(head), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		for _, marker := range generatedMarkers {
			if strings.Contains(line, marker) {
				return true
			}
		}
		if seen++; seen == markerLines {
			break
		}
	}
	return false
}

// generatedSuffixes are the names generators give their output.
var generatedSuffixes = []string{".pb.go", ".pb.gw.go", ".connect.go", "_gen.go", ".gen.go", "_pb2.py", "_pb2_grpc.py", "_pb2.pyi", "_pb.ts", "_pb.js", "_pb.d.ts", ".gen.ts", ".generated.ts", ".generated.go", ".g.dart", ".g.cs", ".min.js", ".min.css"}

func generatedName(name string) bool {
	base := path.Base(name)
	for _, suffix := range generatedSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return slices.Contains(strings.Split(path.Dir(name), "/"), "generated")
}

// testDirs are the directories that hold tests in the ecosystems counted.
var testDirs = []string{"test", "tests", "__tests__", "testdata", "e2e", "spec"}

func testFile(name string) bool {
	base := path.Base(name)
	stem := strings.TrimSuffix(base, path.Ext(base))
	switch {
	case strings.HasSuffix(stem, "_test"), strings.HasPrefix(stem, "test_"), base == "conftest.py":
		return true
	case strings.HasSuffix(stem, ".test"), strings.HasSuffix(stem, ".spec"), strings.HasSuffix(stem, "Test"), strings.HasSuffix(stem, "Tests"):
		return true
	}
	for _, segment := range strings.Split(path.Dir(name), "/") {
		if slices.Contains(testDirs, segment) {
			return true
		}
	}
	return false
}
