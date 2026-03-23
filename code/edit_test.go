package code

import (
	"strings"
	"testing"
)

func TestSmartEdit_Exact(t *testing.T) {
	r := SmartEdit("line1\nline2\nline3\n", "line2", "replaced")
	if !r.OK || r.Strategy != "exact" {
		t.Fatalf("expected exact match, got ok=%v strategy=%s", r.OK, r.Strategy)
	}
	if !strings.Contains(r.Content, "replaced") {
		t.Errorf("expected replaced in content")
	}
}

func TestSmartEdit_Trailing(t *testing.T) {
	r := SmartEdit("func foo() {  \n\treturn\n}\n", "func foo() {\n\treturn\n}", "func foo() {\n\treturn 42\n}")
	if !r.OK || r.Strategy != "trailing" {
		t.Fatalf("expected trailing match, got ok=%v strategy=%s", r.OK, r.Strategy)
	}
	if !strings.Contains(r.Content, "return 42") {
		t.Errorf("expected return 42")
	}
}

func TestSmartEdit_Trimmed(t *testing.T) {
	r := SmartEdit("func bar() {\n    return\n}\n", "func bar() {\n\treturn\n}", "func bar() {\n\treturn 99\n}")
	if !r.OK {
		t.Fatal("expected trimmed match")
	}
	if r.Strategy != "trimmed" {
		t.Logf("matched via %s (acceptable)", r.Strategy)
	}
}

func TestSmartEdit_IndentShifted(t *testing.T) {
	content := "func outer() {\n\tif true {\n\t\tfmt.Println(\"hello\")\n\t\tfmt.Println(\"world\")\n\t}\n}\n"
	find := "if true {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n}"
	replace := "if true {\n\tfmt.Println(\"REPLACED\")\n}"
	r := SmartEdit(content, find, replace)
	if !r.OK {
		t.Fatal("expected indent-shifted match")
	}
	if !strings.Contains(r.Content, "REPLACED") {
		t.Errorf("expected REPLACED")
	}
}

func TestSmartEdit_Anchor(t *testing.T) {
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := 1\n\ty := 2\n\tz := x + y\n\tfmt.Println(z)\n}\n"
	find := "func main() {\n\tx := 1\n\ty := 3\n\tz := x + y\n\tfmt.Println(z)\n}"
	replace := "func main() {\n\tx := 10\n\ty := 20\n\tz := x + y\n\tfmt.Println(z)\n}"
	r := SmartEdit(content, find, replace)
	if !r.OK {
		t.Fatal("expected anchor/fuzzy match")
	}
	if !strings.Contains(r.Content, "x := 10") {
		t.Errorf("expected x := 10")
	}
}

func TestSmartEdit_FuzzyScore(t *testing.T) {
	content := "func calculate(a, b int) int {\n\tresult := a + b\n\treturn result\n}\n"
	find := "func calculate(a, b int) int {\n\treslt := a + b\n\treturn reslt\n}"
	replace := "func calculate(a, b int) int {\n\tresult := a * b\n\treturn result\n}"
	r := SmartEdit(content, find, replace)
	if !r.OK {
		t.Fatal("expected fuzzy score match")
	}
	if !strings.Contains(r.Content, "a * b") {
		t.Errorf("expected a * b")
	}
}

func TestSmartEdit_NoMatch(t *testing.T) {
	r := SmartEdit("func foo() {}\n", "something completely different\nthat has no overlap\nwith the file", "replaced")
	if r.OK {
		t.Fatal("expected no match")
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
		{"result", "reslt", 1},
	}
	for _, tt := range tests {
		got := Levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("Levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDedentLines(t *testing.T) {
	got := DedentLines([]string{"\t\tline1", "\t\tline2", "\t\t\tindented"})
	if got[0] != "line1" {
		t.Errorf("got %q, want 'line1'", got[0])
	}
	if got[2] != "\tindented" {
		t.Errorf("got %q, want '\\tindented'", got[2])
	}
}
