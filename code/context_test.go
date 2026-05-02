package code

import (
	"context"
	"strings"
	"testing"
)

// repoObjective defines a query and what files/symbols should rank highly.
type repoObjective struct {
	Query           string
	ExpectedFiles   []string // at least one must appear in top-K
	ExpectedSymbols []string // at least one must appear in the formatted context
}

var objectives = map[string]repoObjective{
	"fatih_color": {
		Query:           "add HiRedBg function that prints with hi-intensity red background",
		ExpectedFiles:   []string{"color.go"},
		ExpectedSymbols: []string{"Color", "New"},
	},
	"mitchellh_mapstructure": {
		Query:           "support decoding into embedded struct fields",
		ExpectedFiles:   []string{"mapstructure.go"},
		ExpectedSymbols: []string{"Decode", "DecoderConfig"},
	},
	"tidwall_gjson": {
		Query:           "add method to parse nested JSON arrays efficiently",
		ExpectedFiles:   []string{"gjson.go"},
		ExpectedSymbols: []string{"Get", "Result"},
	},
	"spf13_pflag": {
		Query:           "add Duration flag type with shorthand",
		ExpectedFiles:   []string{"flag.go"},
		ExpectedSymbols: []string{"FlagSet"},
	},
	"rs_zerolog": {
		Query:           "add Uint8 method to Event for logging uint8 values",
		ExpectedFiles:   []string{"event.go"},
		ExpectedSymbols: []string{"Event"},
	},
	"go_chi_chi": {
		Query:           "add route group with pattern matching on the Mux router",
		ExpectedFiles:   []string{"mux.go", "chi.go", "tree.go"},
		ExpectedSymbols: []string{"Mux"},
	},
	"gorilla_mux": {
		Query:           "add Methods helper that registers multiple HTTP methods at once",
		ExpectedFiles:   []string{"mux.go", "route.go"},
		ExpectedSymbols: []string{"Router", "Route"},
	},
	"charmbracelet_lipgloss": {
		Query: "add border style rendering with custom characters",
		// `borders.go` is the file actually defining BorderStyle, custom
		// borders, and the rendering helpers — directly named by every
		// noun in the query. style.go declares the broader Style type
		// the borders attach to. Either match counts as success; the
		// scorer doesn't have to put style.go above the get/set/unset
		// trio that incidentally has higher search-token density.
		ExpectedFiles:   []string{"style.go", "borders.go"},
		ExpectedSymbols: []string{"Style"},
	},
}

func TestBuildCodebaseContext_AllRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
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
			if cc.CodeMap == nil {
				t.Fatal("CodeMap is nil")
			}
			if len(cc.CodeMap.Files) == 0 {
				t.Error("CodeMap has no files")
			}
			if cc.DepGraph == nil {
				t.Fatal("DepGraph is nil")
			}
			if cc.Graph == nil {
				t.Error("Graph is nil (AST parse may have failed)")
			}
			if len(cc.Timelines) == 0 {
				t.Error("no timelines")
			}
			if cc.Stats.TotalFiles == 0 {
				t.Error("stats TotalFiles = 0")
			}

			t.Logf("module=%s packages=%d codemap_files=%d codemap_symbols=%d graph_nodes=%d timeline_files=%d",
				cc.Module, len(cc.Packages), len(cc.CodeMap.Files), cc.CodeMap.Stats().Symbols,
				len(cc.Graph.Nodes), cc.Stats.TotalFiles)
		})
	}
}

func TestCodebaseContext_Format(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
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
			if !strings.Contains(full, "Code Map") {
				t.Error("formatted output missing Code Map section")
			}

			budgeted := cc.Format(2000)
			if len(budgeted) > 2000 {
				t.Errorf("Format(2000) produced %d bytes, exceeds budget", len(budgeted))
			}
			if len(budgeted) == 0 {
				t.Error("Format(2000) produced empty output")
			}

			t.Logf("full=%d bytes, budgeted(2000)=%d bytes", len(full), len(budgeted))
		})
	}
}

func TestRelevanceScoring_AllRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
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
				t.Fatalf("no objective defined for repo %q — add it to objectives map or remove from AllTestRepos()", repo.Name)
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

			t.Log("--- Relevance top 5 ---")
			for i, sf := range topK {
				t.Logf("  #%d %s  score=%.3f  search=%d sym=%d callers=%d recent=%d importers=%d",
					i+1, sf.Path, sf.Score, sf.SearchHits, sf.SymbolHits, sf.Callers, sf.RecentLines, sf.Importers)
			}

			topPaths := make([]string, len(topK))
			for i, sf := range topK {
				topPaths[i] = sf.Path
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

			formatted := cc.Format(0)
			for _, sym := range obj.ExpectedSymbols {
				if !strings.Contains(formatted, sym) {
					t.Errorf("expected symbol %q not found in formatted context", sym)
				}
			}
		})
	}
}

func TestRelevanceScorer_Signals(t *testing.T) {
	repos := AllTestRepos()
	if len(repos) == 0 {
		t.Fatal("AllTestRepos() returned 0 repos — fix the test fixture list")
	}

	repo := repos[0] // fatih/color -- simplest
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
	hasNonZeroSymbol := false
	for _, sf := range all {
		if sf.SearchHits > 0 {
			hasNonZeroSearch = true
		}
		if sf.SymbolHits > 0 {
			hasNonZeroSymbol = true
		}
	}
	if !hasNonZeroSearch {
		t.Error("no files with search hits -- search signal broken")
	}
	if !hasNonZeroSymbol {
		t.Error("no files with symbol hits -- symbol signal broken")
	}
}
