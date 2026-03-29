package code

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// ============================================================
// Stacked Overlays (overlay on overlay -- used per-objective)
// ============================================================

func TestStackedOverlay_IsolatesChanges(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/main.go": "package main\n",
		"/proj/util.go": "package main\n\nfunc Util() {}\n",
	})

	session := NewOverlayVFS(base)
	session.WriteFile("/proj/session.go", []byte("package main // session\n"), 0644)

	obj := NewOverlayVFS(session)
	obj.WriteFile("/proj/obj.go", []byte("package main // objective\n"), 0644)

	if _, err := obj.ReadFile("/proj/obj.go"); err != nil {
		t.Fatal("objective overlay should see its own write")
	}
	if _, err := obj.ReadFile("/proj/session.go"); err != nil {
		t.Fatal("objective overlay should read through to session overlay")
	}
	if _, err := obj.ReadFile("/proj/main.go"); err != nil {
		t.Fatal("objective overlay should read through to base")
	}

	if _, err := session.ReadFile("/proj/obj.go"); err == nil {
		t.Error("session should NOT see objective's writes before commit")
	}
}

func TestStackedOverlay_CommitBubbles(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/a.go": "original\n",
	})
	session := NewOverlayVFS(base)
	obj := NewOverlayVFS(session)

	obj.WriteFile("/proj/a.go", []byte("modified by obj\n"), 0644)
	obj.WriteFile("/proj/new.go", []byte("created by obj\n"), 0644)

	if err := obj.Commit(); err != nil {
		t.Fatal(err)
	}

	data, _ := session.ReadFile("/proj/a.go")
	if string(data) != "modified by obj\n" {
		t.Errorf("session should see obj's change after commit, got %q", data)
	}
	data, _ = session.ReadFile("/proj/new.go")
	if string(data) != "created by obj\n" {
		t.Error("session should see obj's new file after commit")
	}

	baseData, _ := base.ReadFile("/proj/a.go")
	if string(baseData) != "original\n" {
		t.Error("base should be unchanged until session commits")
	}
}

func TestStackedOverlay_RollbackIsolates(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/proj/x.go": "base\n"})
	session := NewOverlayVFS(base)
	obj := NewOverlayVFS(session)

	obj.WriteFile("/proj/x.go", []byte("bad change\n"), 0644)
	obj.Rollback()

	data, _ := session.ReadFile("/proj/x.go")
	if string(data) != "base\n" {
		t.Errorf("session should be unaffected by rollback, got %q", data)
	}
}

func TestStackedOverlay_TwoObjectivesParallel(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/shared.go": "package main\n",
	})
	session := NewOverlayVFS(base)

	objA := NewOverlayVFS(session)
	objB := NewOverlayVFS(session)

	objA.WriteFile("/proj/a.go", []byte("package main // A\n"), 0644)
	objB.WriteFile("/proj/b.go", []byte("package main // B\n"), 0644)

	if _, err := objA.ReadFile("/proj/b.go"); err == nil {
		t.Error("objA should NOT see objB's writes")
	}
	if _, err := objB.ReadFile("/proj/a.go"); err == nil {
		t.Error("objB should NOT see objA's writes")
	}

	objA.Commit()
	objB.Commit()

	dataA, _ := session.ReadFile("/proj/a.go")
	dataB, _ := session.ReadFile("/proj/b.go")
	if string(dataA) != "package main // A\n" || string(dataB) != "package main // B\n" {
		t.Error("session should see both after commits")
	}
}

func TestStackedOverlay_ThreeLevels(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/f": "L0"})
	l1 := NewOverlayVFS(base)
	l2 := NewOverlayVFS(l1)
	l3 := NewOverlayVFS(l2)

	l3.WriteFile("/f", []byte("L3"), 0644)
	data, _ := l3.ReadFile("/f")
	if string(data) != "L3" {
		t.Errorf("L3 should see its own write, got %q", data)
	}
	data, _ = l2.ReadFile("/f")
	if string(data) != "L0" {
		t.Errorf("L2 should still see base, got %q", data)
	}

	l3.Commit()
	data, _ = l2.ReadFile("/f")
	if string(data) != "L3" {
		t.Errorf("L2 should see L3's commit, got %q", data)
	}
	data, _ = l1.ReadFile("/f")
	if string(data) != "L0" {
		t.Errorf("L1 should still see base, got %q", data)
	}

	l2.Commit()
	data, _ = l1.ReadFile("/f")
	if string(data) != "L3" {
		t.Errorf("L1 should see L3's change after two commits, got %q", data)
	}
}

func TestStackedOverlay_DeleteBubblesUp(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/proj/kill.go": "dead"})
	session := NewOverlayVFS(base)
	obj := NewOverlayVFS(session)

	obj.Remove("/proj/kill.go")
	if _, err := obj.ReadFile("/proj/kill.go"); !os.IsNotExist(err) {
		t.Error("obj should not see deleted file")
	}
	if _, err := session.ReadFile("/proj/kill.go"); err != nil {
		t.Error("session should still see file before commit")
	}

	obj.Commit()
	if _, err := session.ReadFile("/proj/kill.go"); !os.IsNotExist(err) {
		t.Error("session should not see file after obj commits delete")
	}
}

func TestStackedOverlay_DiffAtEachLevel(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/f": "v0"})
	session := NewOverlayVFS(base)
	obj := NewOverlayVFS(session)

	session.WriteFile("/f", []byte("v1-session"), 0644)
	obj.WriteFile("/f", []byte("v2-obj"), 0644)

	objDiff := obj.Diff()
	if len(objDiff) != 1 || objDiff[0].Type != "modify" || string(objDiff[0].Content) != "v2-obj" {
		t.Errorf("obj diff unexpected: %+v", objDiff)
	}

	sessDiff := session.Diff()
	if len(sessDiff) != 1 || string(sessDiff[0].Content) != "v1-session" {
		t.Errorf("session diff unexpected: %+v", sessDiff)
	}
}

// ============================================================
// Full File Visibility (WalkDir/ReadDir with overlay files)
// ============================================================

func TestOverlayVFS_WalkDir_SeesOverlayFiles(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/existing.go": "package main\n",
	})
	ov := NewOverlayVFS(base)
	ov.WriteFile("/proj/new.go", []byte("package main\n"), 0644)
	ov.WriteFile("/proj/sub/deep.go", []byte("package sub\n"), 0644)

	var paths []string
	ov.WalkDir("/proj", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel("/proj", path)
		if rel != "." {
			paths = append(paths, rel)
		}
		return nil
	})
	sort.Strings(paths)

	expected := []string{"existing.go", "new.go", "sub", "sub/deep.go"}
	if len(paths) != len(expected) {
		t.Fatalf("WalkDir paths = %v, want %v", paths, expected)
	}
	for i := range expected {
		if paths[i] != expected[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], expected[i])
		}
	}
}

func TestOverlayVFS_WalkDir_HidesDeletedFiles(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/keep.go":   "keep\n",
		"/proj/delete.go": "delete\n",
	})
	ov := NewOverlayVFS(base)
	ov.Remove("/proj/delete.go")

	var paths []string
	ov.WalkDir("/proj", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel("/proj", path)
		if rel != "." {
			paths = append(paths, rel)
		}
		return nil
	})

	if len(paths) != 1 || paths[0] != "keep.go" {
		t.Errorf("expected only keep.go, got %v", paths)
	}
}

func TestOverlayVFS_ReadDir_MergesOverlayAndBase(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/base.go": "base\n",
	})
	ov := NewOverlayVFS(base)
	ov.WriteFile("/proj/overlay.go", []byte("overlay\n"), 0644)
	ov.WriteFile("/proj/sub/nested.go", []byte("nested\n"), 0644)

	entries, err := ov.ReadDir("/proj")
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	expected := []string{"base.go", "overlay.go", "sub"}
	if len(names) != len(expected) {
		t.Fatalf("ReadDir = %v, want %v", names, expected)
	}
	for i := range expected {
		if names[i] != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], expected[i])
		}
	}
}

func TestOverlayVFS_ReadDir_DeletedNotShown(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/a.go": "a\n",
		"/proj/b.go": "b\n",
		"/proj/c.go": "c\n",
	})
	ov := NewOverlayVFS(base)
	ov.Remove("/proj/b.go")

	entries, _ := ov.ReadDir("/proj")
	for _, e := range entries {
		if e.Name() == "b.go" {
			t.Error("deleted file should not appear in ReadDir")
		}
	}
}

func TestOverlayVFS_WalkDir_OverlayCreatedDirs(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)
	ov.MkdirAll("/proj/a/b/c", 0755)
	ov.WriteFile("/proj/a/b/c/deep.txt", []byte("deep"), 0644)

	var paths []string
	ov.WalkDir("/proj", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel("/proj", path)
		if rel != "." {
			paths = append(paths, rel)
		}
		return nil
	})
	sort.Strings(paths)

	expected := []string{"a", "a/b", "a/b/c", "a/b/c/deep.txt"}
	if len(paths) != len(expected) {
		t.Fatalf("WalkDir got %v, want %v", paths, expected)
	}
	for i := range expected {
		if paths[i] != expected[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], expected[i])
		}
	}
}

// ============================================================
// Full Lifecycle: create, modify, delete, rename, commit
// ============================================================

func TestOverlayVFS_FullLifecycle(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/original.go": "package original\n",
	})
	ov := NewOverlayVFS(base)

	ov.WriteFile("/proj/new.go", []byte("package new\n"), 0644)
	ov.WriteFile("/proj/original.go", []byte("package modified\n"), 0644)

	ov.WriteFile("/proj/temp.go", []byte("temp\n"), 0644)
	ov.Remove("/proj/temp.go")

	ov.Rename("/proj/new.go", "/proj/renamed.go")

	diff := ov.Diff()
	byPath := make(map[string]FileChange)
	for _, c := range diff {
		byPath[c.Path] = c
	}

	if c, ok := byPath["/proj/original.go"]; !ok || c.Type != "modify" {
		t.Error("original.go should be modify")
	}
	if c, ok := byPath["/proj/renamed.go"]; !ok || c.Type != "create" {
		t.Error("renamed.go should be create")
	}

	if err := ov.Commit(); err != nil {
		t.Fatal(err)
	}

	data, _ := base.ReadFile("/proj/original.go")
	if string(data) != "package modified\n" {
		t.Error("base should have modified content after commit")
	}
	data, _ = base.ReadFile("/proj/renamed.go")
	if string(data) != "package new\n" {
		t.Error("base should have renamed file after commit")
	}
}

func TestOverlayVFS_CommitDeletesPropagateToBase(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/proj/a.go": "a",
		"/proj/b.go": "b",
	})
	ov := NewOverlayVFS(base)
	ov.Remove("/proj/a.go")

	if err := ov.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := base.ReadFile("/proj/a.go"); !os.IsNotExist(err) {
		t.Error("base should not have deleted file after commit")
	}
	data, _ := base.ReadFile("/proj/b.go")
	if string(data) != "b" {
		t.Error("unmodified file should survive commit")
	}
}

// ============================================================
// Stat behavior through overlay
// ============================================================

func TestOverlayVFS_Stat_OverlayFile(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)
	ov.WriteFile("/proj/x.go", []byte("content here"), 0644)

	info, err := ov.Stat("/proj/x.go")
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Error("file should not be dir")
	}
	if info.Size() != int64(len("content here")) {
		t.Errorf("size = %d, want %d", info.Size(), len("content here"))
	}
}

func TestOverlayVFS_Stat_DeletedFile(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/f.txt": "hello"})
	ov := NewOverlayVFS(base)
	ov.Remove("/f.txt")

	_, err := ov.Stat("/f.txt")
	if !os.IsNotExist(err) {
		t.Errorf("deleted file should not be statable, got %v", err)
	}
}

func TestOverlayVFS_Stat_OverlayDir(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)
	ov.MkdirAll("/proj/sub/deep", 0755)

	info, err := ov.Stat("/proj/sub")
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("should be dir")
	}
}

func TestOverlayVFS_Stat_ModifiedFileSize(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/f": "short"})
	ov := NewOverlayVFS(base)
	ov.WriteFile("/f", []byte("this is a much longer string now"), 0644)

	info, err := ov.Stat("/f")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len("this is a much longer string now")) {
		t.Errorf("stat size should reflect overlay, got %d", info.Size())
	}
}

// ============================================================
// Concurrent access safety
// ============================================================

func TestOverlayVFS_ConcurrentWritesSafe(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			path := "/proj/" + strings.Repeat("a", n%10+1) + ".go"
			ov.WriteFile(path, []byte("data"), 0644)
			ov.ReadFile(path)
			ov.Stat(path)
		}(i)
	}
	wg.Wait()

	if !ov.Dirty() {
		t.Error("should be dirty after concurrent writes")
	}
}

func TestOverlayVFS_ConcurrentReadsSafe(t *testing.T) {
	files := make(map[string]string)
	for i := 0; i < 20; i++ {
		files["/proj/"+string(rune('a'+i))+".go"] = "package main\n"
	}
	base := NewMemoryVFSFrom(files)
	ov := NewOverlayVFS(base)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "/proj/" + string(rune('a'+n%20)) + ".go"
			data, err := ov.ReadFile(key)
			if err != nil {
				t.Errorf("concurrent read failed: %v", err)
				return
			}
			if string(data) != "package main\n" {
				t.Errorf("unexpected content: %q", data)
			}
		}(i)
	}
	wg.Wait()
}

// ============================================================
// Code Server through OverlayVFS (full integration)
// ============================================================

func TestCodeServer_ViaOverlay_ReadWriteSearch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)

	base := LocalVFS{}
	ov := NewOverlayVFS(base)
	code := NewGoCodeServer(dir, []ServerOption{WithVFS(ov)})

	ctx := t.Context()

	_, err := code.FileOps().ReadFile(ctx, "main.go")
	if err != nil {
		t.Fatalf("should read existing file through overlay: %v", err)
	}

	if err := code.FileOps().WriteFile(ctx, "helper.go", []byte("package main\n\nfunc Helper() int { return 42 }\n")); err != nil {
		t.Fatal(err)
	}

	helperContent, err := code.FileOps().ReadFile(ctx, "helper.go")
	if err != nil {
		t.Fatal("should read overlay-written file:", err)
	}
	if !strings.Contains(string(helperContent), "Helper") {
		t.Error("content should contain Helper")
	}

	if _, err := os.Stat(filepath.Join(dir, "helper.go")); !os.IsNotExist(err) {
		t.Error("overlay file should not be on disk yet")
	}

	if err := ov.Commit(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "helper.go"))
	if !strings.Contains(string(data), "Helper") {
		t.Error("file should be on disk after commit")
	}
}

func TestCodeServer_ViaOverlay_ListFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing.go"), []byte("package main\n"), 0644)

	base := LocalVFS{}
	ov := NewOverlayVFS(base)
	code := NewGoCodeServer(dir, []ServerOption{WithVFS(ov)})

	ctx := t.Context()

	code.FileOps().WriteFile(ctx, "new.go", []byte("package main\n"))

	files, err := code.FileOps().ListFiles(ctx, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	hasExisting := false
	hasNew := false
	for _, f := range files {
		if strings.Contains(f, "existing.go") {
			hasExisting = true
		}
		if strings.Contains(f, "new.go") {
			hasNew = true
		}
	}
	if !hasExisting {
		t.Error("should list existing.go")
	}
	if !hasNew {
		t.Error("should list overlay new.go")
	}
}

func TestCodeServer_ViaOverlay_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "victim.go"), []byte("package main\n"), 0644)

	base := LocalVFS{}
	ov := NewOverlayVFS(base)
	code := NewGoCodeServer(dir, []ServerOption{WithVFS(ov)})

	ctx := t.Context()

	if err := code.FileOps().DeleteFile(ctx, "victim.go"); err != nil {
		t.Fatal(err)
	}

	_, readErr := code.FileOps().ReadFile(ctx, "victim.go")
	if readErr == nil {
		t.Error("deleted file should not exist via overlay")
	}

	if _, err := os.Stat(filepath.Join(dir, "victim.go")); os.IsNotExist(err) {
		t.Error("file should still be on disk before commit")
	}
}

func TestCodeServer_ViaOverlay_ApplyEdit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0644)

	ov := NewOverlayVFS(LocalVFS{})
	code := NewGoCodeServer(dir, []ServerOption{WithVFS(ov)})

	ctx := t.Context()

	resp, err := code.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
			File: "main.go", Find: "func hello() {}", Replace: "func Hello() string { return \"hi\" }",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ae := resp.GetApplyEdit()
	if !ae.Success {
		t.Fatalf("apply edit failed: %s", ae.Error)
	}

	mainContent, _ := code.FileOps().ReadFile(ctx, "main.go")
	if !strings.Contains(string(mainContent), "Hello") {
		t.Error("read should reflect the edit through overlay")
	}

	diskData, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if strings.Contains(string(diskData), "Hello") {
		t.Error("disk should NOT have the edit before commit")
	}
}

func TestCodeServer_ViaOverlay_Search(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc existing() {}\n"), 0644)

	ov := NewOverlayVFS(LocalVFS{})
	code := NewGoCodeServer(dir, []ServerOption{WithVFS(ov)})

	ctx := t.Context()

	code.FileOps().WriteFile(ctx, "overlay.go", []byte("package main\n\nfunc fromOverlay() {}\n"))

	result, err := code.FileOps().Search(ctx, SearchOpts{Pattern: "fromOverlay", Literal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) == 0 {
		t.Error("search should find content in overlay-written files")
	}
}

func TestCodeServer_ViaOverlay_MoveFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "old.go"), []byte("package main\n"), 0644)

	ov := NewOverlayVFS(LocalVFS{})
	code := NewGoCodeServer(dir, []ServerOption{WithVFS(ov)})

	ctx := t.Context()

	if err := code.FileOps().MoveFile(ctx, "old.go", "new.go"); err != nil {
		t.Fatalf("move should succeed: %v", err)
	}

	_, err := code.FileOps().ReadFile(ctx, "new.go")
	if err != nil {
		t.Error("new path should exist after move")
	}

	_, err = code.FileOps().ReadFile(ctx, "old.go")
	if err == nil {
		t.Error("old path should not exist after move")
	}
}

// ============================================================
// Plugin simulation: process reads from disk after overlay commit
// ============================================================

func TestOverlayVFS_PluginSimulation_FlushAndRead(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("package main\n\nimport \"fmt\"\n"), 0644)

	base := LocalVFS{}
	ov := NewOverlayVFS(base)

	path := filepath.Join(dir, "code.go")
	ov.WriteFile(path, []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n"), 0644)

	diskData, _ := os.ReadFile(path)
	if strings.Contains(string(diskData), "os") {
		t.Error("disk should not have overlay changes yet")
	}

	ov.Commit()

	diskData, _ = os.ReadFile(path)
	if !strings.Contains(string(diskData), "os") {
		t.Error("disk should have overlay changes after commit")
	}
}

func TestOverlayVFS_PluginSimulation_TempFileWorkflow(t *testing.T) {
	dir := t.TempDir()
	original := "package main\n\nfunc hello(){\nfmt.Println(\"hi\")\n}\n"
	os.WriteFile(filepath.Join(dir, "code.go"), []byte(original), 0644)

	base := LocalVFS{}
	ov := NewOverlayVFS(base)

	diskData, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	fixedContent := strings.ReplaceAll(string(diskData), "hello", "Hello")

	ov.WriteFile(filepath.Join(dir, "code.go"), []byte(fixedContent), 0644)

	data, _ := ov.ReadFile(filepath.Join(dir, "code.go"))
	if !strings.Contains(string(data), "Hello") {
		t.Error("overlay should have fixed content")
	}

	diskData2, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	if strings.Contains(string(diskData2), "Hello") {
		t.Error("disk should not be changed until commit")
	}
}

func TestOverlayVFS_PluginSimulation_MultiplePluginRounds(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"v1\")\n}\n"), 0644)

	base := LocalVFS{}
	ov := NewOverlayVFS(base)
	mainPath := filepath.Join(dir, "main.go")

	// Round 1: plugin "fix" reads from VFS, returns modified content
	data, _ := ov.ReadFile(mainPath)
	round1 := strings.ReplaceAll(string(data), "v1", "v2")
	ov.WriteFile(mainPath, []byte(round1), 0644)

	// Round 2: second plugin round reads the previous round's output
	data, _ = ov.ReadFile(mainPath)
	if !strings.Contains(string(data), "v2") {
		t.Fatal("round 2 should see round 1's changes")
	}
	round2 := strings.ReplaceAll(string(data), "v2", "v3")
	ov.WriteFile(mainPath, []byte(round2), 0644)

	data, _ = ov.ReadFile(mainPath)
	if !strings.Contains(string(data), "v3") {
		t.Error("should see final version through overlay")
	}

	// Disk should be untouched
	diskData, _ := os.ReadFile(mainPath)
	if !strings.Contains(string(diskData), "v1") {
		t.Error("disk should still have original content")
	}
}

// ============================================================
// Edge cases
// ============================================================

func TestOverlayVFS_WriteReadDeleteWrite(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)

	ov.WriteFile("/f", []byte("v1"), 0644)
	ov.Remove("/f")
	if _, err := ov.ReadFile("/f"); !os.IsNotExist(err) {
		t.Error("should not exist after delete")
	}

	ov.WriteFile("/f", []byte("v2"), 0644)
	data, _ := ov.ReadFile("/f")
	if string(data) != "v2" {
		t.Errorf("should have v2 after re-write, got %q", data)
	}
}

func TestOverlayVFS_EmptyDiff(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/f": "content"})
	ov := NewOverlayVFS(base)

	diff := ov.Diff()
	if len(diff) != 0 {
		t.Error("fresh overlay should have empty diff")
	}
}

func TestOverlayVFS_OverwriteSameContent(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/f": "same"})
	ov := NewOverlayVFS(base)
	ov.WriteFile("/f", []byte("same"), 0644)

	// The overlay tracks the write even if content is identical
	if !ov.Dirty() {
		t.Error("overlay should be dirty even if content matches (write was recorded)")
	}
}

func TestOverlayVFS_RenameNonExistent(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)

	err := ov.Rename("/nonexistent", "/dest")
	if err == nil {
		t.Error("renaming non-existent file should error")
	}
}

func TestOverlayVFS_WriteToDeepPath_AutoCreateDirs(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)
	ov.WriteFile("/a/b/c/d/file.txt", []byte("deep"), 0644)

	data, err := ov.ReadFile("/a/b/c/d/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Error("should read deep nested file")
	}

	info, err := ov.Stat("/a/b/c")
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("parent dirs should be auto-created")
	}
}

func TestOverlayVFS_LargeFile(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{})
	ov := NewOverlayVFS(base)

	var b strings.Builder
	for i := 0; i < 10000; i++ {
		b.WriteString("// line " + strings.Repeat("x", 100) + "\n")
	}
	content := b.String()

	ov.WriteFile("/large.go", []byte(content), 0644)
	data, _ := ov.ReadFile("/large.go")
	if len(data) != len(content) {
		t.Errorf("large file round-trip: got %d bytes, want %d", len(data), len(content))
	}
}
