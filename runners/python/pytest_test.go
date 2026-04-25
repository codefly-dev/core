package python

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestScanPytestEvents_EmitsPerLine feeds realistic pytest verbose
// output through scanPytestEvents and asserts the callback fires once
// per progress line, in order.
func TestScanPytestEvents_EmitsPerLine(t *testing.T) {
	input := strings.NewReader(`============ test session starts ============
collected 4 items

tests/test_a.py::test_one PASSED                              [ 25%]
tests/test_a.py::test_two FAILED                              [ 50%]
tests/test_b.py::test_three SKIPPED (skipped reason)          [ 75%]
tests/test_b.py::test_four PASSED                             [100%]

============ short test summary ============
`)

	var mu sync.Mutex
	var got []TestEvent
	scanPytestEvents(input, func(ev TestEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev)
	})

	mu.Lock()
	defer mu.Unlock()
	want := []TestEvent{
		{Action: "pass", Test: "tests/test_a.py::test_one"},
		{Action: "fail", Test: "tests/test_a.py::test_two"},
		{Action: "skip", Test: "tests/test_b.py::test_three"},
		{Action: "pass", Test: "tests/test_b.py::test_four"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("event[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestPytestNode_StripsTrailingProgressMarker confirms the parser
// trims the percentage marker pytest appends, so callbacks see clean
// node ids that match the on-disk paths.
func TestPytestNode_StripsTrailingProgressMarker(t *testing.T) {
	cases := []struct {
		line, marker, want string
	}{
		{"tests/test_x.py::test_y PASSED                              [ 25%]", " PASSED", "tests/test_x.py::test_y"},
		{"  prefix-spaces tests/x.py::z FAILED [50%]", " FAILED", "prefix-spaces tests/x.py::z"},
		{"unrelated", " PASSED", ""},
	}
	for _, c := range cases {
		if got := pytestNode(c.line, c.marker); got != c.want {
			t.Errorf("pytestNode(%q, %q) = %q, want %q", c.line, c.marker, got, c.want)
		}
	}
}

// TestWriteLastTestOutput_PersistsRaw mirrors the go-side helper: a
// single dump to <cacheDir>/last-test.txt, atomic write, no .tmp
// straggler.
func TestWriteLastTestOutput_PersistsRaw(t *testing.T) {
	dir := t.TempDir()
	raw := "============= test session starts =============\nFAILED tests/test_x.py::test_a\n"

	if err := writeLastTestOutput(dir, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "last-test.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != raw {
		t.Errorf("content mismatch")
	}
	if _, err := os.Stat(filepath.Join(dir, "last-test.txt.tmp")); err == nil {
		t.Error(".tmp should be renamed away after write")
	}
}

// TestWriteLastTestOutput_OverwritesPreviousRun ensures operators see
// the LATEST run after a re-run, not stale history from an older one.
func TestWriteLastTestOutput_OverwritesPreviousRun(t *testing.T) {
	dir := t.TempDir()
	if err := writeLastTestOutput(dir, "run-one\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeLastTestOutput(dir, "run-two\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "last-test.txt"))
	if string(got) != "run-two\n" {
		t.Errorf("got %q, want run-two", string(got))
	}
}

// TestWriteLastTestOutput_CreatesMissingDir confirms callers don't
// have to mkdir first — common when CacheDir lives under a fresh
// service tree.
func TestWriteLastTestOutput_CreatesMissingDir(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := writeLastTestOutput(deep, "x\n"); err != nil {
		t.Fatalf("write into nested missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deep, "last-test.txt")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

// TestCombinePytestK confirms multi-filter expansion uses pytest's
// boolean-expression idiom (" or "), not a regex pipe — pytest's -k
// parses the value as a Python expression, not a regex.
func TestCombinePytestK(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"test_login"}, "test_login"},
		{[]string{"test_login", "test_logout"}, "test_login or test_logout"},
		{[]string{"a", "b", "c"}, "a or b or c"},
	}
	for _, tc := range cases {
		if got := combinePytestK(tc.in); got != tc.want {
			t.Errorf("combinePytestK(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
