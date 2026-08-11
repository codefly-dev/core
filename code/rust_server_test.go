package code

import (
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
)

func TestRustCodeServerGetProjectInfoSingleCrate(t *testing.T) {
	root := filepath.Join("testdata", "rust", "single")
	server := NewRustCodeServer(root)
	response, err := server.Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure := response.GetFailure(); failure.GetCode() != basev0.FailureCode_FAILURE_CODE_UNSPECIFIED {
		t.Fatalf("GetProjectInfo failure = %+v", failure)
	}
	info := response.GetGetProjectInfo()
	if info.GetLanguage() != "rust" || info.GetLanguageVersion() != "2021" || info.GetModule() != "single-crate" {
		t.Fatalf("project identity = %+v", info)
	}
	if len(info.GetPackages()) != 1 || info.GetPackages()[0].GetRelativePath() != "." ||
		len(info.GetPackages()[0].GetFiles()) != 1 || info.GetPackages()[0].GetFiles()[0] != "src/lib.rs" {
		t.Fatalf("packages = %+v", info.GetPackages())
	}
	if info.GetFileHashes()["Cargo.toml"] == "" || info.GetFileHashes()["src/lib.rs"] == "" {
		t.Fatalf("file hashes = %+v", info.GetFileHashes())
	}
	if len(info.GetSourceFiles()) != 1 || info.GetSourceFiles()[0].GetPath() != "src/lib.rs" {
		t.Fatalf("source files = %+v", info.GetSourceFiles())
	}
}

func TestRustCodeServerScopesCargoWorkspaceToMemberCodeUnit(t *testing.T) {
	root := filepath.Join("testdata", "rust", "workspace", "alpha")
	response, err := NewSourceTooling(NewRustCodeServer(root)).GetProjectInfo(t.Context(), &toolingv0.GetProjectInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if failure := response.GetFailure(); failure.GetCode() != basev0.FailureCode_FAILURE_CODE_UNSPECIFIED {
		t.Fatalf("GetProjectInfo failure = %+v", failure)
	}
	if response.GetModule() != "alpha" || len(response.GetPackages()) != 1 || response.GetPackages()[0].GetName() != "alpha" {
		t.Fatalf("member project = %+v", response)
	}
	if len(response.GetDependencies()) != 1 || response.GetDependencies()[0].GetName() != "beta" || !response.GetDependencies()[0].GetDirect() {
		t.Fatalf("member dependencies = %+v", response.GetDependencies())
	}
	for path := range response.GetFileHashes() {
		if path == ".." || filepath.IsAbs(path) || filepath.Clean(path) == ".." || len(path) >= 3 && path[:3] == "../" {
			t.Fatalf("file hash escaped member root: %q", path)
		}
	}
}

func TestRustCodeServerGetProjectInfoCargoWorkspace(t *testing.T) {
	fixture := filepath.Join("testdata", "rust", "workspace")
	root := filepath.Join(t.TempDir(), "session-matrix.repository-worktree-ephemeral")
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	response, err := NewRustCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure := response.GetFailure(); failure.GetCode() != basev0.FailureCode_FAILURE_CODE_UNSPECIFIED {
		t.Fatalf("GetProjectInfo failure = %+v", failure)
	}
	info := response.GetGetProjectInfo()
	// A virtual workspace has no Cargo module of its own. Its checkout directory
	// is deliberately lease-shaped to prove execution location never becomes
	// published project identity.
	if info.GetModule() != "" || info.GetLanguageVersion() != "2021,2024" || len(info.GetPackages()) != 2 {
		t.Fatalf("workspace project = %+v", info)
	}
	if info.GetPackages()[0].GetRelativePath() != "alpha" || info.GetPackages()[1].GetRelativePath() != "beta" {
		t.Fatalf("workspace packages = %+v", info.GetPackages())
	}
}

func TestRustCodeServerMalformedManifestFailsClosed(t *testing.T) {
	root := filepath.Join("testdata", "rust", "malformed")
	response, err := NewRustCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure := response.GetFailure(); failure.GetCode() != basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED || failure.GetRetryable() {
		t.Fatalf("malformed manifest failure = %+v, want non-retryable process failure", failure)
	}
	if response.GetGetProjectInfo().GetLanguage() != "rust" {
		t.Fatalf("typed language evidence was dropped: %+v", response.GetGetProjectInfo())
	}
}
