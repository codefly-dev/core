package code

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// TestIngestion_FullPipeline runs the complete analysis pipeline for each repo
// through GoCodeServer.Execute() and validates every layer of output against
// ground truth derived from independently reading the source code.
func TestIngestion_FullPipeline(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			t.Run("GetProjectInfo", func(t *testing.T) {
				testProjectInfo(t, ctx, srv, repo, dir)
			})
			t.Run("ListSymbols", func(t *testing.T) {
				testListSymbols(t, ctx, srv, repo)
			})
			t.Run("BuildCodeMap", func(t *testing.T) {
				testBuildCodeMap(t, ctx, srv, repo)
			})
			t.Run("BuildDepGraph", func(t *testing.T) {
				testBuildDepGraph(t, ctx, srv, repo)
			})
			t.Run("CodeGraph", func(t *testing.T) {
				testCodeGraph(t, repo, dir)
			})
			t.Run("Search", func(t *testing.T) {
				testSearch(t, ctx, srv, repo)
			})
			t.Run("GitOps", func(t *testing.T) {
				testGitOps(t, ctx, srv)
			})
			t.Run("GroundTruth", func(t *testing.T) {
				testGroundTruth(t, ctx, srv, dir)
			})
			t.Run("Timeline", func(t *testing.T) {
				testTimeline(t, ctx, srv)
			})
			t.Run("Context", func(t *testing.T) {
				testContext(t, ctx, srv, repo)
			})
			t.Run("Relevance", func(t *testing.T) {
				testRelevance(t, ctx, srv, repo)
			})
		})
	}
}

// --- 1. GetProjectInfo ---

func testProjectInfo(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo, dir string) {
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := resp.GetGetProjectInfo()
	if info.Language != "go" {
		t.Errorf("language = %q, want go", info.Language)
	}
	if info.Module != repo.Module {
		t.Errorf("module = %q, want %q", info.Module, repo.Module)
	}
	if len(info.FileHashes) == 0 {
		t.Error("no file hashes")
	}

	goFiles := countGoFiles(dir)
	if goFiles == 0 {
		t.Error("ground truth: no .go files found")
	}
	goFileHashes := 0
	for path := range info.FileHashes {
		if strings.HasSuffix(path, ".go") {
			goFileHashes++
		}
	}
	if goFileHashes < goFiles/2 {
		t.Errorf("file hashes (%d) significantly less than .go files (%d)", goFileHashes, goFiles)
	}
}

// --- 2. ListSymbols ---

func testListSymbols(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	syms := resp.GetListSymbols()
	if syms.Status.State != codev0.ListSymbolsStatus_SUCCESS {
		t.Fatalf("failed: %s", syms.Status.Message)
	}
	if len(syms.Symbols) < repo.MinSymbols {
		t.Errorf("%d symbols < min %d", len(syms.Symbols), repo.MinSymbols)
	}

	for _, s := range syms.Symbols {
		if s.Name == "" {
			t.Error("symbol with empty name")
		}
		if s.Location == nil || s.Location.File == "" {
			t.Errorf("symbol %q missing location", s.Name)
		}
		if s.Location != nil && s.Location.Line <= 0 {
			t.Errorf("symbol %q has line %d", s.Name, s.Location.Line)
		}
	}
}

// --- 3. BuildCodeMap ---

func testBuildCodeMap(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	syms := resp.GetListSymbols().Symbols

	var inputs []SymbolInput
	for _, s := range syms {
		file := ""
		line := 0
		if s.Location != nil {
			file = s.Location.File
			line = int(s.Location.Line)
		}
		inputs = append(inputs, SymbolInput{
			Name:      s.Name,
			Kind:      s.Kind.String(),
			Signature: s.Signature,
			File:      file,
			Line:      line,
			Parent:    s.Parent,
		})
	}

	cm := BuildCodeMap("go", inputs)
	if len(cm.Files) == 0 {
		t.Fatal("code map has no files")
	}
	stats := cm.Stats()
	if stats.Symbols < repo.MinSymbols {
		t.Errorf("code map: %d symbols < min %d", stats.Symbols, repo.MinSymbols)
	}

	formatted := cm.Format()
	if len(formatted) == 0 {
		t.Error("formatted code map is empty")
	}
	if !strings.Contains(formatted, "Code Map") {
		t.Error("formatted output missing header")
	}

	t.Logf("%d files, %d symbols, formatted=%d bytes", stats.Files, stats.Symbols, len(formatted))
}

// --- 4. BuildDepGraph ---

func testBuildDepGraph(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := resp.GetGetProjectInfo()
	if len(info.Packages) == 0 {
		t.Skip("no packages from go list")
	}

	var inputs []PackageInput
	for _, pkg := range info.Packages {
		inputs = append(inputs, PackageInput{
			Name:    pkg.Name,
			Path:    pkg.RelativePath,
			Imports: pkg.Imports,
			Files:   pkg.Files,
			Doc:     pkg.Doc,
		})
	}

	dg := BuildDepGraph(info.Module, inputs)
	if len(dg.Packages) == 0 {
		t.Fatal("dep graph has no packages")
	}

	if repo.MultiPackage {
		if len(dg.Packages) < repo.MinPackages {
			t.Errorf("expected >= %d packages, got %d", repo.MinPackages, len(dg.Packages))
		}
		edges := dg.InternalEdges()
		t.Logf("%d packages, %d internal edges", len(dg.Packages), len(edges))
	}

	roots := dg.Roots()
	leaves := dg.Leaves()
	t.Logf("roots=%d, leaves=%d", len(roots), len(leaves))
}

// --- 5. CodeGraph (ParseGoTree directly) ---

func testCodeGraph(t *testing.T, repo TestRepo, dir string) {
	g, err := ParseGoTree(dir)
	if err != nil {
		t.Fatalf("ParseGoTree: %v", err)
	}

	funcCount, methodCount, typeCount := 0, 0, 0
	for _, n := range g.Nodes {
		switch n.Kind {
		case NodeFunction:
			funcCount++
		case NodeMethod:
			methodCount++
		case NodeType:
			typeCount++
		}
	}

	if funcCount+methodCount+typeCount < repo.MinSymbols {
		t.Errorf("CodeGraph: %d total symbols < min %d", funcCount+methodCount+typeCount, repo.MinSymbols)
	}

	callEdges := 0
	for _, e := range g.Edges {
		if e.Kind == EdgeCalls {
			callEdges++
		}
	}

	for _, name := range repo.KnownFunctions {
		found := false
		for _, n := range g.Nodes {
			if n.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected function %q in CodeGraph", name)
		}
	}

	t.Logf("nodes=%d (%d funcs, %d methods, %d types), edges=%d (%d calls)",
		len(g.Nodes), funcCount, methodCount, typeCount, len(g.Edges), callEdges)
}

// --- 6. Search ---

func testSearch(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	result, err := srv.FileOps().Search(ctx, SearchOpts{Pattern: repo.SearchPattern, MaxResults: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) < repo.SearchMinHits {
		t.Errorf("search %q: %d matches < min %d", repo.SearchPattern, len(result.Matches), repo.SearchMinHits)
	}
	t.Logf("search %q: %d matches", repo.SearchPattern, len(result.Matches))
}

// --- 7. Git operations ---

func testGitOps(t *testing.T, ctx context.Context, srv *GoCodeServer) {
	// Use the server's internal git helper (package-private, accessible in tests).
	logOut, err := srv.runGit(ctx, "log", "--max-count=5", "--format=%H %s")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(logOut), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Error("no git commits")
	}
	hash := strings.Fields(lines[0])[0]

	showOut, err := srv.runGit(ctx, "show", hash+":go.mod")
	if err != nil {
		t.Logf("git show go.mod: not found at %s (may be expected)", hash[:8])
	} else if showOut == "" {
		t.Error("git show go.mod: empty content")
	}

	blameOut, err := srv.runGit(ctx, "blame", "--porcelain", "-L1,5", "--", "go.mod")
	if err != nil {
		t.Fatalf("git blame: %v", err)
	}
	if blameOut == "" {
		t.Error("no blame output")
	}
}

// --- 8. Ground truth comparison ---

func testGroundTruth(t *testing.T, ctx context.Context, srv *GoCodeServer, dir string) {
	gtFuncs, gtTypes := countExportedSymbols(t, dir)

	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	syms := resp.GetListSymbols().Symbols

	apiFuncs, apiTypes := 0, 0
	for _, s := range syms {
		if s.Name == "" || !isExported(s.Name) {
			continue
		}
		switch s.Kind {
		case codev0.SymbolKind_SYMBOL_KIND_FUNCTION:
			apiFuncs++
		case codev0.SymbolKind_SYMBOL_KIND_STRUCT:
			apiTypes++
		}
	}

	t.Logf("ground truth: %d exported funcs, %d exported types", gtFuncs, gtTypes)
	t.Logf("Code API:     %d exported funcs, %d exported types", apiFuncs, apiTypes)

	if apiFuncs == 0 && gtFuncs > 0 {
		t.Error("API found 0 exported functions but ground truth found some")
	}
	if apiTypes == 0 && gtTypes > 0 {
		t.Error("API found 0 exported types but ground truth found some")
	}

	funcDiff := abs(apiFuncs - gtFuncs)
	if gtFuncs > 0 && funcDiff > gtFuncs/3 {
		t.Errorf("exported func count diverges: API=%d, ground truth=%d (diff=%d)",
			apiFuncs, gtFuncs, funcDiff)
	}
	typeDiff := abs(apiTypes - gtTypes)
	if gtTypes > 0 && typeDiff > gtTypes/3 {
		t.Errorf("exported type count diverges: API=%d, ground truth=%d (diff=%d)",
			apiTypes, gtTypes, typeDiff)
	}
}

// --- Helpers ---

func countGoFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, _ error) error {
		if d != nil && d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			n++
		}
		return nil
	})
	return n
}

// countExportedSymbols uses go/ast to independently count exported functions and
// types in the source tree -- the "ground truth" we compare the Code API against.
func countExportedSymbols(t *testing.T, dir string) (funcs, types int) {
	t.Helper()
	fset := token.NewFileSet()
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, _ error) error {
		if d != nil && d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() && d.Recv == nil {
					funcs++
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
						types++
					}
				}
			}
		}
		return nil
	})
	return
}

// --- 9. Context ---

func testContext(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	cc, err := BuildCodebaseContext(ctx, srv)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}
	if cc.Module == "" {
		t.Error("Module is empty")
	}
	if cc.CodeMap == nil || len(cc.CodeMap.Files) == 0 {
		t.Error("CodeMap missing or empty")
	}
	if cc.DepGraph == nil {
		t.Error("DepGraph is nil")
	}
	if cc.Graph == nil {
		t.Error("Graph is nil")
	}

	full := cc.Format(0)
	if len(full) == 0 {
		t.Fatal("Format(0) produced empty output")
	}
	if !strings.Contains(full, cc.Module) {
		t.Error("formatted output missing module name")
	}

	budgeted := cc.Format(4000)
	if len(budgeted) > 4000 {
		t.Errorf("Format(4000) produced %d bytes", len(budgeted))
	}

	files := cc.FilePaths()
	if len(files) == 0 {
		t.Error("FilePaths() returned nothing")
	}

	t.Logf("module=%s files=%d full=%d bytes budget(4k)=%d bytes",
		cc.Module, len(files), len(full), len(budgeted))
}

// --- 10. Relevance ---

func testRelevance(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	cc, err := BuildCodebaseContext(ctx, srv)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}

	scorer := NewRelevanceScorer(cc, srv.GetVFS(), srv.GetSourceDir())
	files := cc.FilePaths()
	if len(files) == 0 {
		t.Fatal("no files to score")
	}

	query := repo.SearchPattern
	topK := scorer.TopK(ctx, query, files, 5)
	if len(topK) == 0 {
		t.Fatal("TopK returned no results")
	}
	if topK[0].Score <= 0 {
		t.Error("top result has zero score")
	}

	for i := 1; i < len(topK); i++ {
		if topK[i].Score > topK[i-1].Score {
			t.Errorf("not sorted: index %d score %.3f > index %d score %.3f",
				i, topK[i].Score, i-1, topK[i-1].Score)
		}
	}

	t.Logf("query=%q top=%s score=%.3f", query, topK[0].Path, topK[0].Score)
}

// --- 11. Timeline ---

func testTimeline(t *testing.T, ctx context.Context, srv *GoCodeServer) {
	ref := time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC)
	timelines, err := BuildProjectTimeline(ctx, srv.GetVFS(), srv.GetSourceDir(), []string{".go"}, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(timelines) == 0 {
		t.Fatal("no timelines")
	}

	stats := ComputeTimelineStats(timelines)
	if stats.TotalFiles == 0 || stats.TotalLines == 0 || stats.TotalChunks == 0 {
		t.Errorf("empty stats: files=%d lines=%d chunks=%d",
			stats.TotalFiles, stats.TotalLines, stats.TotalChunks)
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

	t.Logf("%d files, %d lines, %d chunks, recent=%d moderate=%d old=%d ancient=%d",
		stats.TotalFiles, stats.TotalLines, stats.TotalChunks,
		stats.LinesByAge[AgeRecent], stats.LinesByAge[AgeModerate],
		stats.LinesByAge[AgeOld], stats.LinesByAge[AgeAncient])
}

// TestIngestion_OverlayVFS applies virtual edits via OverlayVFS on a real repo,
// verifies the context reflects the edits, then rolls back and confirms the
// original state is restored.
func TestIngestion_OverlayVFS(t *testing.T) {
	repo := AllTestRepos()[0] // simplest repo
	dir := EnsureRepo(t, repo)
	ctx := context.Background()

	overlay := NewOverlayVFS(LocalVFS{})
	srv := NewGoCodeServer(dir, []ServerOption{WithVFS(overlay)})

	origFiles, err := srv.FileOps().ListFiles(ctx, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("original file count: %d", len(origFiles))

	origGoModBytes, err := srv.FileOps().ReadFile(ctx, "go.mod")
	if err != nil {
		t.Fatalf("go.mod should exist: %v", err)
	}
	origGoMod := string(origGoModBytes)

	// --- virtual write: create a new file ---
	if err := srv.FileOps().WriteFile(ctx, "virtual_new.go", []byte("package main\n\nfunc VirtualFunc() {}\n")); err != nil {
		t.Fatalf("virtual write should succeed: %v", err)
	}

	// --- verify virtual file is readable ---
	_, err = srv.FileOps().ReadFile(ctx, "virtual_new.go")
	if err != nil {
		t.Error("virtual file should exist before rollback")
	}

	// --- verify search finds content in virtual file ---
	searchResult, err := srv.FileOps().Search(ctx, SearchOpts{Pattern: "VirtualFunc", Literal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchResult.Matches) == 0 {
		t.Error("search should find VirtualFunc in overlay")
	}

	// --- verify overlay is dirty ---
	if !overlay.Dirty() {
		t.Error("overlay should be dirty after write")
	}

	// --- diff should show the virtual write ---
	changes := overlay.Diff()
	if len(changes) == 0 {
		t.Fatal("overlay diff should have at least one change")
	}
	t.Logf("overlay changes: %d", len(changes))
	for _, c := range changes {
		t.Logf("  %s: %s", c.Type, c.Path)
	}

	// --- rollback: everything goes back to original ---
	overlay.Rollback()

	if overlay.Dirty() {
		t.Error("overlay should not be dirty after rollback")
	}

	_, err = srv.FileOps().ReadFile(ctx, "virtual_new.go")
	if err == nil {
		t.Error("virtual file should NOT exist after rollback")
	}

	restoredGoMod, err := srv.FileOps().ReadFile(ctx, "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredGoMod) != origGoMod {
		t.Error("go.mod content should be restored after rollback")
	}

	t.Log("overlay rollback: original state fully restored")
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
