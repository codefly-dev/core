package code

import (
	"context"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestGoCodeServer_ListSymbols_RealRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)
			ctx := context.Background()

			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			syms := resp.GetListSymbols()
			if syms == nil || syms.Status == nil {
				t.Fatal("nil ListSymbols response")
			}
			if syms.Status.State != codev0.ListSymbolsStatus_SUCCESS {
				t.Fatalf("ListSymbols failed: %s", syms.Status.Message)
			}
			if len(syms.Symbols) < repo.MinSymbols {
				t.Errorf("expected >= %d symbols, got %d", repo.MinSymbols, len(syms.Symbols))
			}

			funcCount, methodCount, typeCount := 0, 0, 0
			for _, s := range syms.Symbols {
				switch s.Kind {
				case codev0.SymbolKind_SYMBOL_KIND_FUNCTION:
					funcCount++
				case codev0.SymbolKind_SYMBOL_KIND_METHOD:
					methodCount++
				case codev0.SymbolKind_SYMBOL_KIND_STRUCT:
					typeCount++
				}
			}
			t.Logf("%s: %d symbols (%d funcs, %d methods, %d types)",
				repo.Name, len(syms.Symbols), funcCount, methodCount, typeCount)

			for _, known := range repo.KnownFunctions {
				if !hasSymbolNamed(syms.Symbols, known) {
					t.Errorf("expected to find function %q", known)
				}
			}
			for _, known := range repo.KnownTypes {
				if !hasSymbolNamed(syms.Symbols, known) {
					t.Errorf("expected to find type %q", known)
				}
			}
		})
	}
}

func TestGoCodeServer_ListSymbols_SingleFile(t *testing.T) {
	repo := AllTestRepos()[0] // fatih/color
	dir := EnsureRepo(t, repo)
	srv := NewGoCodeServer(dir, nil)

	resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{File: "color.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	syms := resp.GetListSymbols()
	if syms.Status.State != codev0.ListSymbolsStatus_SUCCESS {
		t.Fatalf("failed: %s", syms.Status.Message)
	}
	if len(syms.Symbols) == 0 {
		t.Fatal("no symbols in color.go")
	}
	for _, s := range syms.Symbols {
		if s.Location == nil || s.Location.File != "color.go" {
			t.Errorf("symbol %q has wrong file: %v", s.Name, s.Location)
		}
	}
	t.Logf("color.go: %d symbols", len(syms.Symbols))
}

func TestGoCodeServer_GetProjectInfo_RealRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)

			resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			info := resp.GetGetProjectInfo()
			if info == nil {
				t.Fatal("nil GetProjectInfo response")
			}
			if info.Error != "" {
				t.Fatalf("error: %s", info.Error)
			}
			if info.Language != "go" {
				t.Errorf("language: got %q, want go", info.Language)
			}
			if info.Module != repo.Module {
				t.Errorf("module: got %q, want %q", info.Module, repo.Module)
			}
			if len(info.FileHashes) == 0 {
				t.Error("no file hashes")
			}

			t.Logf("%s: module=%s, %d packages, %d deps, %d file hashes",
				repo.Name, info.Module, len(info.Packages), len(info.Dependencies), len(info.FileHashes))

			if repo.MultiPackage && len(info.Packages) < repo.MinPackages {
				t.Errorf("expected >= %d packages, got %d", repo.MinPackages, len(info.Packages))
			}
		})
	}
}

func TestGoCodeServer_ListDependencies_RealRepos(t *testing.T) {
	for _, repo := range AllTestRepos() {
		repo := repo
		t.Run(repo.Name, func(t *testing.T) {
			dir := EnsureRepo(t, repo)
			srv := NewGoCodeServer(dir, nil)

			resp, err := srv.Execute(context.Background(), &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ListDependencies{ListDependencies: &codev0.ListDependenciesRequest{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			deps := resp.GetListDependencies()
			if deps == nil {
				t.Fatal("nil response")
			}
			if deps.Error != "" {
				t.Skipf("go list -m failed (deps not downloaded?): %s", deps.Error)
			}
			t.Logf("%s: %d dependencies", repo.Name, len(deps.Dependencies))
			for _, d := range deps.Dependencies {
				if d.Name == "" {
					t.Error("empty dependency name")
				}
			}
		})
	}
}

func TestGoCodeServer_InheritsDefaultOps(t *testing.T) {
	repo := AllTestRepos()[0]
	dir := EnsureRepo(t, repo)
	srv := NewGoCodeServer(dir, nil)
	ctx := context.Background()

	content, err := srv.FileOps().ReadFile(ctx, "color.go")
	if err != nil {
		t.Fatal("color.go should exist:", err)
	}
	if len(content) == 0 {
		t.Error("color.go is empty")
	}

	result, err := srv.FileOps().Search(ctx, SearchOpts{Pattern: repo.SearchPattern, MaxResults: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) < repo.SearchMinHits {
		t.Errorf("search %q: expected >= %d hits, got %d",
			repo.SearchPattern, repo.SearchMinHits, len(result.Matches))
	}
}

func hasSymbolNamed(symbols []*codev0.Symbol, name string) bool {
	for _, s := range symbols {
		if s.Name == name {
			return true
		}
	}
	return false
}
