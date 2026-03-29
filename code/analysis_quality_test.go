package code

import (
	"context"
	"strings"
	"testing"
)

// TestAnalysisQuality_FatihColor runs the full analysis pipeline on fatih/color
// through GoCodeServer and inspects every layer for quality. This is the
// end-to-end validation that the pipeline produces rich enough data for LLM
// context and relevance scoring.
func TestAnalysisQuality_FatihColor(t *testing.T) {
	repo := AllTestRepos()[0] // fatih/color
	dir := EnsureRepo(t, repo)

	server := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	cc, err := BuildCodebaseContext(ctx, server)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}

	// --- Layer 1: Project Info ---
	t.Run("ProjectInfo", func(t *testing.T) {
		if cc.Module == "" {
			t.Error("module name is empty")
		}
		if cc.Language != "go" {
			t.Errorf("language = %q, want go", cc.Language)
		}
		if len(cc.Packages) == 0 {
			t.Error("no packages discovered")
		}
		t.Logf("module=%s language=%s packages=%d", cc.Module, cc.Language, len(cc.Packages))
		for _, p := range cc.Packages {
			t.Logf("  pkg %s (%s) files=%d imports=%d",
				p.Name, p.RelativePath, len(p.Files), len(p.Imports))
		}
	})

	// --- Layer 2: CodeMap (symbols) ---
	t.Run("CodeMap", func(t *testing.T) {
		if cc.CodeMap == nil {
			t.Fatal("CodeMap is nil")
		}
		formatted := cc.CodeMap.Format()
		if formatted == "" {
			t.Fatal("CodeMap.Format() is empty")
		}

		for _, fn := range repo.KnownFunctions {
			if !strings.Contains(formatted, fn) {
				t.Errorf("CodeMap missing known function %q", fn)
			}
		}
		for _, typ := range repo.KnownTypes {
			if !strings.Contains(formatted, typ) {
				t.Errorf("CodeMap missing known type %q", typ)
			}
		}

		stats := cc.CodeMap.Stats()
		if stats.Symbols < 5 {
			t.Errorf("only %d symbols in CodeMap, want >= 5", stats.Symbols)
		}
		t.Logf("CodeMap: %d files, %d symbols, %d bytes formatted", stats.Files, stats.Symbols, len(formatted))
	})

	// --- Layer 3: DepGraph ---
	t.Run("DepGraph", func(t *testing.T) {
		if cc.DepGraph == nil {
			t.Fatal("DepGraph is nil")
		}
		formatted := cc.DepGraph.Format()
		t.Logf("DepGraph (%d bytes):\n%s", len(formatted), formatted)
	})

	// --- Layer 4: CodeGraph (AST) ---
	t.Run("CodeGraph", func(t *testing.T) {
		if cc.Graph == nil {
			t.Fatal("CodeGraph is nil -- AST graph not extracted")
		}

		funcCount, typeCount := 0, 0
		for _, n := range cc.Graph.Nodes {
			switch n.Kind {
			case NodeFunction, NodeMethod:
				funcCount++
			case NodeType:
				typeCount++
			}
		}
		edgeCount := len(cc.Graph.Edges)

		if funcCount == 0 {
			t.Error("no functions in CodeGraph")
		}
		if typeCount == 0 {
			t.Error("no types in CodeGraph")
		}

		var newNode, colorNode *CodeNode
		for _, n := range cc.Graph.Nodes {
			if n.Name == "New" && (n.Kind == NodeFunction || n.Kind == NodeMethod) {
				newNode = n
			}
			if n.Name == "Color" && n.Kind == NodeType {
				colorNode = n
			}
		}
		if newNode == nil {
			t.Error("CodeGraph cannot find 'New' function")
		} else {
			t.Logf("New function: %s (line %d, calls %d funcs)",
				newNode.Signature, newNode.Line, len(newNode.Calls))
		}
		if colorNode == nil {
			t.Error("CodeGraph cannot find 'Color' type")
		}

		t.Logf("CodeGraph: %d funcs/methods, %d types, %d edges", funcCount, typeCount, edgeCount)
	})

	// --- Layer 5: Timelines ---
	t.Run("Timelines", func(t *testing.T) {
		if len(cc.Timelines) == 0 {
			t.Skip("no timelines (repo may have no git blame support in this env)")
		}

		t.Logf("Timelines: %d files", len(cc.Timelines))
		for _, tl := range cc.Timelines {
			if len(tl.Chunks) == 0 {
				t.Errorf("file %s has 0 timeline chunks", tl.Path)
				continue
			}
			ageDistrib := map[AgeBucket]int{}
			for _, ch := range tl.Chunks {
				ageDistrib[ch.Age]++
			}
			t.Logf("  %s: %d chunks, age distribution: %v", tl.Path, len(tl.Chunks), ageDistrib)
		}

		stats := cc.Stats
		t.Logf("Timeline stats: total_files=%d, hotspots=%d", stats.TotalFiles, len(stats.Hotspots))
		for _, h := range stats.Hotspots {
			t.Logf("  hotspot: %s (%d chunks)", h.Path, h.Chunks)
		}
	})

	// --- Layer 6: Format (LLM context) ---
	t.Run("Format", func(t *testing.T) {
		full := cc.Format(0)
		if len(full) < 500 {
			t.Errorf("Format(0) only %d bytes, expected substantial context", len(full))
		}
		t.Logf("Format(0): %d bytes", len(full))

		budgeted := cc.Format(2000)
		if len(budgeted) > 2200 {
			t.Errorf("Format(2000) produced %d bytes, budget exceeded by too much", len(budgeted))
		}
		t.Logf("Format(2000): %d bytes", len(budgeted))

		mustContain := []string{cc.Module, "go"}
		for _, s := range mustContain {
			if !strings.Contains(full, s) {
				t.Errorf("Format output missing expected string %q", s)
			}
		}
	})

	// --- Layer 7: Relevance Scoring ---
	t.Run("RelevanceScoring", func(t *testing.T) {
		allFiles := collectFiles(cc)
		if len(allFiles) == 0 {
			t.Skip("no files to score")
		}

		scorer := NewRelevanceScorer(cc, server.GetVFS(), server.GetSourceDir())
		top := scorer.TopK(ctx, "add color attribute to output", allFiles, 5)

		if len(top) == 0 {
			t.Fatal("RelevanceScorer returned 0 results")
		}

		t.Logf("Relevance top 5 for 'add color attribute to output':")
		colorInTop3 := false
		for i, sf := range top {
			t.Logf("  %d. %s (score=%.3f)", i+1, sf.Path, sf.Score)
			if i < 3 && strings.Contains(sf.Path, "color.go") {
				colorInTop3 = true
			}
		}
		if !colorInTop3 {
			t.Error("expected color.go in top 3 for color-related query")
		}
	})

	// --- Integration: full context is meaningful ---
	t.Run("ContextMeaningfulness", func(t *testing.T) {
		full := cc.Format(0)

		checks := []struct {
			what    string
			pattern string
		}{
			{"module declaration", cc.Module},
			{"function signature", "func "},
			{"type declaration", "type "},
		}

		for _, c := range checks {
			if !strings.Contains(full, c.pattern) {
				t.Errorf("formatted context missing %s (looking for %q)", c.what, c.pattern)
			}
		}
	})
}

// TestAnalysisQuality_MultiPackage runs the pipeline on go-chi/chi which has
// multiple packages, validating that cross-package analysis works.
func TestAnalysisQuality_MultiPackage(t *testing.T) {
	repos := AllTestRepos()
	var repo TestRepo
	for _, r := range repos {
		if r.Name == "go_chi_chi" {
			repo = r
			break
		}
	}
	if repo.Name == "" {
		t.Skip("go_chi_chi not found in test repos")
	}

	dir := EnsureRepo(t, repo)
	server := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	cc, err := BuildCodebaseContext(ctx, server)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}

	if len(cc.Packages) < repo.MinPackages {
		t.Errorf("packages=%d, want >= %d", len(cc.Packages), repo.MinPackages)
	}

	if cc.DepGraph != nil {
		depText := cc.DepGraph.Format()
		if len(depText) == 0 {
			t.Error("DepGraph.Format() is empty for multi-package repo")
		}
		t.Logf("DepGraph for %s:\n%s", repo.Name, depText)
	}

	if cc.Graph != nil {
		var routerNode *CodeNode
		for _, n := range cc.Graph.Nodes {
			if n.Name == "NewRouter" && (n.Kind == NodeFunction || n.Kind == NodeMethod) {
				routerNode = n
				break
			}
		}
		if routerNode == nil {
			t.Log("Warning: CodeGraph cannot find 'NewRouter'")
		} else {
			t.Logf("NewRouter: %s", routerNode.Signature)
		}
	}
}

// TestAnalysisQuality_AllRepos runs a lightweight quality check on all test repos
// ensuring the pipeline doesn't crash and produces minimum viable data.
func TestAnalysisQuality_AllRepos(t *testing.T) {
	repos := AllTestRepos()

	for _, repo := range repos {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			server := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			cc, err := BuildCodebaseContext(ctx, server)
			if err != nil {
				t.Fatalf("BuildCodebaseContext(%s): %v", repo.Name, err)
			}

			if cc.Module == "" {
				t.Error("empty module")
			}
			if cc.CodeMap == nil {
				t.Error("nil CodeMap")
			}
			if cc.Graph == nil {
				t.Error("nil CodeGraph")
			}

			formatted := cc.Format(0)
			if len(formatted) < 100 {
				t.Errorf("Format(0) = %d bytes, too small", len(formatted))
			}

			t.Logf("%s: module=%s packages=%d format_bytes=%d timelines=%d",
				repo.Name, cc.Module, len(cc.Packages), len(formatted), len(cc.Timelines))
		})
	}
}

// TestAnalysisQuality_OverlayVFS_EditAndAnalyze verifies that after a virtual
// edit via OverlayVFS, the analysis pipeline correctly reflects the change.
func TestAnalysisQuality_OverlayVFS_EditAndAnalyze(t *testing.T) {
	repo := AllTestRepos()[0] // fatih/color
	dir := EnsureRepo(t, repo)

	base := LocalVFS{}
	overlay := NewOverlayVFS(base)

	absPath := dir + "/color_extended.go"
	newCode := `package color

// ExtendedColor provides additional color functionality.
type ExtendedColor struct {
	Base *Color
	Bold bool
}

// NewExtendedColor creates an extended color.
func NewExtendedColor(c *Color) *ExtendedColor {
	return &ExtendedColor{Base: c}
}
`
	if err := overlay.WriteFile(absPath, []byte(newCode), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	server := NewGoCodeServer(dir, []ServerOption{WithVFS(overlay)})

	graph, err := ParseGoTreeVFS(overlay, dir)
	if err != nil {
		t.Fatalf("ParseGoTreeVFS: %v", err)
	}

	found := false
	for _, n := range graph.Nodes {
		if n.Name == "ExtendedColor" {
			found = true
			t.Logf("found virtual type: %s at %s:%d", n.Signature, n.File, n.Line)
		}
		if n.Name == "NewExtendedColor" {
			t.Logf("found virtual function: %s at %s:%d", n.Signature, n.File, n.Line)
		}
	}
	if !found {
		t.Error("virtual type ExtendedColor not found in AST graph")
	}

	_ = server
}

func collectFiles(cc *CodebaseContext) []string {
	seen := make(map[string]bool)
	if cc.CodeMap != nil {
		for _, fm := range cc.CodeMap.Files {
			if fm.Path != "" && !seen[fm.Path] {
				seen[fm.Path] = true
			}
		}
	}
	if cc.Graph != nil {
		for _, n := range cc.Graph.Nodes {
			if n.Kind == NodeFile && !seen[n.File] {
				seen[n.File] = true
			}
		}
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	return files
}
