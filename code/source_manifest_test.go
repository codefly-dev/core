package code

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestGetSourceManifestWorktreeReturnsBodyFreeExactIdentities(t *testing.T) {
	root := t.TempDir()
	writeSourceManifestFile(t, root, "README.md", "hello\n", 0o644)
	writeSourceManifestFile(t, root, "bin/run", "#!/bin/sh\n", 0o755)
	writeSourceManifestFile(t, root, ".env.example", "SAFE=true\n", 0o644)
	writeSourceManifestFile(t, root, "Cart.cs", "namespace Shop;\n", 0o644)
	writeSourceManifestFile(t, root, "node_modules/ignored.js", "ignored\n", 0o644)
	if err := os.Symlink("README.md", filepath.Join(root, "readme-link")); err != nil {
		t.Fatalf("create real symlink: %v", err)
	}

	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetSourceManifest{GetSourceManifest: &codev0.GetSourceManifestRequest{}},
	})
	if err != nil {
		t.Fatalf("GetSourceManifest worktree: %v", err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("GetSourceManifest worktree failure: %+v", response.GetFailure())
	}
	manifest := response.GetGetSourceManifest()
	if manifest.GetRevision() != "" {
		t.Fatalf("worktree revision = %q, want empty", manifest.GetRevision())
	}
	entries := sourceManifestEntriesByPath(manifest)
	if len(entries) != 5 {
		t.Fatalf("worktree entries = %v, want five source artifacts", sourceManifestEntryPaths(manifest))
	}
	assertSourceManifestEntry(t, entries["README.md"], 0o100644, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "hello\n")
	assertSourceManifestEntry(t, entries["bin/run"], 0o100755, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "#!/bin/sh\n")
	assertSourceManifestEntry(t, entries[".env.example"], 0o100644, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "SAFE=true\n")
	assertSourceManifestEntry(t, entries["readme-link"], 0o120000, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "README.md")
	if got := entries["README.md"].GetAttributes(); got.GetLanguage() != basev0.SourceLanguage_SOURCE_LANGUAGE_MARKDOWN || got.GetContentKind() != basev0.SourceContentKind_SOURCE_CONTENT_KIND_TEXT || got.GetSourceRole() != basev0.SourceRole_SOURCE_ROLE_DOCS || got.GetClassifierVersion() != sourceAttributesClassifierVersion {
		t.Fatalf("README attributes = %+v", got)
	}
	if got := entries["readme-link"].GetAttributes(); got.GetContentKind() != basev0.SourceContentKind_SOURCE_CONTENT_KIND_SYMLINK {
		t.Fatalf("symlink attributes = %+v", got)
	}
	if got := entries["Cart.cs"].GetAttributes(); got.GetLanguage() != basev0.SourceLanguage_SOURCE_LANGUAGE_CSHARP || got.GetSourceRole() != basev0.SourceRole_SOURCE_ROLE_PRODUCTION {
		t.Fatalf("C# attributes = %+v", got)
	}
}

func TestGetSourceManifestRevisionPreservesGitEntryKinds(t *testing.T) {
	root := t.TempDir()
	writeSourceManifestFile(t, root, "README.md", "versioned\n", 0o644)
	writeSourceManifestFile(t, root, "bin/run", "#!/bin/sh\n", 0o755)
	if err := os.Symlink("README.md", filepath.Join(root, "readme-link")); err != nil {
		t.Fatalf("create real symlink: %v", err)
	}
	gitSourceManifest(t, root, "init", "-b", "main")
	gitSourceManifest(t, root, "config", "commit.gpgsign", "false")
	gitSourceManifest(t, root, "add", "README.md", "bin/run", "readme-link")
	gitSourceManifest(t, root, "commit", "-m", "source files")
	gitlinkCommit := gitSourceManifest(t, root, "rev-parse", "HEAD")
	gitSourceManifest(t, root, "update-index", "--add", "--cacheinfo", "160000,"+gitlinkCommit+",modules/dependency")
	gitSourceManifest(t, root, "commit", "-m", "gitlink")
	wantRevision := gitSourceManifest(t, root, "rev-parse", "HEAD")

	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetSourceManifest{GetSourceManifest: &codev0.GetSourceManifestRequest{Revision: "HEAD"}},
	})
	if err != nil {
		t.Fatalf("GetSourceManifest revision: %v", err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("GetSourceManifest revision failure: %+v", response.GetFailure())
	}
	manifest := response.GetGetSourceManifest()
	if manifest.GetRevision() != wantRevision {
		t.Fatalf("resolved revision = %q, want %q", manifest.GetRevision(), wantRevision)
	}
	entries := sourceManifestEntriesByPath(manifest)
	if len(entries) != 4 {
		t.Fatalf("revision entries = %v, want four Git artifacts", sourceManifestEntryPaths(manifest))
	}
	if got := entries["README.md"]; got.GetMode() != 0o100644 || got.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE || got.GetIdentity().GetAlgorithm() != basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_BLOB_SHA1 {
		t.Fatalf("README revision entry = %+v", got)
	}
	if got := entries["bin/run"]; got.GetMode() != 0o100755 || got.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE {
		t.Fatalf("executable revision entry = %+v", got)
	}
	if got := entries["readme-link"]; got.GetMode() != 0o120000 || got.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK {
		t.Fatalf("symlink revision entry = %+v", got)
	}
	if got := entries["modules/dependency"]; got.GetMode() != 0o160000 || got.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_GITLINK || got.GetIdentity().GetAlgorithm() != basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_OBJECT_SHA1 || got.GetIdentity().GetSizeBytes() != -1 {
		t.Fatalf("gitlink revision entry = %+v", got)
	}
	if got := entries["modules/dependency"].GetAttributes(); got.GetContentKind() != basev0.SourceContentKind_SOURCE_CONTENT_KIND_GITLINK {
		t.Fatalf("gitlink attributes = %+v", got)
	}

	contentResponse, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetSourceManifest{GetSourceManifest: &codev0.GetSourceManifestRequest{
			Revision:     "HEAD",
			IdentityMode: basev0.SourceManifestIdentityMode_SOURCE_MANIFEST_IDENTITY_MODE_CONTENT_SHA256,
		}},
	})
	if err != nil {
		t.Fatalf("GetSourceManifest content identities: %v", err)
	}
	if contentResponse.GetFailure() != nil {
		t.Fatalf("GetSourceManifest content identity failure: %+v", contentResponse.GetFailure())
	}
	contentEntries := sourceManifestEntriesByPath(contentResponse.GetGetSourceManifest())
	assertSourceManifestEntry(t, contentEntries["README.md"], 0o100644, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "versioned\n")
	assertSourceManifestEntry(t, contentEntries["bin/run"], 0o100755, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "#!/bin/sh\n")
	assertSourceManifestEntry(t, contentEntries["readme-link"], 0o120000, basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, "README.md")
	if got := contentEntries["modules/dependency"].GetIdentity().GetAlgorithm(); got != basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_OBJECT_SHA1 {
		t.Fatalf("content-mode gitlink algorithm = %s", got)
	}
}

func TestGetSourceManifestWorktreeTreatsInitializedSubmoduleAsOneGitlink(t *testing.T) {
	dependency := t.TempDir()
	gitSourceManifest(t, dependency, "init", "-b", "main")
	gitSourceManifest(t, dependency, "config", "commit.gpgsign", "false")
	writeSourceManifestFile(t, dependency, "dependency.go", "package dependency\n", 0o644)
	gitSourceManifest(t, dependency, "add", "dependency.go")
	gitSourceManifest(t, dependency, "commit", "-m", "dependency")
	wantCommit := gitSourceManifest(t, dependency, "rev-parse", "HEAD")

	root := t.TempDir()
	gitSourceManifest(t, root, "init", "-b", "main")
	gitSourceManifest(t, root, "config", "commit.gpgsign", "false")
	writeSourceManifestFile(t, root, "main.go", "package main\n", 0o644)
	gitSourceManifest(t, root, "add", "main.go")
	gitSourceManifest(t, root, "commit", "-m", "root")
	gitSourceManifest(t, root, "-c", "protocol.file.allow=always", "submodule", "add", dependency, "modules/dependency")
	gitSourceManifest(t, root, "commit", "-am", "submodule")

	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetSourceManifest{GetSourceManifest: &codev0.GetSourceManifestRequest{}},
	})
	if err != nil {
		t.Fatalf("GetSourceManifest worktree gitlink: %v", err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("GetSourceManifest worktree gitlink failure: %+v", response.GetFailure())
	}
	entries := sourceManifestEntriesByPath(response.GetGetSourceManifest())
	if len(entries) != 3 || entries["modules/dependency/dependency.go"] != nil {
		t.Fatalf("worktree entries = %v, want parent files plus one gitlink", sourceManifestEntryPaths(response.GetGetSourceManifest()))
	}
	gitlink := entries["modules/dependency"]
	if gitlink.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_GITLINK || gitlink.GetMode() != 0o160000 || gitlink.GetIdentity().GetDigest() != wantCommit || gitlink.GetIdentity().GetSizeBytes() != -1 {
		t.Fatalf("worktree gitlink = %+v", gitlink)
	}

	writeSourceManifestFile(t, root, "modules/dependency/untracked.go", "package dependency\n", 0o644)
	dirtyResponse, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetSourceManifest{GetSourceManifest: &codev0.GetSourceManifestRequest{}},
	})
	if err != nil {
		t.Fatalf("GetSourceManifest dirty gitlink transport: %v", err)
	}
	if dirtyResponse.GetFailure() == nil || !strings.Contains(dirtyResponse.GetFailure().GetMessage(), "dirty gitlink modules/dependency") {
		t.Fatalf("dirty gitlink failure = %+v", dirtyResponse.GetFailure())
	}
}

func writeSourceManifestFile(t *testing.T, root, relative, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func gitSourceManifest(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	command.Env = append(command.Environ(),
		"GIT_AUTHOR_NAME=codefly", "GIT_AUTHOR_EMAIL=test@codefly.dev",
		"GIT_COMMITTER_NAME=codefly", "GIT_COMMITTER_EMAIL=test@codefly.dev",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func sourceManifestEntriesByPath(value *basev0.SourceManifest) map[string]*basev0.SourceManifestEntry {
	entries := make(map[string]*basev0.SourceManifestEntry, len(value.GetEntries()))
	for _, entry := range value.GetEntries() {
		entries[entry.GetPath()] = entry
	}
	return entries
}

func sourceManifestEntryPaths(value *basev0.SourceManifest) []string {
	paths := make([]string, 0, len(value.GetEntries()))
	for _, entry := range value.GetEntries() {
		paths = append(paths, entry.GetPath())
	}
	return paths
}

func assertSourceManifestEntry(t *testing.T, entry *basev0.SourceManifestEntry, mode uint32, kind basev0.SourceEntryKind, algorithm basev0.SourceIdentityAlgorithm, body string) {
	t.Helper()
	if entry == nil {
		t.Fatal("source manifest entry is missing")
	}
	digest := sha256.Sum256([]byte(body))
	if entry.GetMode() != mode || entry.GetKind() != kind || entry.GetIdentity().GetAlgorithm() != algorithm || entry.GetIdentity().GetDigest() != hex.EncodeToString(digest[:]) || entry.GetIdentity().GetSizeBytes() != int64(len(body)) || entry.GetAttributes() == nil {
		t.Fatalf("source manifest entry = %+v", entry)
	}
}
