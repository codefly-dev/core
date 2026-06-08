package rust

import (
	"fmt"
	"regexp"
	"strings"
)

// TestEvent is a single parsed `cargo test` result line, surfaced through
// TestOptions.OnEvent for streaming. It is the Rust analog of golang.TestEvent
// — cargo emits human-readable libtest text (not JSON on stable), so events
// are reconstructed from that text.
type TestEvent struct {
	// Suite is the current test binary descriptor (e.g. "unittests src/lib.rs"
	// or "tests/integration.rs").
	Suite string
	// Test is the test name (e.g. "tests::it_works").
	Test string
	// Action is one of: "pass", "fail", "skip".
	Action string
	// Output is any captured failure output (populated post-hoc; empty for
	// streamed events).
	Output string
}

// TestSummary holds the parsed results of a `cargo test` run. Mirrors
// golang.TestSummary. Coverage is always 0 (cargo has no built-in coverage).
type TestSummary struct {
	Run      int32
	Passed   int32
	Failed   int32
	Skipped  int32
	Coverage float32
	Failures []string
}

var (
	// `test tests::it_works ... ok`  /  `... FAILED`  /  `... ignored, reason`
	testLineRe = regexp.MustCompile(`^test (.+?) \.\.\. (ok|FAILED|ignored)`)
	// `     Running unittests src/lib.rs (target/debug/deps/foo-1a2b3c)`
	runningRe = regexp.MustCompile(`^\s*Running (.+?) \(`)
	// `   Doc-tests foo`
	docTestsRe = regexp.MustCompile(`^\s*Doc-tests (.+)$`)
	// failure detail header: `---- tests::it_fails stdout ----`
	failHeaderRe = regexp.MustCompile(`^---- (.+?) stdout ----$`)
)

// ParseCargoTest parses the accumulated text output of `cargo test`. It
// recognizes the libtest per-test result lines and the per-suite "Running …"
// markers, and collects failure detail blocks. Mirrors golang.ParseTestJSON.
func ParseCargoTest(raw string) *TestSummary {
	s := &TestSummary{}
	suite := ""
	failBuf := map[string]*strings.Builder{}
	captureKey := ""
	// cargo prints the `test … FAILED` summary line BEFORE the `---- … stdout
	// ----` detail block, so failure strings are assembled after the full walk.
	var failedKeys []string

	for _, line := range strings.Split(raw, "\n") {
		// Suite boundaries.
		if m := runningRe.FindStringSubmatch(line); m != nil {
			suite = strings.TrimSpace(m[1])
			captureKey = ""
			continue
		}
		if m := docTestsRe.FindStringSubmatch(line); m != nil {
			suite = "doc-tests " + strings.TrimSpace(m[1])
			captureKey = ""
			continue
		}

		// Failure detail capture: `---- <name> stdout ----` opens a block that
		// runs until the next blank line / marker.
		if m := failHeaderRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			captureKey = suite + "::" + m[1]
			failBuf[captureKey] = &strings.Builder{}
			continue
		}
		if captureKey != "" {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "----") || strings.HasPrefix(trimmed, "failures:") || strings.HasPrefix(trimmed, "test result:") {
				captureKey = ""
			} else {
				failBuf[captureKey].WriteString(line)
				failBuf[captureKey].WriteString("\n")
			}
		}

		// Per-test result lines.
		m := testLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		name, result := m[1], m[2]
		s.Run++
		switch result {
		case "ok":
			s.Passed++
		case "ignored":
			s.Skipped++
		case "FAILED":
			s.Failed++
			failedKeys = append(failedKeys, suite+"::"+name)
		}
	}

	// Assemble failure strings now that detail blocks have been collected.
	for _, key := range failedKeys {
		if b, ok := failBuf[key]; ok && b.Len() > 0 {
			s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s\n%s", key, b.String()))
		} else {
			s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s", key))
		}
	}
	return s
}

// SummaryLine formats a one-line summary string. Mirrors golang.TestSummary.
func (s *TestSummary) SummaryLine() string {
	parts := []string{fmt.Sprintf("%d passed", s.Passed)}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}
	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", s.Skipped))
	}
	if s.Coverage > 0 {
		parts = append(parts, fmt.Sprintf("%.1f%% coverage", s.Coverage))
	}
	return strings.Join(parts, ", ")
}

// LineCapture implements io.Writer and accumulates all written data with
// newlines preserved. Identical to golang.LineCapture.
type LineCapture struct {
	buf strings.Builder
}

func (lc *LineCapture) Write(p []byte) (n int, err error) {
	lc.buf.Write(p)
	lc.buf.WriteByte('\n')
	return len(p), nil
}

func (lc *LineCapture) String() string {
	return lc.buf.String()
}

// StreamingTestWriter is a LineCapture that ALSO invokes a callback for every
// recognized `cargo test` result line as it arrives. Mirrors
// golang.StreamingTestWriter. It tracks the current suite from "Running …"
// markers so streamed events carry the suite name.
type StreamingTestWriter struct {
	LineCapture
	OnEvent func(TestEvent)

	suite string
}

func (w *StreamingTestWriter) Write(p []byte) (int, error) {
	n, err := w.LineCapture.Write(p)
	if err != nil {
		return n, err
	}
	if w.OnEvent == nil {
		return n, nil
	}
	line := string(p)
	if m := runningRe.FindStringSubmatch(line); m != nil {
		w.suite = strings.TrimSpace(m[1])
		return n, nil
	}
	if m := docTestsRe.FindStringSubmatch(line); m != nil {
		w.suite = "doc-tests " + strings.TrimSpace(m[1])
		return n, nil
	}
	if m := testLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
		ev := TestEvent{Suite: w.suite, Test: m[1]}
		switch m[2] {
		case "ok":
			ev.Action = "pass"
		case "FAILED":
			ev.Action = "fail"
		case "ignored":
			ev.Action = "skip"
		}
		w.OnEvent(ev)
	}
	return n, nil
}
