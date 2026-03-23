package code

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

type serverTestCase struct {
	name      string
	setupFunc func(t *testing.T) (string, *DefaultCodeServer)
}

func serverTestCases(t *testing.T) []serverTestCase {
	t.Helper()
	return []serverTestCase{
		{
			name: "LocalVFS",
			setupFunc: func(t *testing.T) (string, *DefaultCodeServer) {
				t.Helper()
				dir := t.TempDir()
				os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world\n"), 0o644)
				os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
				os.WriteFile(filepath.Join(dir, "sub", "nested.go"), []byte("package sub\n"), 0o644)
				return dir, NewDefaultCodeServer(dir)
			},
		},
		{
			name: "MemoryVFS",
			setupFunc: func(t *testing.T) (string, *DefaultCodeServer) {
				t.Helper()
				dir := "/memtest"
				m := NewMemoryVFSFrom(map[string]string{
					filepath.Join(dir, "hello.txt"):        "hello world\n",
					filepath.Join(dir, "sub", "nested.go"): "package sub\n",
				})
				return dir, NewDefaultCodeServer(dir, WithVFS(m))
			},
		},
	}
}

func TestExecute_ReadFile(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "hello.txt"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			rf := resp.GetReadFile()
			if rf == nil {
				t.Fatal("expected ReadFileResponse")
			}
			if !rf.Exists || rf.Content != "hello world\n" {
				t.Errorf("unexpected: exists=%v content=%q", rf.Exists, rf.Content)
			}
		})
	}
}

func TestExecute_ReadFile_NotFound(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "nope.txt"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetReadFile().Exists {
				t.Error("expected Exists=false for missing file")
			}
		})
	}
}

func TestExecute_WriteFile(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_WriteFile{WriteFile: &codev0.WriteFileRequest{Path: "new.txt", Content: "data"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !resp.GetWriteFile().Success {
				t.Error("expected success")
			}
			data, readErr := srv.FS.ReadFile(filepath.Join(srv.SourceDir, "new.txt"))
			if readErr != nil {
				t.Fatalf("read back: %v", readErr)
			}
			if string(data) != "data" {
				t.Errorf("file content = %q", data)
			}
		})
	}
}

func TestExecute_ListFiles(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ListFiles{ListFiles: &codev0.ListFilesRequest{Recursive: true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			files := resp.GetListFiles().Files
			if len(files) < 2 {
				t.Errorf("expected at least 2 files, got %d", len(files))
			}
		})
	}
}

func TestExecute_CreateFile(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_CreateFile{CreateFile: &codev0.CreateFileRequest{Path: "brand_new.txt", Content: "brand new"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !resp.GetCreateFile().Success {
				t.Error("expected success")
			}
			resp, err = srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_CreateFile{CreateFile: &codev0.CreateFileRequest{Path: "brand_new.txt", Content: "again"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetCreateFile().Success {
				t.Error("expected failure on duplicate create without overwrite")
			}
			data, _ := srv.FS.ReadFile(filepath.Join(srv.SourceDir, "brand_new.txt"))
			if string(data) != "brand new" {
				t.Errorf("content should be unchanged, got %q", data)
			}
		})
	}
}

func TestExecute_DeleteFile(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_DeleteFile{DeleteFile: &codev0.DeleteFileRequest{Path: "hello.txt"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !resp.GetDeleteFile().Success {
				t.Error("expected success")
			}
			resp, err = srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_DeleteFile{DeleteFile: &codev0.DeleteFileRequest{Path: "hello.txt"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDeleteFile().Success {
				t.Error("expected failure for already deleted file")
			}
		})
	}
}

func TestExecute_MoveFile(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_MoveFile{MoveFile: &codev0.MoveFileRequest{OldPath: "hello.txt", NewPath: "moved.txt"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !resp.GetMoveFile().Success {
				t.Error("expected success")
			}
			if _, err := srv.FS.Stat(filepath.Join(srv.SourceDir, "moved.txt")); err != nil {
				t.Error("moved file should exist")
			}
			if _, err := srv.FS.Stat(filepath.Join(srv.SourceDir, "hello.txt")); err == nil {
				t.Error("original file should be gone")
			}
		})
	}
}

func TestExecute_ApplyEdit(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			srv.FS.WriteFile(filepath.Join(srv.SourceDir, "edit_me.txt"), []byte("line one\nline two\nline three\n"), 0o644)

			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
					File: "edit_me.txt", Find: "line two", Replace: "line TWO",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			ae := resp.GetApplyEdit()
			if !ae.Success {
				t.Fatalf("expected success, got error: %s", ae.Error)
			}
			if ae.Strategy != "exact" {
				t.Errorf("expected exact strategy, got %s", ae.Strategy)
			}
		})
	}
}

func TestExecute_Override(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			srv.Override("read_file", func(_ context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
				return &codev0.CodeResponse{Result: &codev0.CodeResponse_ReadFile{ReadFile: &codev0.ReadFileResponse{
					Content: "overridden!", Exists: true,
				}}}, nil
			})
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "hello.txt"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetReadFile().Content != "overridden!" {
				t.Errorf("expected overridden content, got %q", resp.GetReadFile().Content)
			}
		})
	}
}

func TestExecute_EmptyRequest(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			_, err := srv.Execute(ctx, &codev0.CodeRequest{})
			if err == nil {
				t.Error("expected error for empty request")
			}
		})
	}
}

func TestOperationName(t *testing.T) {
	tests := []struct {
		req  *codev0.CodeRequest
		want string
	}{
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_ReadFile{}}, "read_file"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_WriteFile{}}, "write_file"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_Fix{}}, "fix"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_Search{}}, "search"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_ListSymbols{}}, "list_symbols"},
		{&codev0.CodeRequest{}, ""},
	}
	for _, tt := range tests {
		if got := OperationName(tt.req); got != tt.want {
			t.Errorf("OperationName() = %q, want %q", got, tt.want)
		}
	}
}

func TestExecute_Search(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			srv.FS.WriteFile(filepath.Join(srv.SourceDir, "searchable.go"), []byte("package main\n// needle in a haystack\nfunc main() {}\n"), 0o644)

			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{Pattern: "needle", Literal: true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			sr := resp.GetSearch()
			if sr == nil {
				t.Fatal("expected SearchResponse")
			}
			if len(sr.Matches) == 0 {
				t.Error("expected at least one match for 'needle'")
			}
		})
	}
}

func TestExecute_Fix_Default(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			srv.FS.WriteFile(filepath.Join(srv.SourceDir, "fix_me.go"), []byte("package main\n"), 0o644)

			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{File: "fix_me.go"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			fr := resp.GetFix()
			if fr == nil {
				t.Fatal("expected FixResponse")
			}
			if !fr.Success {
				t.Error("default fix should succeed (no-op)")
			}
			if fr.Content != "package main\n" {
				t.Errorf("expected file content unchanged, got %q", fr.Content)
			}
		})
	}
}

func TestExecute_GetProjectInfo(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			pi := resp.GetGetProjectInfo()
			if pi == nil {
				t.Fatal("expected GetProjectInfoResponse")
			}
			if len(pi.FileHashes) == 0 {
				t.Error("expected at least one file hash")
			}
		})
	}
}

func TestExecute_ListSymbols_Stub(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: &codev0.ListSymbolsRequest{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			ls := resp.GetListSymbols()
			if ls == nil {
				t.Fatal("expected ListSymbolsResponse")
			}
			if ls.Status == nil || ls.Status.State != codev0.ListSymbolsStatus_ERROR {
				t.Error("LSP stub should return ERROR status")
			}
		})
	}
}

func TestExecute_ListDependencies_Stub(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := tc.setupFunc(t)
			ctx := context.Background()
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_ListDependencies{ListDependencies: &codev0.ListDependenciesRequest{}},
			})
			if err != nil {
				t.Fatal(err)
			}
			ld := resp.GetListDependencies()
			if ld == nil {
				t.Fatal("expected ListDependenciesResponse")
			}
			if ld.Error == "" {
				t.Error("dependency stub should return an error message")
			}
		})
	}
}
