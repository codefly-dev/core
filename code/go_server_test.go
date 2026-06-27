package code

import (
	"context"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestGoCodeServer_GetProjectInfo_RealRepos(t *testing.T) {
	for _, repo := range representativeOperationalRepos() {
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
			if len(info.Packages) == 0 {
				t.Fatal("no packages")
			}
			if len(info.FileHashes) == 0 {
				t.Error("no file hashes")
			}
			if repo.MultiPackage && len(info.Packages) < repo.MinPackages {
				t.Errorf("expected >= %d packages, got %d", repo.MinPackages, len(info.Packages))
			}

			t.Logf("%s: module=%s, %d packages, %d deps, %d file hashes",
				repo.Name, info.Module, len(info.Packages), len(info.Dependencies), len(info.FileHashes))
		})
	}
}

func TestGoCodeServer_ListDependencies_RealRepos(t *testing.T) {
	for _, repo := range representativeOperationalRepos() {
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
				t.Fatalf("go list -m failed (run `go mod download` in the testdata repo): %s", deps.Error)
			}
			for _, d := range deps.Dependencies {
				if d.Name == "" {
					t.Error("empty dependency name")
				}
			}
			t.Logf("%s: %d dependencies", repo.Name, len(deps.Dependencies))
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
