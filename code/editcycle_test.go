package code

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// scaffoldHTTPProject creates a minimal Go HTTP server with tests in dir.
// This is the "before" state -- a working project with a root endpoint.
func scaffoldHTTPProject(t *testing.T, dir string) {
	t.Helper()

	goMod := `module testserver

go 1.21
`
	mainGo := `package main

import (
	"fmt"
	"net/http"
	"os"
)

func setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", handleRoot)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func main() {
	mux := http.NewServeMux()
	setupRoutes(mux)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.ListenAndServe(":"+port, mux)
}
`
	mainTestGo := `package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleRoot(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected 'ok', got %q", w.Body.String())
	}
}
`

	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644)
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(mainTestGo), 0o644)
}

// TestEditCycle_VirtualEndpoint is an advanced end-to-end test that validates
// the full virtual edit → build → test → rollback cycle through the Code API.
//
// Flow (mirrors what an agent via the gateway does):
//  1. Scaffold a Go HTTP server in a temp dir
//  2. Verify it builds and tests pass (baseline)
//  3. Wrap with OverlayVFS -- all subsequent Code API calls go through the overlay
//  4. Use Execute(ApplyEdit) to add a /health endpoint + its test (simulated agent)
//  5. Verify the virtual file has the edit (ReadFile through overlay)
//  6. Commit the overlay so go toolchain can see the changes
//  7. Build and run tests -- verify the new /health endpoint passes
//  8. Rollback the overlay
//  9. Verify original source is restored
func TestEditCycle_VirtualEndpoint(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	dir := t.TempDir()
	scaffoldHTTPProject(t, dir)

	ctx := context.Background()

	// --- Phase 1: Verify baseline compiles and tests pass ---
	t.Log("Phase 1: Verifying baseline project")
	runGoCommand(t, dir, "build", ".")
	runGoCommand(t, dir, "test", "-v", ".")

	origMain, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read original main.go: %v", err)
	}
	origTest, err := os.ReadFile(filepath.Join(dir, "main_test.go"))
	if err != nil {
		t.Fatalf("read original main_test.go: %v", err)
	}

	// --- Phase 2: Wrap with OverlayVFS ---
	t.Log("Phase 2: Setting up OverlayVFS")
	overlay := NewOverlayVFS(LocalVFS{})
	srv := NewDefaultCodeServer(dir, WithVFS(overlay))

	// Verify we can still read through the overlay
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("ReadFile through overlay: %v", err)
	}
	if !resp.GetReadFile().Exists {
		t.Fatal("main.go should exist through overlay")
	}

	// --- Phase 3: Apply edits via Code API (simulated agent) ---
	t.Log("Phase 3: Applying virtual edits via Code API")

	// Edit 1: Add handleHealth function and register it in setupRoutes
	editResp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
			File: "main.go",
			Find: `func setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", handleRoot)
}`,
			Replace: `func setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/health", handleHealth)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, ` + "`" + `{"status":"healthy"}` + "`" + `)
}`,
		}},
	})
	if err != nil {
		t.Fatalf("ApplyEdit main.go: %v", err)
	}
	if !editResp.GetApplyEdit().Success {
		t.Fatalf("ApplyEdit main.go failed: %s", editResp.GetApplyEdit().Error)
	}
	t.Logf("  main.go edit: strategy=%s", editResp.GetApplyEdit().Strategy)

	// Edit 2: Add test for /health endpoint
	editResp, err = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
			File: "main_test.go",
			Find: `func TestHandleRoot(t *testing.T) {`,
			Replace: `func TestHandleHealth(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != ` + "`" + `{"status":"healthy"}` + "`" + ` {
		t.Errorf("expected health JSON, got %q", w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
}

func TestHandleRoot(t *testing.T) {`,
		}},
	})
	if err != nil {
		t.Fatalf("ApplyEdit main_test.go: %v", err)
	}
	if !editResp.GetApplyEdit().Success {
		t.Fatalf("ApplyEdit main_test.go failed: %s", editResp.GetApplyEdit().Error)
	}
	t.Logf("  main_test.go edit: strategy=%s", editResp.GetApplyEdit().Strategy)

	// --- Phase 4: Verify overlay state ---
	t.Log("Phase 4: Verifying overlay state")

	if !overlay.Dirty() {
		t.Fatal("overlay should be dirty after edits")
	}

	changes := overlay.Diff()
	t.Logf("  overlay changes: %d", len(changes))
	for _, c := range changes {
		t.Logf("    %s: %s", c.Type, c.Path)
	}

	// Verify the edited content is visible through the Code API
	resp, err = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "main.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.GetReadFile().Content, "handleHealth") {
		t.Error("virtual main.go should contain handleHealth")
	}
	if !strings.Contains(resp.GetReadFile().Content, "/health") {
		t.Error("virtual main.go should contain /health route")
	}

	// Verify search finds the new function through overlay
	searchResp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{
			Pattern: "handleHealth", Literal: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if searchResp.GetSearch().TotalMatches < 2 {
		t.Errorf("search should find handleHealth in at least 2 places (definition + registration), got %d",
			searchResp.GetSearch().TotalMatches)
	}

	// Verify original files on disk are UNTOUCHED
	diskMain, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if string(diskMain) != string(origMain) {
		t.Error("CRITICAL: original main.go on disk was modified by overlay!")
	}

	// --- Phase 5: Commit overlay and build/test ---
	t.Log("Phase 5: Committing overlay and running go build + go test")

	if err := overlay.Commit(); err != nil {
		t.Fatalf("overlay commit: %v", err)
	}
	if overlay.Dirty() {
		t.Error("overlay should not be dirty after commit")
	}

	// Now the real files have the edits -- go toolchain can see them
	runGoCommand(t, dir, "build", ".")
	t.Log("  go build: PASS")

	out := runGoCommand(t, dir, "test", "-v", ".")
	t.Log("  go test output:")
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			t.Logf("    %s", line)
		}
	}

	if !strings.Contains(out, "TestHandleHealth") {
		t.Error("test output should contain TestHandleHealth")
	}
	if !strings.Contains(out, "TestHandleRoot") {
		t.Error("test output should contain TestHandleRoot")
	}
	if strings.Contains(out, "FAIL") {
		t.Error("tests should not fail after commit")
	}

	// --- Phase 6: Restore original and verify ---
	t.Log("Phase 6: Restoring original state")

	// Write back the originals (simulating a rollback to pre-edit state)
	os.WriteFile(filepath.Join(dir, "main.go"), origMain, 0o644)
	os.WriteFile(filepath.Join(dir, "main_test.go"), origTest, 0o644)

	// Verify originals are back
	restored, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if string(restored) != string(origMain) {
		t.Error("main.go not properly restored")
	}
	if strings.Contains(string(restored), "handleHealth") {
		t.Error("restored main.go should NOT contain handleHealth")
	}

	// Verify baseline still builds and tests pass
	runGoCommand(t, dir, "build", ".")
	runGoCommand(t, dir, "test", "-v", ".")

	t.Log("Edit cycle complete: virtual edit → build → test → restore → verify")
}

// TestEditCycle_OverlayRollback tests that Rollback() properly discards
// virtual edits without ever touching the real filesystem.
func TestEditCycle_OverlayRollback(t *testing.T) {
	dir := t.TempDir()
	scaffoldHTTPProject(t, dir)
	ctx := context.Background()

	origMain, _ := os.ReadFile(filepath.Join(dir, "main.go"))

	overlay := NewOverlayVFS(LocalVFS{})
	srv := NewDefaultCodeServer(dir, WithVFS(overlay))

	// Write a new file virtually
	writeResp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_WriteFile{WriteFile: &codev0.WriteFileRequest{
			Path: "middleware.go", Content: "package main\n\nfunc Logger() {}\n",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !writeResp.GetWriteFile().Success {
		t.Fatal("virtual write should succeed")
	}

	// Modify existing file virtually
	srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
			File: "main.go", Find: `fmt.Fprintf(w, "ok")`, Replace: `fmt.Fprintf(w, "modified")`,
		}},
	})

	// Verify virtual state
	readResp, _ := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "middleware.go"}},
	})
	if !readResp.GetReadFile().Exists {
		t.Error("middleware.go should exist virtually")
	}

	readResp, _ = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "main.go"}},
	})
	if !strings.Contains(readResp.GetReadFile().Content, "modified") {
		t.Error("virtual main.go should contain 'modified'")
	}

	if len(overlay.Diff()) == 0 {
		t.Error("should have pending changes")
	}

	// ROLLBACK
	overlay.Rollback()

	// Verify: virtual file gone
	readResp, _ = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "middleware.go"}},
	})
	if readResp.GetReadFile().Exists {
		t.Error("middleware.go should NOT exist after rollback")
	}

	// Verify: original content restored
	readResp, _ = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ReadFile{ReadFile: &codev0.ReadFileRequest{Path: "main.go"}},
	})
	if readResp.GetReadFile().Content != string(origMain) {
		t.Error("main.go should be restored to original after rollback")
	}

	// Verify: disk untouched
	diskMain, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if string(diskMain) != string(origMain) {
		t.Error("CRITICAL: disk was modified despite overlay")
	}
	if _, err := os.Stat(filepath.Join(dir, "middleware.go")); err == nil {
		t.Error("CRITICAL: middleware.go exists on disk despite being overlay-only")
	}

	t.Log("Rollback verified: all virtual changes discarded, disk untouched")
}

// TestEditCycle_MultipleEditsBeforeCommit tests applying several edits
// through the Code API, verifying the cumulative state, then committing once.
func TestEditCycle_MultipleEditsBeforeCommit(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	dir := t.TempDir()
	scaffoldHTTPProject(t, dir)
	ctx := context.Background()

	overlay := NewOverlayVFS(LocalVFS{})
	srv := NewDefaultCodeServer(dir, WithVFS(overlay))

	// Edit 1: Add a handler via CreateFile
	srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_CreateFile{CreateFile: &codev0.CreateFileRequest{
			Path: "handlers.go",
			Content: `package main

import (
	"fmt"
	"net/http"
)

func handlePing(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "pong")
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "v1.0.0")
}
`,
		}},
	})

	// Edit 2: Register the new handlers
	srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
			File: "main.go",
			Find: `	mux.HandleFunc("/", handleRoot)`,
			Replace: `	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/ping", handlePing)
	mux.HandleFunc("/version", handleVersion)`,
		}},
	})

	// Edit 3: Add tests for the new handlers
	srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_CreateFile{CreateFile: &codev0.CreateFileRequest{
			Path: "handlers_test.go",
			Content: `package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePing(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 || w.Body.String() != "pong" {
		t.Errorf("ping: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestHandleVersion(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)

	req := httptest.NewRequest("GET", "/version", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 || w.Body.String() != "v1.0.0" {
		t.Errorf("version: code=%d body=%q", w.Code, w.Body.String())
	}
}
`,
		}},
	})

	// Verify cumulative overlay state
	changes := overlay.Diff()
	t.Logf("Pending changes: %d", len(changes))
	for _, c := range changes {
		t.Logf("  %s: %s", c.Type, c.Path)
	}

	// List files through overlay -- should include new files
	listResp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListFiles{ListFiles: &codev0.ListFilesRequest{
			Recursive: true, Extensions: []string{".go"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fileNames := make(map[string]bool)
	for _, f := range listResp.GetListFiles().Files {
		fileNames[f.Path] = true
	}
	if !fileNames["handlers.go"] {
		t.Error("handlers.go should appear in file listing")
	}
	if !fileNames["handlers_test.go"] {
		t.Error("handlers_test.go should appear in file listing")
	}

	// Commit and verify with go toolchain
	overlay.Commit()
	runGoCommand(t, dir, "build", ".")
	out := runGoCommand(t, dir, "test", "-v", ".")

	if !strings.Contains(out, "TestHandlePing") {
		t.Error("expected TestHandlePing in output")
	}
	if !strings.Contains(out, "TestHandleVersion") {
		t.Error("expected TestHandleVersion in output")
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("tests should pass, got: %s", out)
	}

	t.Logf("Multiple edits committed and verified: 2 new files, 1 modified, all tests pass")
}

func runGoCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

// TestEditCycle_SearchAcrossOverlay verifies that search works correctly
// when some files exist on disk and others only in the overlay.
func TestEditCycle_SearchAcrossOverlay(t *testing.T) {
	dir := t.TempDir()
	scaffoldHTTPProject(t, dir)
	ctx := context.Background()

	overlay := NewOverlayVFS(LocalVFS{})
	srv := NewDefaultCodeServer(dir, WithVFS(overlay))

	// Original disk file contains "handleRoot"
	resp, err := srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{
			Pattern: "handleRoot", Literal: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseMatches := resp.GetSearch().TotalMatches
	if baseMatches == 0 {
		t.Fatal("should find handleRoot in base files")
	}
	t.Logf("Base search 'handleRoot': %d matches", baseMatches)

	// Add a new virtual file that also references handleRoot
	srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_CreateFile{CreateFile: &codev0.CreateFileRequest{
			Path:    "wrapper.go",
			Content: "package main\n\n// wraps handleRoot for logging\nfunc wrappedRoot() { handleRoot(nil, nil) }\n",
		}},
	})

	// Search should now find more matches (base + overlay)
	resp, err = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{
			Pattern: "handleRoot", Literal: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	overlayMatches := resp.GetSearch().TotalMatches
	if overlayMatches <= baseMatches {
		t.Errorf("overlay search should find more matches: base=%d overlay=%d", baseMatches, overlayMatches)
	}
	t.Logf("Overlay search 'handleRoot': %d matches (was %d)", overlayMatches, baseMatches)

	// Search for something that only exists in the overlay file
	resp, err = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{
			Pattern: "wrappedRoot", Literal: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSearch().TotalMatches == 0 {
		t.Error("should find 'wrappedRoot' in overlay-only file")
	}

	// Rollback and verify search goes back to base-only
	overlay.Rollback()
	resp, err = srv.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_Search{Search: &codev0.SearchRequest{
			Pattern: "wrappedRoot", Literal: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSearch().TotalMatches != 0 {
		t.Error("'wrappedRoot' should not be found after rollback")
	}

	t.Log("Search across overlay verified: finds content in both base and overlay files")
}
