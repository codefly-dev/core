package code

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// AgeBucket classifies code age relative to a reference date.
type AgeBucket string

const (
	AgeRecent   AgeBucket = "recent"   // < 3 months
	AgeModerate AgeBucket = "moderate" // 3-12 months
	AgeOld      AgeBucket = "old"      // 1-3 years
	AgeAncient  AgeBucket = "ancient"  // > 3 years
)

// TimelineChunk groups consecutive lines from the same commit.
type TimelineChunk struct {
	StartLine int
	EndLine   int
	Hash      string
	Author    string
	Date      time.Time
	Age       AgeBucket
	Summary   string // first non-blank content line in the range
}

// FileTimeline is the temporal breakdown of a single source file.
type FileTimeline struct {
	Path   string
	Chunks []TimelineChunk
}

// Newest returns the most recent chunk date, or zero time if empty.
func (ft *FileTimeline) Newest() time.Time {
	var newest time.Time
	for _, c := range ft.Chunks {
		if c.Date.After(newest) {
			newest = c.Date
		}
	}
	return newest
}

// Oldest returns the earliest chunk date, or zero time if empty.
func (ft *FileTimeline) Oldest() time.Time {
	if len(ft.Chunks) == 0 {
		return time.Time{}
	}
	oldest := ft.Chunks[0].Date
	for _, c := range ft.Chunks[1:] {
		if c.Date.Before(oldest) {
			oldest = c.Date
		}
	}
	return oldest
}

// Lines returns total line count (EndLine of last chunk).
func (ft *FileTimeline) Lines() int {
	if len(ft.Chunks) == 0 {
		return 0
	}
	return ft.Chunks[len(ft.Chunks)-1].EndLine
}

// BuildFileTimeline groups blame lines into consecutive chunks by commit hash,
// classifies each by age relative to refDate, and extracts a summary.
func BuildFileTimeline(path string, blameLines []*codev0.GitBlameLine, refDate time.Time) *FileTimeline {
	if len(blameLines) == 0 {
		return &FileTimeline{Path: path}
	}

	var chunks []TimelineChunk
	var cur *TimelineChunk

	for _, bl := range blameLines {
		t := parseBlameDate(bl.Date)
		if cur == nil || bl.Hash != cur.Hash {
			if cur != nil {
				chunks = append(chunks, *cur)
			}
			summary := strings.TrimSpace(bl.Content)
			if summary == "" || summary == "{" || summary == "}" {
				summary = ""
			}
			cur = &TimelineChunk{
				StartLine: int(bl.Line),
				EndLine:   int(bl.Line),
				Hash:      bl.Hash,
				Author:    bl.Author,
				Date:      t,
				Age:       classifyAge(t, refDate),
				Summary:   summary,
			}
		} else {
			cur.EndLine = int(bl.Line)
			if cur.Summary == "" {
				s := strings.TrimSpace(bl.Content)
				if s != "" && s != "{" && s != "}" {
					cur.Summary = s
				}
			}
		}
	}
	if cur != nil {
		chunks = append(chunks, *cur)
	}

	return &FileTimeline{Path: path, Chunks: chunks}
}

// BuildProjectTimeline blames every source file and returns timelines sorted
// by path. It uses the VFS to discover files, skipping test files, vendor,
// and generated directories, then runs git blame directly via exec.Command.
func BuildProjectTimeline(ctx context.Context, vfs VFS, rootDir string, extensions []string, refDate time.Time) ([]*FileTimeline, error) {
	if len(extensions) == 0 {
		extensions = []string{".go"}
	}

	extSet := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		e := ext
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[e] = true
	}

	var sourceFiles []string
	walkErr := vfs.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			switch name {
			case "vendor", "testdata", "node_modules", ".git", "generated":
				return fs.SkipDir
			}
			return nil
		}
		if !extSet[filepath.Ext(path)] {
			return nil
		}
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			return nil
		}
		if shouldSkipForTimeline(rel) {
			return nil
		}
		sourceFiles = append(sourceFiles, rel)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("list files: %w", walkErr)
	}
	sort.Strings(sourceFiles)

	var timelines []*FileTimeline
	for _, path := range sourceFiles {
		lines, err := gitBlameFile(ctx, rootDir, path)
		if err != nil || len(lines) == 0 {
			continue
		}
		timelines = append(timelines, BuildFileTimeline(path, lines, refDate))
	}

	return timelines, nil
}

// gitBlameFile runs git blame --porcelain on a single file and returns parsed blame lines.
func gitBlameFile(ctx context.Context, repoDir, path string) ([]*codev0.GitBlameLine, error) {
	cmd := exec.CommandContext(ctx, "git", "blame", "--porcelain", "--", path)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git blame %s: %s", path, strings.TrimSpace(stderr.String()))
	}
	return parseGitBlame(stdout.String()), nil
}

// FormatTimeline produces a compact text representation of file timelines,
// suitable for LLM context or developer review.
func FormatTimeline(timelines []*FileTimeline) string {
	var b strings.Builder
	b.WriteString("# File Age Timeline\n\n")

	for _, ft := range timelines {
		if len(ft.Chunks) == 0 {
			continue
		}
		newest := ft.Newest().Format("2006-01-02")
		oldest := ft.Oldest().Format("2006-01-02")
		b.WriteString(fmt.Sprintf("## %s (%d chunks, newest: %s, oldest: %s)\n",
			ft.Path, len(ft.Chunks), newest, oldest))

		for _, c := range ft.Chunks {
			lineRange := fmt.Sprintf("L%d-%d", c.StartLine, c.EndLine)
			summary := c.Summary
			if len(summary) > 60 {
				summary = summary[:57] + "..."
			}
			author := truncAuthor(c.Author, 18)
			b.WriteString(fmt.Sprintf("  %-12s [%-8s] %s  %-18s  %q\n",
				lineRange, c.Age, c.Date.Format("2006-01-02"), author, summary))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// TimelineStats summarizes the temporal distribution of a project's code.
type TimelineStats struct {
	TotalLines  int
	TotalFiles  int
	TotalChunks int

	LinesByAge map[AgeBucket]int // lines per bucket

	NewestFile string    // most recently modified file
	NewestDate time.Time // date of most recent change
	OldestFile string    // oldest untouched file
	OldestDate time.Time // date of oldest change

	Hotspots []HotspotFile // files with most distinct chunks (frequently patched)
}

// HotspotFile tracks files with many distinct commit chunks.
type HotspotFile struct {
	Path   string
	Chunks int
}

// ComputeTimelineStats derives project-wide statistics from a set of timelines.
func ComputeTimelineStats(timelines []*FileTimeline) TimelineStats {
	stats := TimelineStats{
		LinesByAge: make(map[AgeBucket]int),
	}

	type fileAge struct {
		path   string
		newest time.Time
		oldest time.Time
	}
	var fileAges []fileAge

	for _, ft := range timelines {
		if len(ft.Chunks) == 0 {
			continue
		}
		stats.TotalFiles++
		stats.TotalChunks += len(ft.Chunks)

		for _, c := range ft.Chunks {
			lineCount := c.EndLine - c.StartLine + 1
			stats.TotalLines += lineCount
			stats.LinesByAge[c.Age] += lineCount
		}

		fileAges = append(fileAges, fileAge{
			path: ft.Path, newest: ft.Newest(), oldest: ft.Oldest(),
		})

		stats.Hotspots = append(stats.Hotspots, HotspotFile{
			Path: ft.Path, Chunks: len(ft.Chunks),
		})
	}

	sort.Slice(stats.Hotspots, func(i, j int) bool {
		return stats.Hotspots[i].Chunks > stats.Hotspots[j].Chunks
	})
	if len(stats.Hotspots) > 10 {
		stats.Hotspots = stats.Hotspots[:10]
	}

	if len(fileAges) > 0 {
		sort.Slice(fileAges, func(i, j int) bool {
			return fileAges[i].newest.After(fileAges[j].newest)
		})
		stats.NewestFile = fileAges[0].path
		stats.NewestDate = fileAges[0].newest

		sort.Slice(fileAges, func(i, j int) bool {
			return fileAges[i].oldest.Before(fileAges[j].oldest)
		})
		stats.OldestFile = fileAges[0].path
		stats.OldestDate = fileAges[0].oldest
	}

	return stats
}

// FormatTimelineStats produces a readable summary.
func FormatTimelineStats(s TimelineStats) string {
	var b strings.Builder
	b.WriteString("# Timeline Summary\n\n")
	b.WriteString(fmt.Sprintf("Files: %d, Lines: %d, Chunks: %d\n\n", s.TotalFiles, s.TotalLines, s.TotalChunks))

	b.WriteString("Age Distribution:\n")
	for _, bucket := range []AgeBucket{AgeRecent, AgeModerate, AgeOld, AgeAncient} {
		lines := s.LinesByAge[bucket]
		pct := 0.0
		if s.TotalLines > 0 {
			pct = float64(lines) / float64(s.TotalLines) * 100
		}
		b.WriteString(fmt.Sprintf("  %-10s %5d lines (%5.1f%%)\n", bucket, lines, pct))
	}

	if s.NewestFile != "" {
		b.WriteString(fmt.Sprintf("\nMost recent: %s (%s)\n", s.NewestFile, s.NewestDate.Format("2006-01-02")))
	}
	if s.OldestFile != "" {
		b.WriteString(fmt.Sprintf("Oldest code: %s (%s)\n", s.OldestFile, s.OldestDate.Format("2006-01-02")))
	}

	if len(s.Hotspots) > 0 {
		b.WriteString("\nHotspots (most commit chunks):\n")
		for _, h := range s.Hotspots {
			b.WriteString(fmt.Sprintf("  %s: %d chunks\n", h.Path, h.Chunks))
		}
	}

	return b.String()
}

// --- helpers ---

func parseBlameDate(raw string) time.Time {
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(unix, 0).UTC()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05-07:00", raw); err == nil {
		return t
	}
	return time.Time{}
}

func classifyAge(commitDate, refDate time.Time) AgeBucket {
	age := refDate.Sub(commitDate)
	switch {
	case age < 3*30*24*time.Hour:
		return AgeRecent
	case age < 12*30*24*time.Hour:
		return AgeModerate
	case age < 3*365*24*time.Hour:
		return AgeOld
	default:
		return AgeAncient
	}
}

func shouldSkipForTimeline(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	parts := strings.Split(path, "/")
	for _, p := range parts {
		switch p {
		case "vendor", "testdata", "node_modules", ".git", "generated":
			return true
		}
	}
	return false
}

func truncAuthor(name string, max int) string {
	if len(name) <= max {
		return name
	}
	return name[:max-1] + "."
}
