package code

import (
	"context"
	"testing"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

var refDate = time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC)

func TestBuildFileTimeline_ChunkGrouping(t *testing.T) {
	lines := []*codev0.GitBlameLine{
		{Hash: "aaa", Author: "Alice", Date: "1700000000", Line: 1, Content: "package main"},
		{Hash: "aaa", Author: "Alice", Date: "1700000000", Line: 2, Content: ""},
		{Hash: "aaa", Author: "Alice", Date: "1700000000", Line: 3, Content: "import \"fmt\""},
		{Hash: "bbb", Author: "Bob", Date: "1710000000", Line: 4, Content: "func main() {"},
		{Hash: "bbb", Author: "Bob", Date: "1710000000", Line: 5, Content: "  fmt.Println(\"hello\")"},
		{Hash: "bbb", Author: "Bob", Date: "1710000000", Line: 6, Content: "}"},
		{Hash: "aaa", Author: "Alice", Date: "1700000000", Line: 7, Content: "// footer"},
	}

	ft := BuildFileTimeline("main.go", lines, refDate)
	if ft.Path != "main.go" {
		t.Errorf("path = %q", ft.Path)
	}
	if len(ft.Chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(ft.Chunks))
	}

	c0 := ft.Chunks[0]
	if c0.StartLine != 1 || c0.EndLine != 3 {
		t.Errorf("chunk0 lines: %d-%d", c0.StartLine, c0.EndLine)
	}
	if c0.Hash != "aaa" || c0.Author != "Alice" {
		t.Errorf("chunk0: hash=%s author=%s", c0.Hash, c0.Author)
	}
	if c0.Summary != "package main" {
		t.Errorf("chunk0 summary = %q", c0.Summary)
	}

	c1 := ft.Chunks[1]
	if c1.StartLine != 4 || c1.EndLine != 6 {
		t.Errorf("chunk1 lines: %d-%d", c1.StartLine, c1.EndLine)
	}
	if c1.Hash != "bbb" {
		t.Errorf("chunk1 hash = %q", c1.Hash)
	}
	if c1.Summary != "func main() {" {
		t.Errorf("chunk1 summary = %q", c1.Summary)
	}

	c2 := ft.Chunks[2]
	if c2.StartLine != 7 || c2.EndLine != 7 {
		t.Errorf("chunk2 lines: %d-%d", c2.StartLine, c2.EndLine)
	}
}

func TestBuildFileTimeline_SkipsBraces(t *testing.T) {
	lines := []*codev0.GitBlameLine{
		{Hash: "aaa", Author: "A", Date: "1700000000", Line: 1, Content: "{"},
		{Hash: "aaa", Author: "A", Date: "1700000000", Line: 2, Content: "}"},
		{Hash: "aaa", Author: "A", Date: "1700000000", Line: 3, Content: "real code"},
	}
	ft := BuildFileTimeline("f.go", lines, refDate)
	if ft.Chunks[0].Summary != "real code" {
		t.Errorf("summary should skip braces, got %q", ft.Chunks[0].Summary)
	}
}

func TestBuildFileTimeline_Empty(t *testing.T) {
	ft := BuildFileTimeline("empty.go", nil, refDate)
	if len(ft.Chunks) != 0 {
		t.Error("expected no chunks for empty blame")
	}
	if ft.Lines() != 0 {
		t.Error("expected 0 lines")
	}
}

func TestClassifyAge(t *testing.T) {
	ref := time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		date time.Time
		want AgeBucket
	}{
		{ref.Add(-24 * time.Hour), AgeRecent},
		{ref.Add(-60 * 24 * time.Hour), AgeRecent},
		{ref.Add(-100 * 24 * time.Hour), AgeModerate},
		{ref.Add(-300 * 24 * time.Hour), AgeModerate},
		{ref.Add(-500 * 24 * time.Hour), AgeOld},
		{ref.Add(-2 * 365 * 24 * time.Hour), AgeOld},
		{ref.Add(-4 * 365 * 24 * time.Hour), AgeAncient},
		{ref.Add(-10 * 365 * 24 * time.Hour), AgeAncient},
	}
	for _, tt := range tests {
		got := classifyAge(tt.date, ref)
		if got != tt.want {
			t.Errorf("classifyAge(%s) = %s, want %s", tt.date.Format("2006-01-02"), got, tt.want)
		}
	}
}

func TestFileTimeline_NewestOldest(t *testing.T) {
	t1 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	ft := &FileTimeline{
		Path: "test.go",
		Chunks: []TimelineChunk{
			{StartLine: 1, EndLine: 10, Date: t1},
			{StartLine: 11, EndLine: 20, Date: t2},
		},
	}
	if !ft.Newest().Equal(t2) {
		t.Errorf("newest = %v, want %v", ft.Newest(), t2)
	}
	if !ft.Oldest().Equal(t1) {
		t.Errorf("oldest = %v, want %v", ft.Oldest(), t1)
	}
	if ft.Lines() != 20 {
		t.Errorf("lines = %d", ft.Lines())
	}
}

func TestShouldSkipForTimeline(t *testing.T) {
	tests := []struct {
		path string
		skip bool
	}{
		{"color.go", false},
		{"color_test.go", true},
		{"vendor/lib/x.go", true},
		{"internal/generated/foo.go", true},
		{"cmd/main.go", false},
		{"testdata/fixture.go", true},
	}
	for _, tt := range tests {
		got := shouldSkipForTimeline(tt.path)
		if got != tt.skip {
			t.Errorf("shouldSkipForTimeline(%q) = %v, want %v", tt.path, got, tt.skip)
		}
	}
}

// --- Real repo tests ---

func TestBuildProjectTimeline_RealRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			timelines, err := BuildProjectTimeline(ctx, srv, []string{".go"}, refDate)
			if err != nil {
				t.Fatal(err)
			}
			if len(timelines) == 0 {
				t.Fatal("no timelines produced")
			}

			for _, ft := range timelines {
				if len(ft.Chunks) == 0 {
					t.Errorf("%s: empty timeline", ft.Path)
				}
				for _, c := range ft.Chunks {
					if c.Hash == "" {
						t.Errorf("%s L%d: empty hash", ft.Path, c.StartLine)
					}
					if c.Date.IsZero() {
						t.Errorf("%s L%d: zero date", ft.Path, c.StartLine)
					}
					if c.StartLine > c.EndLine {
						t.Errorf("%s: start %d > end %d", ft.Path, c.StartLine, c.EndLine)
					}
				}
			}

			stats := ComputeTimelineStats(timelines)
			if stats.TotalFiles == 0 {
				t.Error("TotalFiles is zero")
			}
			if stats.TotalLines == 0 {
				t.Error("TotalLines is zero")
			}
			if stats.TotalChunks == 0 {
				t.Error("TotalChunks is zero")
			}
			if stats.NewestFile == "" {
				t.Error("no newest file")
			}
			if stats.OldestFile == "" {
				t.Error("no oldest file")
			}

			totalBucketed := 0
			for _, v := range stats.LinesByAge {
				totalBucketed += v
			}
			if totalBucketed != stats.TotalLines {
				t.Errorf("bucket lines %d != total %d", totalBucketed, stats.TotalLines)
			}

			formatted := FormatTimeline(timelines)
			if len(formatted) == 0 {
				t.Error("empty formatted timeline")
			}

			statsText := FormatTimelineStats(stats)
			if len(statsText) == 0 {
				t.Error("empty stats text")
			}

			t.Logf("%s: %d files, %d lines, %d chunks", repo.Name, stats.TotalFiles, stats.TotalLines, stats.TotalChunks)
			t.Logf("  age distribution: recent=%d moderate=%d old=%d ancient=%d",
				stats.LinesByAge[AgeRecent], stats.LinesByAge[AgeModerate],
				stats.LinesByAge[AgeOld], stats.LinesByAge[AgeAncient])
			t.Logf("  newest: %s (%s)", stats.NewestFile, stats.NewestDate.Format("2006-01-02"))
			t.Logf("  oldest: %s (%s)", stats.OldestFile, stats.OldestDate.Format("2006-01-02"))
			if len(stats.Hotspots) > 0 {
				t.Logf("  top hotspot: %s (%d chunks)", stats.Hotspots[0].Path, stats.Hotspots[0].Chunks)
			}
		})
	}
}

func TestBuildProjectTimeline_SingleFile(t *testing.T) {
	repo := AllTestRepos()[0] // fatih/color
	dir := EnsureRepo(t, repo)
	srv := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GitBlame{GitBlame: &codev0.GitBlameRequest{Path: "color.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	br := resp.GetGitBlame()
	if br.Error != "" {
		t.Fatalf("blame error: %s", br.Error)
	}

	ft := BuildFileTimeline("color.go", br.Lines, refDate)
	if len(ft.Chunks) == 0 {
		t.Fatal("no chunks")
	}

	prev := 0
	for _, c := range ft.Chunks {
		if c.StartLine <= prev {
			t.Errorf("non-monotonic: chunk starts at %d after previous ended at %d", c.StartLine, prev)
		}
		prev = c.EndLine
	}

	t.Logf("color.go: %d chunks, %d lines, newest=%s, oldest=%s",
		len(ft.Chunks), ft.Lines(),
		ft.Newest().Format("2006-01-02"), ft.Oldest().Format("2006-01-02"))
	for _, c := range ft.Chunks {
		t.Logf("  L%d-%d [%s] %s %s %q",
			c.StartLine, c.EndLine, c.Age, c.Date.Format("2006-01-02"), c.Author, c.Summary)
	}
}

func TestFormatTimeline_Output(t *testing.T) {
	repo := AllTestRepos()[0]
	dir := EnsureRepo(t, repo)
	srv := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	timelines, err := BuildProjectTimeline(ctx, srv, []string{".go"}, refDate)
	if err != nil {
		t.Fatal(err)
	}

	formatted := FormatTimeline(timelines)
	if !containsStr(formatted, "# File Age Timeline") {
		t.Error("missing header")
	}
	if !containsStr(formatted, "color.go") {
		t.Error("missing color.go in output")
	}
	if !containsStr(formatted, "recent") || !containsStr(formatted, "ancient") ||
		!containsStr(formatted, "old") || !containsStr(formatted, "moderate") {
		t.Logf("output may be missing some buckets, checking...")
	}

	t.Logf("formatted output: %d bytes", len(formatted))
}

func containsStr(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
