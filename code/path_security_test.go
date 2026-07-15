package code

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestDefaultCodeServerRejectsTraversalForEveryPathOperation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.txt")
	inside := filepath.Join(root, "inside.txt")
	s := NewDefaultCodeServer(root)
	ctx := context.Background()

	tests := []struct {
		name string
		run  func() error
	}{
		{"read", func() error { _, err := s.readFile(ctx, &codev0.ReadFileRequest{Path: "../outside.txt"}); return err }},
		{"write", func() error {
			_, err := s.writeFile(ctx, &codev0.WriteFileRequest{Path: "../outside.txt", Content: "changed"})
			return err
		}},
		{"list", func() error { _, err := s.listFiles(ctx, &codev0.ListFilesRequest{Path: ".."}); return err }},
		{"delete", func() error {
			_, err := s.deleteFile(ctx, &codev0.DeleteFileRequest{Path: "../outside.txt"})
			return err
		}},
		{"move-source", func() error {
			_, err := s.moveFile(ctx, &codev0.MoveFileRequest{OldPath: "../outside.txt", NewPath: "moved.txt"})
			return err
		}},
		{"move-destination", func() error {
			_, err := s.moveFile(ctx, &codev0.MoveFileRequest{OldPath: "inside.txt", NewPath: "../outside.txt"})
			return err
		}},
		{"create", func() error {
			_, err := s.createFile(ctx, &codev0.CreateFileRequest{Path: "../outside.txt", Content: "changed", Overwrite: true})
			return err
		}},
		{"search", func() error {
			_, err := s.search(ctx, &codev0.SearchRequest{Path: "..", Pattern: "secret"})
			return err
		}},
		{"apply-edit", func() error {
			_, err := s.applyEdit(ctx, &codev0.ApplyEditRequest{File: "../outside.txt", Find: "secret", Replace: "changed"})
			return err
		}},
		{"fix", func() error { _, err := s.fixDefault(ctx, &codev0.FixRequest{File: "../outside.txt"}); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tt.run(); err == nil {
				t.Fatal("expected traversal to be rejected")
			}
			got, err := os.ReadFile(outside)
			if err != nil {
				t.Fatalf("outside file was removed: %v", err)
			}
			if string(got) != "secret" {
				t.Fatalf("outside file changed to %q", got)
			}
		})
	}
}

func TestDefaultCodeServerDoesNotFollowEscapingSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	s := NewDefaultCodeServer(root)
	resp, err := s.readFile(context.Background(), &codev0.ReadFileRequest{Path: "escape/secret.txt"})
	if err != nil {
		return // Rejection is also a safe outcome.
	}
	if got := resp.GetReadFile(); got != nil && got.Content == "outside-secret" {
		t.Fatal("read escaped the workspace through a symlink")
	}
}

func TestFileOpsRejectsAbsoluteAndTraversalPaths(t *testing.T) {
	root := t.TempDir()
	ops := NewFileOps(LocalVFS{}, root)
	ctx := context.Background()

	if _, err := ops.ReadFile(ctx, filepath.Join(root, "file.txt")); err == nil {
		t.Fatal("absolute path was accepted")
	}
	if err := ops.WriteFile(ctx, "../outside.txt", []byte("no")); err == nil {
		t.Fatal("traversal path was accepted")
	}
}

func TestGitOperationsRejectOptionInjectionAndEscapingPaths(t *testing.T) {
	s := NewDefaultCodeServer(t.TempDir())
	ctx := context.Background()

	if _, err := s.gitDiff(ctx, &codev0.GitDiffRequest{
		BaseRef: "--no-index",
		HeadRef: "/etc/hosts",
		Path:    "/etc/passwd",
	}); err == nil {
		t.Fatal("git diff accepted option injection that enables arbitrary --no-index paths")
	}
	if _, err := s.gitShow(ctx, &codev0.GitShowRequest{
		Ref:  "--output=/tmp/codefly-git-show",
		Path: "",
	}); err == nil {
		t.Fatal("git show accepted an option in place of a ref")
	}
	if _, err := s.gitBlame(ctx, &codev0.GitBlameRequest{Path: "../../etc/passwd"}); err == nil {
		t.Fatal("git blame accepted an escaping path")
	}
}
