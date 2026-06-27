package code

import (
	"context"
	"strings"
	"testing"
)

type repoObjective struct {
	Query         string
	ExpectedFiles []string
}

var objectives = map[string]repoObjective{
	"fatih_color": {
		Query:         "add HiRedBg function that prints with hi-intensity red background",
		ExpectedFiles: []string{"color.go"},
	},
	"mitchellh_mapstructure": {
		Query:         "support decoding into embedded struct fields",
		ExpectedFiles: []string{"mapstructure.go"},
	},
	"tidwall_gjson": {
		Query:         "add method to parse nested JSON arrays efficiently",
		ExpectedFiles: []string{"gjson.go"},
	},
	"spf13_pflag": {
		Query:         "add Duration flag type with shorthand",
		ExpectedFiles: []string{"flag.go"},
	},
	"rs_zerolog": {
		Query:         "add Uint8 method to Event for logging uint8 values",
		ExpectedFiles: []string{"event.go"},
	},
	"go_chi_chi": {
		Query:         "add route group with pattern matching on the Mux router",
		ExpectedFiles: []string{"mux.go", "chi.go", "tree.go"},
	},
	"gorilla_mux": {
		Query:         "add Methods helper that registers multiple HTTP methods at once",
		ExpectedFiles: []string{"mux.go", "route.go"},
	},
	"charmbracelet_lipgloss": {
		Query:         "add border style rendering with custom characters",
		ExpectedFiles: []string{"style.go", "borders.go"},
	},
}

func TestBuildCodebaseContext_RepresentativeRepos(t *testing.T) {
	for _, repo := range representativeOperationalRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			cc, err := BuildCodebaseContext(ctx, srv)
			if err != nil {
				t.Fatalf("BuildCodebaseContext: %v", err)
			}

			if cc.Module == "" {
				t.Error("Module is empty")
			}
			if cc.Language != "go" {
				t.Errorf("Language = %q, want go", cc.Language)
			}
			if len(cc.Packages) == 0 {
				t.Fatal("no packages")
			}
			if cc.DepGraph == nil {
				t.Fatal("DepGraph is nil")
			}
			if len(cc.Timelines) == 0 {
				t.Error("no timelines")
			}
			if cc.Stats.TotalFiles == 0 {
				t.Error("stats TotalFiles = 0")
			}

			t.Logf("module=%s packages=%d timeline_files=%d",
				cc.Module, len(cc.Packages), cc.Stats.TotalFiles)
		})
	}
}

func TestCodebaseContext_Format_OperationalOnly(t *testing.T) {
	repo := AllTestRepos()[0]
	dir := EnsureRepo(t, repo)
	srv := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	cc, err := BuildCodebaseContext(ctx, srv)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}

	full := cc.Format(0)
	if len(full) == 0 {
		t.Fatal("Format(0) produced empty output")
	}
	if !strings.Contains(full, cc.Module) {
		t.Error("formatted output missing module name")
	}
	if strings.Contains(full, "Code Map") || strings.Contains(full, "Call Graph") {
		t.Error("formatted output includes semantic sections that belong in Mind")
	}

	budgeted := cc.Format(2000)
	if len(budgeted) > 2000 {
		t.Errorf("Format(2000) produced %d bytes, exceeds budget", len(budgeted))
	}
}

func TestRelevanceScoring_LightweightRepos(t *testing.T) {
	for _, repo := range lightweightOperationalRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			cc, err := BuildCodebaseContext(ctx, srv)
			if err != nil {
				t.Fatalf("BuildCodebaseContext: %v", err)
			}

			obj, ok := objectives[repo.Name]
			if !ok {
				t.Fatalf("no objective defined for repo %q", repo.Name)
			}

			scorer := NewRelevanceScorer(cc, srv.GetVFS(), srv.GetSourceDir())
			files := cc.FilePaths()
			if len(files) == 0 {
				t.Fatal("no files to score")
			}

			topK := scorer.TopK(ctx, obj.Query, files, 5)
			if len(topK) == 0 {
				t.Fatal("TopK returned no results")
			}

			topPaths := make([]string, len(topK))
			for i, sf := range topK {
				topPaths[i] = sf.Path
				t.Logf("  #%d %s score=%.3f search=%d recent=%d importers=%d",
					i+1, sf.Path, sf.Score, sf.SearchHits, sf.RecentLines, sf.Importers)
			}

			found := false
			for _, expected := range obj.ExpectedFiles {
				for _, tp := range topPaths {
					if strings.Contains(tp, expected) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Errorf("none of expected files %v found in top-%d: %v", obj.ExpectedFiles, len(topK), topPaths)
			}
		})
	}
}

func TestRelevanceScorer_Signals(t *testing.T) {
	repo := AllTestRepos()[0]
	dir := EnsureRepo(t, repo)
	srv := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	cc, err := BuildCodebaseContext(ctx, srv)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}

	scorer := NewRelevanceScorer(cc, srv.GetVFS(), srv.GetSourceDir())
	files := cc.FilePaths()
	all := scorer.ScoreFiles(ctx, "Color New Sprintf", files)

	for i := 1; i < len(all); i++ {
		if all[i].Score > all[i-1].Score {
			t.Errorf("results not sorted: index %d (%.3f) > index %d (%.3f)", i, all[i].Score, i-1, all[i-1].Score)
		}
	}
	if len(all) > 0 && all[0].Score <= 0 {
		t.Error("top result has zero score")
	}
	hasNonZeroSearch := false
	for _, sf := range all {
		if sf.SearchHits > 0 {
			hasNonZeroSearch = true
			break
		}
	}
	if !hasNonZeroSearch {
		t.Error("no files with search hits")
	}
}
