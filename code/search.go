package code

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var errSearchStop = errors.New("search: max results reached")

// SearchOpts configures a text search.
type SearchOpts struct {
	Pattern         string
	Literal         bool
	CaseInsensitive bool
	Path            string   // subdirectory (relative to root)
	Extensions      []string // e.g. [".go", ".py"]
	Exclude         []string // glob patterns
	MaxResults      int      // 0 = 100
	ContextLines    int
}

// SearchMatch is one search result.
type SearchMatch struct {
	File string // relative to root
	Line int
	Text string
}

// SearchResult holds all matches.
type SearchResult struct {
	Matches   []SearchMatch
	Truncated bool
}

// Search runs ripgrep on a local directory.
func Search(ctx context.Context, root string, opts SearchOpts) (*SearchResult, error) {
	searchDir := root
	if opts.Path != "" {
		searchDir = filepath.Join(root, opts.Path)
	}
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}

	args := []string{"rg", "--line-number", "--no-heading", "--color=never"}
	if opts.Literal {
		args = append(args, "--fixed-strings")
	}
	if opts.CaseInsensitive {
		args = append(args, "--ignore-case")
	}
	if opts.ContextLines > 0 {
		args = append(args, fmt.Sprintf("--context=%d", opts.ContextLines))
	}
	for _, ext := range opts.Extensions {
		e := strings.TrimPrefix(ext, ".")
		args = append(args, "--type-add", fmt.Sprintf("custom:*.%s", e), "--type", "custom")
	}
	for _, excl := range opts.Exclude {
		args = append(args, "--glob", "!"+excl)
	}
	args = append(args, fmt.Sprintf("--max-count=%d", maxResults))
	args = append(args, opts.Pattern, searchDir)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, _ := cmd.Output()

	matches, truncated := parseOutput(string(out), root, maxResults)
	return &SearchResult{Matches: matches, Truncated: truncated}, nil
}

// SearchVFS performs regex-based text search over a VFS. Used when the
// filesystem is non-local (MemoryVFS, OverlayVFS) and ripgrep can't run.
func SearchVFS(_ context.Context, vfs VFS, root string, opts SearchOpts) (*SearchResult, error) {
	searchDir := root
	if opts.Path != "" {
		searchDir = filepath.Join(root, opts.Path)
	}
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}

	pattern := opts.Pattern
	if opts.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	flags := ""
	if opts.CaseInsensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern %q: %w", opts.Pattern, err)
	}

	extSet := make(map[string]bool, len(opts.Extensions))
	for _, ext := range opts.Extensions {
		e := ext
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[e] = true
	}

	var matches []SearchMatch
	truncated := false

	walkErr := vfs.WalkDir(searchDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if len(extSet) > 0 && !extSet[filepath.Ext(path)] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, excl := range opts.Exclude {
			if matched, _ := filepath.Match(excl, filepath.Base(path)); matched {
				return nil
			}
			if matched, _ := filepath.Match(excl, rel); matched {
				return nil
			}
		}
		data, readErr := vfs.ReadFile(path)
		if readErr != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				matches = append(matches, SearchMatch{File: rel, Line: i + 1, Text: line})
				if len(matches) >= maxResults {
					truncated = true
					return errSearchStop
				}
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errSearchStop) {
		return nil, fmt.Errorf("walk %s: %w", searchDir, walkErr)
	}

	return &SearchResult{Matches: matches, Truncated: truncated}, nil
}

func parseOutput(output, root string, maxResults int) ([]SearchMatch, bool) {
	var matches []SearchMatch
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" || line == "--" {
			continue
		}
		if len(matches) >= maxResults {
			return matches, true
		}
		file, lineNo, text := parseGrepLine(line)
		if file == "" {
			continue
		}
		if rel, err := filepath.Rel(root, file); err == nil {
			file = rel
		}
		matches = append(matches, SearchMatch{File: file, Line: lineNo, Text: text})
	}
	return matches, false
}

func parseGrepLine(line string) (string, int, string) {
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 3 {
		return "", 0, ""
	}
	n := 0
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return "", 0, ""
		}
		n = n*10 + int(c-'0')
	}
	return parts[0], n, parts[2]
}
