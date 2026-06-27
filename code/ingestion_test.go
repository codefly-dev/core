package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// TestIngestion_FullPipeline validates the operational Codefly code layer:
// project metadata, packages, dependencies, file hashes, search, git, timeline,
// context formatting, and relevance. Semantic symbol/flow extraction belongs in
// Mind, not here.
func TestIngestion_FullPipeline(t *testing.T) {
	for _, repo := range representativeOperationalRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			t.Run("GetProjectInfo", func(t *testing.T) {
				testProjectInfo(t, ctx, srv, repo, dir)
			})
			t.Run("BuildDepGraph", func(t *testing.T) {
				testBuildDepGraph(t, ctx, srv, repo)
			})
			t.Run("Search", func(t *testing.T) {
				testSearch(t, ctx, srv, repo)
			})
			t.Run("GitOps", func(t *testing.T) {
				testGitOps(t, ctx, srv)
			})
			t.Run("Timeline", func(t *testing.T) {
				testTimeline(t, ctx, srv)
			})
			t.Run("Context", func(t *testing.T) {
				testContext(t, ctx, srv)
			})
			t.Run("Relevance", func(t *testing.T) {
				testRelevance(t, ctx, srv, repo)
			})
		})
	}
}

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
	if len(info.Packages) == 0 {
		t.Fatal("no packages")
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

func testBuildDepGraph(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := resp.GetGetProjectInfo()
	if len(info.Packages) == 0 {
		t.Fatal("no packages from go list")
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
	if repo.MultiPackage && len(dg.Packages) < repo.MinPackages {
		t.Errorf("expected >= %d packages, got %d", repo.MinPackages, len(dg.Packages))
	}
	t.Logf("packages=%d internal_edges=%d", len(dg.Packages), len(dg.InternalEdges()))
}

func testSearch(t *testing.T, ctx context.Context, srv *GoCodeServer, repo TestRepo) {
	result, err := srv.FileOps().Search(ctx, SearchOpts{Pattern: repo.SearchPattern, MaxResults: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) < repo.SearchMinHits {
		t.Errorf("search %q: %d matches < min %d", repo.SearchPattern, len(result.Matches), repo.SearchMinHits)
	}
}

func testGitOps(t *testing.T, ctx context.Context, srv *GoCodeServer) {
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
}

func testContext(t *testing.T, ctx context.Context, srv *GoCodeServer) {
	cc, err := BuildCodebaseContext(ctx, srv)
	if err != nil {
		t.Fatalf("BuildCodebaseContext: %v", err)
	}
	if cc.Module == "" {
		t.Error("Module is empty")
	}
	if cc.DepGraph == nil {
		t.Error("DepGraph is nil")
	}

	full := cc.Format(0)
	if len(full) == 0 {
		t.Fatal("Format(0) produced empty output")
	}
	if strings.Contains(full, "Code Map") || strings.Contains(full, "Call Graph") {
		t.Fatal("CodebaseContext should not include semantic symbol sections")
	}

	files := cc.FilePaths()
	if len(files) == 0 {
		t.Error("FilePaths() returned nothing")
	}
}

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

	topK := scorer.TopK(ctx, repo.SearchPattern, files, 5)
	if len(topK) == 0 {
		t.Fatal("TopK returned no results")
	}
	if topK[0].Score <= 0 {
		t.Error("top result has zero score")
	}
}

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

func TestIngestion_OverlayVFS(t *testing.T) {
	repo := AllTestRepos()[0]
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

	if err := srv.FileOps().WriteFile(ctx, "virtual_new.go", []byte("package main\n\nfunc VirtualFunc() {}\n")); err != nil {
		t.Fatalf("virtual write should succeed: %v", err)
	}

	if _, err := srv.FileOps().ReadFile(ctx, "virtual_new.go"); err != nil {
		t.Error("virtual file should exist before rollback")
	}

	searchResult, err := srv.FileOps().Search(ctx, SearchOpts{Pattern: "VirtualFunc", Literal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchResult.Matches) == 0 {
		t.Error("search should find VirtualFunc in overlay")
	}

	if !overlay.Dirty() {
		t.Error("overlay should be dirty after write")
	}
	if len(overlay.Diff()) == 0 {
		t.Fatal("overlay diff should have at least one change")
	}

	overlay.Rollback()
	if overlay.Dirty() {
		t.Error("overlay should not be dirty after rollback")
	}
	if _, err := srv.FileOps().ReadFile(ctx, "virtual_new.go"); err == nil {
		t.Error("virtual file should NOT exist after rollback")
	}
	restoredGoMod, err := srv.FileOps().ReadFile(ctx, "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredGoMod) != origGoMod {
		t.Error("go.mod content should be restored after rollback")
	}
}
