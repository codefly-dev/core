package code

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TestParseGoTree_RealRepos
// ---------------------------------------------------------------------------

func TestParseGoTree_RealRepos(t *testing.T) {
	repos := AllTestRepos()

	for _, repo := range repos {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)

			graph, err := ParseGoTree(dir)
			if err != nil {
				t.Fatalf("ParseGoTree(%s): %v", repo.Name, err)
			}

			stats := graph.Stats()
			t.Logf("%s: %s", repo.Name, stats)

			if stats.TotalNodes == 0 {
				t.Error("graph has zero nodes")
			}
			if stats.TotalEdges == 0 {
				t.Error("graph has zero edges")
			}
			if stats.Functions+stats.Methods == 0 {
				t.Error("graph has no functions or methods")
			}
			if stats.Files == 0 {
				t.Error("graph has no file nodes")
			}

			symbols := stats.Functions + stats.Methods + stats.Types
			if repo.MinSymbols > 0 && symbols < repo.MinSymbols {
				t.Errorf("expected at least %d symbols, got %d", repo.MinSymbols, symbols)
			}

			for _, fn := range repo.KnownFunctions {
				if !graphContainsName(graph, fn) {
					t.Errorf("expected function/method %q not found in graph", fn)
				}
			}

			for _, typ := range repo.KnownTypes {
				if !graphContainsName(graph, typ) {
					t.Errorf("expected type %q not found in graph", typ)
				}
			}

			for _, n := range graph.Nodes {
				if n.Kind == NodeFile && !strings.HasSuffix(n.File, ".go") {
					t.Errorf("file node %q has unexpected extension", n.File)
				}
			}

			containsEdges := 0
			for _, e := range graph.Edges {
				if e.Kind == EdgeContains {
					containsEdges++
				}
			}
			if containsEdges == 0 {
				t.Error("no Contains edges found")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestDepGraph_RealRepos
// ---------------------------------------------------------------------------

func TestDepGraph_RealRepos(t *testing.T) {
	repos := AllTestRepos()

	for _, repo := range repos {
		if !repo.MultiPackage {
			continue
		}
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)

			pkgs := extractPackagesFromDir(t, dir, repo.Module)
			if len(pkgs) == 0 {
				t.Skip("no packages extracted")
			}

			dg := BuildDepGraph(repo.Module, pkgs)
			t.Logf("%s: %d packages", repo.Name, len(dg.Packages))

			if repo.MinPackages > 0 && len(dg.Packages) < repo.MinPackages {
				t.Errorf("expected at least %d packages, got %d", repo.MinPackages, len(dg.Packages))
			}

			edges := dg.InternalEdges()
			t.Logf("%s: %d internal edges", repo.Name, len(edges))

			roots := dg.Roots()
			leaves := dg.Leaves()
			t.Logf("%s: roots=%d, leaves=%d", repo.Name, len(roots), len(leaves))

			if len(roots) == 0 {
				t.Error("expected at least one root package")
			}
			if len(leaves) == 0 {
				t.Error("expected at least one leaf package")
			}

			components := dg.ConnectedComponents()
			t.Logf("%s: %d connected components", repo.Name, len(components))
			if len(components) == 0 {
				t.Error("expected at least one connected component")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestSearch_RealRepos
// ---------------------------------------------------------------------------

func TestSearch_RealRepos(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not installed")
	}

	repos := AllTestRepos()

	for _, repo := range repos {
		if repo.SearchPattern == "" {
			continue
		}
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)

			result, err := Search(context.Background(), dir, SearchOpts{
				Pattern:    repo.SearchPattern,
				Extensions: []string{".go"},
				MaxResults: 50,
			})
			if err != nil {
				t.Fatalf("Search(%s, %q): %v", repo.Name, repo.SearchPattern, err)
			}

			t.Logf("%s: search %q => %d matches", repo.Name, repo.SearchPattern, len(result.Matches))

			if len(result.Matches) < repo.SearchMinHits {
				t.Errorf("expected at least %d matches for %q, got %d",
					repo.SearchMinHits, repo.SearchPattern, len(result.Matches))
			}

			for _, m := range result.Matches {
				if m.File == "" {
					t.Error("match has empty file path")
				}
				if m.Line <= 0 {
					t.Errorf("match in %s has invalid line %d", m.File, m.Line)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func graphContainsName(g *CodeGraph, name string) bool {
	for _, n := range g.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

func extractPackagesFromDir(t *testing.T, dir, module string) []PackageInput {
	t.Helper()
	return scanPackageDirs(dir, module)
}

// scanPackageDirs builds PackageInput entries by parsing the Go source tree.
func scanPackageDirs(dir, module string) []PackageInput {
	graph, err := ParseGoTree(dir)
	if err != nil {
		return nil
	}

	dirImports := make(map[string]map[string]bool)
	dirFiles := make(map[string][]string)

	for _, n := range graph.Nodes {
		if n.Kind == NodeFile {
			pkgDir := ""
			if idx := strings.LastIndex(n.File, "/"); idx >= 0 {
				pkgDir = n.File[:idx]
			}
			dirFiles[pkgDir] = append(dirFiles[pkgDir], n.Name)
			if dirImports[pkgDir] == nil {
				dirImports[pkgDir] = make(map[string]bool)
			}
			for _, imp := range n.Imports {
				dirImports[pkgDir][imp] = true
			}
		}
	}

	var pkgs []PackageInput
	for pkgDir, files := range dirFiles {
		var imports []string
		for imp := range dirImports[pkgDir] {
			imports = append(imports, imp)
		}
		pkgPath := pkgDir
		if pkgPath == "" {
			pkgPath = "."
		}
		pkgs = append(pkgs, PackageInput{
			Name:    pkgPath,
			Path:    pkgPath,
			Imports: imports,
			Files:   files,
		})
	}
	return pkgs
}
