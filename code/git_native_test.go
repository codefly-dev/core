package code

import (
	"context"
	"testing"
)

func TestNativeGit_OpenAndLog(t *testing.T) {
	// Use the codefly core repo itself as test subject
	ng := OpenNativeGit("..") // parent = codefly.dev/core/.. = codefly.dev
	if ng == nil {
		// Try current dir
		ng = OpenNativeGit(".")
	}
	if ng == nil {
		t.Skip("no git repo found for testing")
	}

	ctx := context.Background()

	// HEAD
	head, err := ng.HEAD()
	if err != nil {
		t.Fatal("HEAD:", err)
	}
	if len(head) != 40 {
		t.Fatalf("expected 40-char hash, got %d: %s", len(head), head)
	}

	// Log
	commits, err := ng.Log(ctx, 5, "", "", "")
	if err != nil {
		t.Fatal("Log:", err)
	}
	if len(commits) == 0 {
		t.Fatal("expected at least 1 commit")
	}
	if len(commits) > 5 {
		t.Fatalf("expected <= 5 commits, got %d", len(commits))
	}

	c := commits[0]
	if c.Hash == "" || c.Author == "" || c.Message == "" {
		t.Fatalf("incomplete commit: %+v", c)
	}
	t.Logf("Latest: %s %s — %s", c.ShortHash, c.Author, c.Message)
}

func TestNativeGit_Show(t *testing.T) {
	ng := OpenNativeGit("..")
	if ng == nil {
		ng = OpenNativeGit(".")
	}
	if ng == nil {
		t.Skip("no git repo found")
	}

	ctx := context.Background()

	// Show a file that definitely exists at HEAD
	content, exists, err := ng.Show(ctx, "HEAD", "go.mod")
	if err != nil {
		t.Fatal("Show:", err)
	}
	if !exists {
		// Try code/go.mod if in different location
		content, exists, err = ng.Show(ctx, "HEAD", "core/go.mod")
		if err != nil {
			t.Fatal("Show fallback:", err)
		}
	}
	if exists && len(content) == 0 {
		t.Fatal("go.mod exists but empty")
	}

	// Non-existent file
	_, exists, err = ng.Show(ctx, "HEAD", "nonexistent-file-xyz.txt")
	if err != nil {
		t.Fatal("Show nonexistent:", err)
	}
	if exists {
		t.Fatal("nonexistent file should not exist")
	}
}

func TestNativeGit_NotARepo(t *testing.T) {
	ng := OpenNativeGit(t.TempDir())
	if ng != nil {
		t.Fatal("expected nil for non-git directory")
	}
}

func TestNativeGit_Branches(t *testing.T) {
	ng := OpenNativeGit("..")
	if ng == nil {
		ng = OpenNativeGit(".")
	}
	if ng == nil {
		t.Skip("no git repo found")
	}

	branches, err := ng.ListBranches()
	if err != nil {
		t.Fatal("ListBranches:", err)
	}
	// Should have at least main/master
	if len(branches) == 0 {
		t.Fatal("expected at least 1 branch")
	}
	t.Logf("Branches: %v", branches)
}
