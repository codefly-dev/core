package golang

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildTestArgs replicates the arg construction inside RunGoTests so we
// can assert on the resulting `go test` invocation without needing a live
// runner environment.
//
// If this function ever drifts from the real RunGoTests logic, the unit
// tests here will start passing against stale args — keep them aligned.
func buildTestArgs(opt TestOptions) []string {
	args := []string{"test", "-json"}
	if opt.Verbose {
		args = append(args, "-v")
	}
	if opt.Race {
		args = append(args, "-race")
	}
	if opt.Timeout != "" {
		args = append(args, "-timeout", opt.Timeout)
	}
	if opt.Coverage {
		args = append(args, "-cover")
	}
	pkg := "./..."
	if opt.Target != "" {
		if isPackagePath(opt.Target) {
			pkg = opt.Target
		} else {
			args = append(args, "-run", opt.Target)
		}
	}
	return append(args, pkg)
}

func TestGoTestArgs_CoverageOptIn(t *testing.T) {
	cases := []struct {
		name     string
		opt      TestOptions
		wantCov  bool
		wantRace bool
	}{
		{"default: no cover", TestOptions{}, false, false},
		{"coverage only", TestOptions{Coverage: true}, true, false},
		{"race only", TestOptions{Race: true}, false, true},
		{"coverage + race", TestOptions{Coverage: true, Race: true}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := buildTestArgs(tc.opt)
			joined := strings.Join(args, " ")
			if got := strings.Contains(joined, " -cover"); got != tc.wantCov {
				t.Errorf("args=%q: -cover present=%v, want=%v", joined, got, tc.wantCov)
			}
			if got := strings.Contains(joined, " -race"); got != tc.wantRace {
				t.Errorf("args=%q: -race present=%v, want=%v", joined, got, tc.wantRace)
			}
		})
	}
}

func TestGoTestArgs_TargetAndVerbose(t *testing.T) {
	args := buildTestArgs(TestOptions{Target: "./handlers", Verbose: true})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, " -v") {
		t.Errorf("-v should be present: %q", joined)
	}
	if !strings.HasSuffix(joined, " ./handlers") {
		t.Errorf("target ./handlers should be the last arg: %q", joined)
	}
	if strings.Contains(joined, " -run") {
		t.Errorf("package path target should NOT trigger -run: %q", joined)
	}
}

func TestGoTestArgs_TargetPattern(t *testing.T) {
	args := buildTestArgs(TestOptions{Target: "TestFoo.*"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, " -run TestFoo.*") {
		t.Errorf("non-path target should map to -run: %q", joined)
	}
	if !strings.HasSuffix(joined, " ./...") {
		t.Errorf("pkg should default to ./... when target is a pattern: %q", joined)
	}
}

// TestStreamingTestWriter_EmitsEvents feeds realistic `go test -json`
// output through StreamingTestWriter and asserts the callback fires
// once per structured event in order. Non-JSON lines are buffered but
// NOT surfaced — some runners emit leading stderr (e.g. "=== RUN ..."
// from older toolchains) which we don't want as spurious events.
func TestStreamingTestWriter_EmitsEvents(t *testing.T) {
	input := []string{
		`{"Action":"run","Package":"pkg","Test":"TestA"}`,
		`{"Action":"output","Package":"pkg","Test":"TestA","Output":"--- PASS: TestA\n"}`,
		`{"Action":"pass","Package":"pkg","Test":"TestA","Elapsed":0.02}`,
		`garbage not-json line that should not crash us`,
		`{"Action":"fail","Package":"pkg","Test":"TestB","Elapsed":0.05}`,
	}

	var mu sync.Mutex
	var got []string
	w := &StreamingTestWriter{
		OnEvent: func(ev TestEvent) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, ev.Action+":"+ev.Test)
		},
	}
	for _, line := range input {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	wantEvents := []string{
		"run:TestA",
		"output:TestA",
		"pass:TestA",
		"fail:TestB",
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(wantEvents) {
		t.Fatalf("event count: got %d (%v), want %d", len(got), got, len(wantEvents))
	}
	for i, want := range wantEvents {
		if got[i] != want {
			t.Errorf("event[%d]: got %q, want %q", i, got[i], want)
		}
	}

	// The buffered output should still contain every line (including the
	// garbage one) so the end-of-run summary parse doesn't miss anything.
	captured := w.LineCapture.String()
	for _, line := range input {
		if !strings.Contains(captured, line) {
			t.Errorf("buffer missing line: %s", line)
		}
	}
}

// TestWriteLastTestOutput_PersistsRawJSON exercises the post-mortem
// dump path. The file ends up at <cacheDir>/last-test.json and contains
// the full raw stream — debug surface for failed runs.
func TestWriteLastTestOutput_PersistsRawJSON(t *testing.T) {
	dir := t.TempDir()
	raw := `{"Action":"pass","Test":"X"}` + "\n" + `{"Action":"fail","Test":"Y"}` + "\n"

	if err := writeLastTestOutput(dir, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "last-test.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != raw {
		t.Errorf("content mismatch:\n got: %q\nwant: %q", string(got), raw)
	}
	// Atomic write should leave no .tmp file behind.
	if _, err := os.Stat(filepath.Join(dir, "last-test.json.tmp")); err == nil {
		t.Error(".tmp file should be renamed away after write")
	}
}

// TestWriteLastTestOutput_OverwritesPreviousRun confirms second run
// replaces first — operators should always see the LATEST test, not
// have to dig through history files.
func TestWriteLastTestOutput_OverwritesPreviousRun(t *testing.T) {
	dir := t.TempDir()
	if err := writeLastTestOutput(dir, "first\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeLastTestOutput(dir, "second\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "last-test.json"))
	if string(got) != "second\n" {
		t.Errorf("got %q, want second", string(got))
	}
}

// TestWriteLastTestOutput_CreatesMissingDirectory removes the burden on
// callers to mkdir the cache location — this helper handles it so the
// debug dump never silently disappears because of a forgotten setup
// step.
func TestWriteLastTestOutput_CreatesMissingDirectory(t *testing.T) {
	parent := t.TempDir()
	deep := filepath.Join(parent, "a", "b", "c")
	if err := writeLastTestOutput(deep, "x\n"); err != nil {
		t.Fatalf("write into nested missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deep, "last-test.json")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

// TestStreamingTestWriter_NilCallbackIsSafe allows using the type for
// buffering only (equivalent to LineCapture) without requiring a sink.
func TestStreamingTestWriter_NilCallbackIsSafe(t *testing.T) {
	w := &StreamingTestWriter{OnEvent: nil}
	if _, err := w.Write([]byte(`{"Action":"pass","Test":"X"}`)); err != nil {
		t.Fatalf("Write with nil OnEvent: %v", err)
	}
	if w.LineCapture.String() == "" {
		t.Error("expected line to be buffered even without callback")
	}
}
