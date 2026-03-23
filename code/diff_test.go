package code

import (
	"testing"
)

const sampleDiff = `diff --git a/main.go b/main.go
index abc1234..def5678 100644
--- a/main.go
+++ b/main.go
@@ -10,6 +10,8 @@ func main() {
 	fmt.Println("hello")
 	fmt.Println("world")
 
+	fmt.Println("new line 1")
+	fmt.Println("new line 2")
 	fmt.Println("end")
 }
diff --git a/handler.go b/handler.go
index 111..222 100644
--- a/handler.go
+++ b/handler.go
@@ -5,7 +5,7 @@ func handle() {
 	old := true
-	doOld()
+	doNew()
 	end := false
 }
`

func TestParseUnifiedDiff_FileCount(t *testing.T) {
	diffs := ParseUnifiedDiff(sampleDiff)
	if len(diffs) != 2 {
		t.Fatalf("expected 2 file diffs, got %d", len(diffs))
	}
}

func TestParseUnifiedDiff_FilePaths(t *testing.T) {
	diffs := ParseUnifiedDiff(sampleDiff)
	if diffs[0].OldPath != "main.go" || diffs[0].NewPath != "main.go" {
		t.Errorf("unexpected paths: old=%s new=%s", diffs[0].OldPath, diffs[0].NewPath)
	}
	if diffs[1].NewPath != "handler.go" {
		t.Errorf("expected handler.go, got %s", diffs[1].NewPath)
	}
}

func TestParseUnifiedDiff_HunkLines(t *testing.T) {
	diffs := ParseUnifiedDiff(sampleDiff)
	if len(diffs[0].Hunks) != 1 {
		t.Fatalf("expected 1 hunk in main.go, got %d", len(diffs[0].Hunks))
	}
	hunk := diffs[0].Hunks[0]
	if hunk.OldStart != 10 {
		t.Errorf("expected OldStart=10, got %d", hunk.OldStart)
	}

	addCount := 0
	for _, l := range hunk.Lines {
		if l.Kind == DiffAdd {
			addCount++
			if l.NewLine == 0 {
				t.Error("added line should have a NewLine number")
			}
		}
	}
	if addCount != 2 {
		t.Errorf("expected 2 added lines, got %d", addCount)
	}
}

func TestParseUnifiedDiff_RemoveAndAdd(t *testing.T) {
	diffs := ParseUnifiedDiff(sampleDiff)
	hunk := diffs[1].Hunks[0]
	var added, removed int
	for _, l := range hunk.Lines {
		switch l.Kind {
		case DiffAdd:
			added++
		case DiffRemove:
			removed++
		}
	}
	if added != 1 || removed != 1 {
		t.Errorf("expected 1 add + 1 remove, got %d add + %d remove", added, removed)
	}
}

func TestFileDiff_Summary(t *testing.T) {
	diffs := ParseUnifiedDiff(sampleDiff)
	s := diffs[0].Summary()
	if s != "main.go: +2/-0 (1 hunks)" {
		t.Errorf("unexpected summary: %s", s)
	}
}

func TestParseUnifiedDiff_Empty(t *testing.T) {
	diffs := ParseUnifiedDiff("")
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs from empty input, got %d", len(diffs))
	}
}

func TestDiffKind_String(t *testing.T) {
	if DiffAdd.String() != "+" {
		t.Error("expected +")
	}
	if DiffRemove.String() != "-" {
		t.Error("expected -")
	}
	if DiffContext.String() != " " {
		t.Error("expected space")
	}
}
