package code

import (
	"context"
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
				srv := NewDefaultCodeServer(dir)
				srv.FS.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644)
				return dir, srv
			},
		},
		{
			name: "MemoryVFS",
			setupFunc: func(t *testing.T) (string, *DefaultCodeServer) {
				t.Helper()
				dir := "/memtest"
				m := NewMemoryVFSFrom(map[string]string{
					filepath.Join(dir, "hello.go"): "package main\n",
				})
				return dir, NewDefaultCodeServer(dir, WithVFS(m))
			},
		},
	}
}

func TestExecute_ApplyEdit(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			dir, srv := tc.setupFunc(t)
			ctx := context.Background()
			srv.FS.WriteFile(filepath.Join(dir, "edit_me.txt"), []byte("line one\nline two\nline three\n"), 0o644)

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
			srv.Override("fix", func(_ context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
				return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{
					Success: true, Content: "overridden!",
				}}}, nil
			})
			resp, err := srv.Execute(ctx, &codev0.CodeRequest{
				Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{File: "hello.go"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetFix().Content != "overridden!" {
				t.Errorf("expected overridden content, got %q", resp.GetFix().Content)
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
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_Fix{}}, "fix"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_ApplyEdit{}}, "apply_edit"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_ListSymbols{}}, "list_symbols"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_GetDiagnostics{}}, "get_diagnostics"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_GetProjectInfo{}}, "get_project_info"},
		{&codev0.CodeRequest{Operation: &codev0.CodeRequest_ListDependencies{}}, "list_dependencies"},
		{&codev0.CodeRequest{}, ""},
	}
	for _, tt := range tests {
		if got := OperationName(tt.req); got != tt.want {
			t.Errorf("OperationName(%T) = %q, want %q", tt.req.Operation, got, tt.want)
		}
	}
}

func TestExecute_Fix_Default(t *testing.T) {
	for _, tc := range serverTestCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			dir, srv := tc.setupFunc(t)
			ctx := context.Background()
			srv.FS.WriteFile(filepath.Join(dir, "fix_me.go"), []byte("package main\n"), 0o644)

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
