package code

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var errSearchStop = errors.New("search: result budget reached")

// DefaultSearchMaxBytes is the cumulative match-text budget used when callers
// do not declare a tighter SearchOpts.MaxBytes boundary.
const DefaultSearchMaxBytes = 64 * 1024

// SearchTruncationReason identifies the first deterministic result boundary
// reached by a search. Match-count and byte budgets are separate because a
// handful of matches from generated or minified files can be much larger than
// hundreds of ordinary source lines.
type SearchTruncationReason uint8

const (
	SearchTruncationNone SearchTruncationReason = iota
	SearchTruncationMaxResults
	SearchTruncationMaxBytes
)

// SearchOpts configures a text search.
type SearchOpts struct {
	Pattern         string
	Literal         bool
	CaseInsensitive bool
	Path            string   // subdirectory (relative to root)
	Extensions      []string // e.g. [".go", ".py"]
	Exclude         []string // glob patterns
	MaxResults      int      // 0 = 100
	MaxBytes        int      // cumulative returned match-text bytes; 0 = 64 KiB
	ContextLines    int
}

// SearchMatch is one search result.
type SearchMatch struct {
	File string // relative to root
	Line int
	Text string
	// TextTruncated reports that Text is a UTF-8-safe prefix selected to fit the
	// remaining result byte budget.
	TextTruncated bool
	// OriginalTextBytes is the exact byte length before result-budget truncation.
	OriginalTextBytes int
}

// SearchResult holds all matches.
type SearchResult struct {
	// Matches is the deterministic bounded prefix of matching source lines.
	Matches []SearchMatch
	// Truncated reports whether a count or byte boundary stopped collection.
	Truncated bool
	// TruncationReason identifies the first boundary reached.
	TruncationReason SearchTruncationReason
	// ReturnedTextBytes is the cumulative byte length of Matches.Text.
	ReturnedTextBytes int
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

	// --sort=path forces ripgrep to single-threaded mode and emits
	// matches in lexicographic path order. We pay a small perf cost
	// but get a deterministic match prefix, which is critical when
	// downstream truncates to --max-count and feeds the result into
	// a cassette-keyed LLM prompt (Mind tests). Without --sort, the
	// parallel walker emits matches in unpredictable order and
	// truncation produces a different 50-line slice on every run.
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("ripgrep is required for local search: %w", err)
	}
	args := []string{"--line-number", "--no-heading", "--color=never", "--sort=path"}
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

	cmd := exec.CommandContext(ctx, rgPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("ripgrep search canceled: %w", ctxErr)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("ripgrep search failed: %w", err)
		}
		if exitErr.ExitCode() != 1 {
			message := strings.TrimSpace(string(exitErr.Stderr))
			if message != "" {
				return nil, fmt.Errorf("ripgrep search failed: %s: %w", message, err)
			}
			return nil, fmt.Errorf("ripgrep search failed: %w", err)
		}
		// ripgrep exit code 1 means a successful search with no matches.
	}

	matches, reason, returnedBytes := parseOutput(string(out), root, maxResults, searchMaxBytes(opts.MaxBytes))
	// Ripgrep runs file traversal in parallel workers and emits matches
	// as they're found, so the natural output order varies across runs.
	// Deterministic order (path, line) is critical for cassette replay
	// in Mind tests — without it, the LLM prompt that quotes search
	// results hashes differently between record and replay sessions.
	// Stable-sort here keeps within-file line ordering intact when
	// ripgrep happens to emit them sorted; cross-file order becomes
	// path-alphabetical.
	// Defense-in-depth: ripgrep with --sort=path returns matches in
	// path order, but stale binaries or unusual builds may not honor
	// it. Re-sort in Go to guarantee determinism even when the flag
	// is silently ignored.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	return &SearchResult{
		Matches: matches, Truncated: reason != SearchTruncationNone,
		TruncationReason: reason, ReturnedTextBytes: returnedBytes,
	}, nil
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
	collector := newSearchResultCollector(maxResults, opts.MaxBytes)

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
				if collector.append(rel, i+1, line) {
					return errSearchStop
				}
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errSearchStop) {
		return nil, fmt.Errorf("walk %s: %w", searchDir, walkErr)
	}

	return collector.result(), nil
}

func parseOutput(output, root string, maxResults, maxBytes int) ([]SearchMatch, SearchTruncationReason, int) {
	collector := newSearchResultCollector(maxResults, maxBytes)
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" || line == "--" {
			continue
		}
		file, lineNo, text := parseGrepLine(line)
		if file == "" {
			continue
		}
		if rel, err := filepath.Rel(root, file); err == nil {
			file = rel
		}
		if collector.append(file, lineNo, text) {
			break
		}
	}
	result := collector.result()
	return result.Matches, result.TruncationReason, result.ReturnedTextBytes
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

// SearchTrigram performs trigram-accelerated search. The index narrows candidates
// to files containing the query's trigrams, then regex matches only those files.
// Falls back to SearchVFS if trigrams can't be extracted (e.g., pure wildcard pattern).
func SearchTrigram(_ context.Context, vfs VFS, idx *TrigramIndex, root string, opts SearchOpts) (*SearchResult, error) {
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}
	collector := newSearchResultCollector(maxResults, opts.MaxBytes)

	// Build regex for verification.
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

	// Query trigram index for candidate files.
	queryPattern := opts.Pattern
	if opts.Literal {
		queryPattern = opts.Pattern // literal, not regex-escaped
	}
	candidates := idx.Query(queryPattern)
	if candidates == nil {
		return &SearchResult{}, nil
	}
	// Query is sorted too, but retain this boundary guarantee if another index
	// implementation is introduced later. Truncation must select a stable prefix.
	sort.Strings(candidates)

	// Filter candidates by path and extension.
	extSet := make(map[string]bool, len(opts.Extensions))
	for _, ext := range opts.Extensions {
		e := ext
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[e] = true
	}

	for _, relPath := range candidates {
		if opts.Path != "" && !strings.HasPrefix(relPath, opts.Path) {
			continue
		}
		if len(extSet) > 0 && !extSet[filepath.Ext(relPath)] {
			continue
		}

		absPath := filepath.Join(root, relPath)
		data, readErr := vfs.ReadFile(absPath)
		if readErr != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				if collector.append(relPath, i+1, strings.TrimSpace(line)) {
					return collector.result(), nil
				}
			}
		}
	}

	return collector.result(), nil
}

type searchResultCollector struct {
	maxResults int
	maxBytes   int
	matches    []SearchMatch
	usedBytes  int
	reason     SearchTruncationReason
}

func newSearchResultCollector(maxResults, maxBytes int) *searchResultCollector {
	if maxResults <= 0 {
		maxResults = 100
	}
	return &searchResultCollector{maxResults: maxResults, maxBytes: searchMaxBytes(maxBytes)}
}

// append adds one match and reports whether search must stop. A partial final
// match preserves its file and line while making the byte truncation explicit;
// callers never receive an unmarked oversized line.
func (c *searchResultCollector) append(file string, line int, text string) bool {
	if c.reason != SearchTruncationNone {
		return true
	}
	if len(c.matches) >= c.maxResults {
		c.reason = SearchTruncationMaxResults
		return true
	}
	remaining := c.maxBytes - c.usedBytes
	if remaining <= 0 {
		c.reason = SearchTruncationMaxBytes
		return true
	}
	originalBytes := len(text)
	match := SearchMatch{File: file, Line: line, Text: text, OriginalTextBytes: originalBytes}
	if originalBytes > remaining {
		match.Text = utf8SafePrefix(text, remaining)
		match.TextTruncated = true
		c.matches = append(c.matches, match)
		c.usedBytes += len(match.Text)
		c.reason = SearchTruncationMaxBytes
		return true
	}
	c.matches = append(c.matches, match)
	c.usedBytes += originalBytes
	return false
}

func (c *searchResultCollector) result() *SearchResult {
	return &SearchResult{
		Matches:           append([]SearchMatch(nil), c.matches...),
		Truncated:         c.reason != SearchTruncationNone,
		TruncationReason:  c.reason,
		ReturnedTextBytes: c.usedBytes,
	}
}

func searchMaxBytes(configured int) int {
	if configured <= 0 {
		return DefaultSearchMaxBytes
	}
	return configured
}

func utf8SafePrefix(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	prefix := text[:end]
	if utf8.ValidString(prefix) {
		return prefix
	}
	// Project files can contain malformed byte sequences. Protobuf strings must
	// remain valid UTF-8, and dropping malformed runes cannot exceed the budget.
	return strings.ToValidUTF8(prefix, "")
}
