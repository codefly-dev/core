package code

import (
	"os"
	"path/filepath"
	"testing"
)

type prDiffCase struct {
	File         string
	MinFiles     int // minimum number of changed files
	MinHunks     int // minimum total hunks across all files
	HasAdditions bool
}

func TestParseUnifiedDiff_RealPRs(t *testing.T) {
	cases := []prDiffCase{
		{
			File:         "chi_middleware.diff",
			MinFiles:     1,
			MinHunks:     1,
			HasAdditions: true,
		},
		{
			File:         "zerolog_feature.diff",
			MinFiles:     1,
			MinHunks:     1,
			HasAdditions: true,
		},
		{
			File:         "mux_middleware.diff",
			MinFiles:     1,
			MinHunks:     1,
			HasAdditions: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.File, func(t *testing.T) {
			path := filepath.Join("testdata", "prdiffs", tc.File)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read diff %s: %v", tc.File, err)
			}

			diffs := ParseUnifiedDiff(string(raw))

			t.Logf("%s: %d file diffs", tc.File, len(diffs))

			if len(diffs) < tc.MinFiles {
				t.Errorf("expected at least %d file diffs, got %d", tc.MinFiles, len(diffs))
			}

			totalHunks := 0
			totalAdded := 0
			totalRemoved := 0

			for _, fd := range diffs {
				if fd.OldPath == "" && fd.NewPath == "" {
					t.Error("file diff has both paths empty")
				}

				for _, h := range fd.Hunks {
					totalHunks++

					if h.OldStart <= 0 && h.NewStart <= 0 {
						t.Errorf("hunk in %s has invalid start lines: old=%d new=%d",
							fd.NewPath, h.OldStart, h.NewStart)
					}

					for _, l := range h.Lines {
						switch l.Kind {
						case DiffAdd:
							totalAdded++
							if l.NewLine <= 0 {
								t.Errorf("added line has invalid NewLine=%d", l.NewLine)
							}
						case DiffRemove:
							totalRemoved++
							if l.OldLine <= 0 {
								t.Errorf("removed line has invalid OldLine=%d", l.OldLine)
							}
						case DiffContext:
							if l.OldLine <= 0 || l.NewLine <= 0 {
								t.Errorf("context line has invalid lines: old=%d new=%d",
									l.OldLine, l.NewLine)
							}
						}
					}
				}

				t.Logf("  %s: %s", fd.NewPath, fd.Summary())
			}

			if totalHunks < tc.MinHunks {
				t.Errorf("expected at least %d hunks, got %d", tc.MinHunks, totalHunks)
			}

			if tc.HasAdditions && totalAdded == 0 {
				t.Error("expected added lines but found none")
			}

			t.Logf("  totals: %d hunks, +%d/-%d lines", totalHunks, totalAdded, totalRemoved)
		})
	}
}
