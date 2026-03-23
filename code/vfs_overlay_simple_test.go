package code

import (
	"sync"
	"testing"
)

func mustRead(t *testing.T, v VFS, path string) string {
	t.Helper()
	data, err := v.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
}

func mustWrite(t *testing.T, v VFS, path, content string) {
	t.Helper()
	if err := v.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func TestTwoOverlays_HelloWorld(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/hello.txt": "hello"})
	overlayA := NewOverlayVFS(base)
	overlayB := NewOverlayVFS(base)

	mustWrite(t, overlayB, "/hello.txt", "world")

	if got := mustRead(t, overlayA, "/hello.txt"); got != "hello" {
		t.Fatalf("overlay A should still see %q, got %q", "hello", got)
	}
	if got := mustRead(t, overlayB, "/hello.txt"); got != "world" {
		t.Fatalf("overlay B should see %q, got %q", "world", got)
	}
	if got := mustRead(t, base, "/hello.txt"); got != "hello" {
		t.Fatalf("base should be untouched (%q), got %q", "hello", got)
	}

	if err := overlayB.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, base, "/hello.txt"); got != "world" {
		t.Fatalf("after commit base should be %q, got %q", "world", got)
	}

	if got := mustRead(t, overlayA, "/hello.txt"); got != "world" {
		t.Fatalf("overlay A should now fall through to committed base %q, got %q", "world", got)
	}
}

func TestTwoOverlays_ConcurrentReadWrite(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/data.txt": "original"})

	const N = 20
	overlays := make([]*OverlayVFS, N)
	for i := range overlays {
		overlays[i] = NewOverlayVFS(base)
	}

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			ov := overlays[idx]
			data, err := ov.ReadFile("/data.txt")
			if err != nil {
				t.Errorf("overlay %d read failed: %v", idx, err)
				return
			}
			if string(data) != "original" {
				t.Errorf("overlay %d expected %q, got %q", idx, "original", string(data))
				return
			}
			newContent := []byte("written-by-" + string(rune('A'+idx)))
			if err := ov.WriteFile("/data.txt", newContent, 0644); err != nil {
				t.Errorf("overlay %d write failed: %v", idx, err)
				return
			}
			got, err := ov.ReadFile("/data.txt")
			if err != nil {
				t.Errorf("overlay %d re-read failed: %v", idx, err)
				return
			}
			if string(got) != string(newContent) {
				t.Errorf("overlay %d expected own write %q, got %q", idx, string(newContent), string(got))
			}
		}(i)
	}
	wg.Wait()

	if got := mustRead(t, base, "/data.txt"); got != "original" {
		t.Fatalf("base should still be %q after concurrent overlay writes, got %q", "original", got)
	}
}

func TestTwoOverlays_IsolatedMutations(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{
		"/a.txt": "aaa",
		"/b.txt": "bbb",
	})
	ov1 := NewOverlayVFS(base)
	ov2 := NewOverlayVFS(base)

	mustWrite(t, ov1, "/a.txt", "AAA")
	mustWrite(t, ov2, "/b.txt", "BBB")

	if got := mustRead(t, ov1, "/a.txt"); got != "AAA" {
		t.Fatalf("ov1/a.txt: expected AAA, got %s", got)
	}
	if got := mustRead(t, ov1, "/b.txt"); got != "bbb" {
		t.Fatalf("ov1/b.txt: expected bbb, got %s", got)
	}

	if got := mustRead(t, ov2, "/a.txt"); got != "aaa" {
		t.Fatalf("ov2/a.txt: expected aaa, got %s", got)
	}
	if got := mustRead(t, ov2, "/b.txt"); got != "BBB" {
		t.Fatalf("ov2/b.txt: expected BBB, got %s", got)
	}

	if err := ov1.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := mustRead(t, ov2, "/a.txt"); got != "AAA" {
		t.Fatalf("after ov1 commit, ov2/a.txt should fall through to AAA, got %s", got)
	}
	if got := mustRead(t, ov2, "/b.txt"); got != "BBB" {
		t.Fatalf("ov2/b.txt should still be its own BBB, got %s", got)
	}
}
