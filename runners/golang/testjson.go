package golang

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TestEvent represents one line of `go test -json` output.
type TestEvent struct {
	Action     string  `json:"Action"`
	Package    string  `json:"Package"`
	ImportPath string  `json:"ImportPath"`
	Test       string  `json:"Test"`
	Output     string  `json:"Output"`
	Elapsed    float64 `json:"Elapsed"`
}

// TestSummary holds the parsed results of a `go test -json` run.
type TestSummary struct {
	Run      int32
	Passed   int32
	Failed   int32
	Skipped  int32
	Coverage float32
	Failures []string

	failOutput       map[string]*strings.Builder
	packageOutput    map[string]*strings.Builder
	packageTestFails map[string]bool
	packageFailures  map[string]bool
}

var coverageRe = regexp.MustCompile(`coverage:\s+([\d.]+)%`)

// ParseTestJSON parses the accumulated output of `go test -json -cover`.
func ParseTestJSON(raw string) *TestSummary {
	s := &TestSummary{
		failOutput:       make(map[string]*strings.Builder),
		packageOutput:    make(map[string]*strings.Builder),
		packageTestFails: make(map[string]bool),
		packageFailures:  make(map[string]bool),
	}
	recordPackageFailure := func(pkg string) {
		if pkg == "" || s.packageTestFails[pkg] || s.packageFailures[pkg] {
			return
		}
		s.Run++
		s.Failed++
		s.packageFailures[pkg] = true
		if buf, ok := s.packageOutput[pkg]; ok && buf.Len() > 0 {
			s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s\n%s", pkg, buf.String()))
		} else {
			s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s", pkg))
		}
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev TestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		pkg := ev.Package
		if pkg == "" {
			pkg = ev.ImportPath
		}

		switch ev.Action {
		case "pass":
			if ev.Test != "" {
				s.Run++
				s.Passed++
			}
		case "fail":
			if ev.Test != "" {
				s.Run++
				s.Failed++
				key := pkg + "/" + ev.Test
				s.packageTestFails[pkg] = true
				if buf, ok := s.failOutput[key]; ok {
					s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s\n%s", key, buf.String()))
				} else {
					s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s", key))
				}
			} else {
				recordPackageFailure(pkg)
			}
		case "skip":
			if ev.Test != "" {
				s.Run++
				s.Skipped++
			}
		case "build-output":
			if pkg != "" {
				if _, ok := s.packageOutput[pkg]; !ok {
					s.packageOutput[pkg] = &strings.Builder{}
				}
				s.packageOutput[pkg].WriteString(ev.Output)
			}
		case "build-fail":
			recordPackageFailure(pkg)
		case "output":
			if m := coverageRe.FindStringSubmatch(ev.Output); len(m) > 1 {
				var pct float64
				fmt.Sscanf(m[1], "%f", &pct)
				if float32(pct) > s.Coverage {
					s.Coverage = float32(pct)
				}
			}
			if ev.Test != "" {
				key := pkg + "/" + ev.Test
				if _, ok := s.failOutput[key]; !ok {
					s.failOutput[key] = &strings.Builder{}
				}
				s.failOutput[key].WriteString(ev.Output)
			} else if pkg != "" {
				if _, ok := s.packageOutput[pkg]; !ok {
					s.packageOutput[pkg] = &strings.Builder{}
				}
				s.packageOutput[pkg].WriteString(ev.Output)
			}
		}
	}

	return s
}

// SummaryLine formats a one-line summary string.
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

// LineCapture implements io.Writer and accumulates all written data
// with newlines preserved (the native runner strips trailing whitespace).
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

// StreamingTestWriter is a LineCapture that ALSO invokes a callback for
// every `go test -json` event as it arrives. Used when callers want
// real-time per-test progress rather than waiting for the full
// TestSummary at the end — typical case is forwarding events to a TUI
// via the agent's log channel.
//
// Non-JSON lines are buffered but not surfaced through the callback, so
// the sink sees only structured events.
type StreamingTestWriter struct {
	LineCapture
	OnEvent func(TestEvent)
}

// Write parses each line as a TestEvent and invokes OnEvent on success.
// Malformed lines are still buffered (via the embedded LineCapture) so
// ParseTestJSON can do its own defensive pass at the end — but they
// don't produce spurious events.
func (w *StreamingTestWriter) Write(p []byte) (int, error) {
	n, err := w.LineCapture.Write(p)
	if err != nil {
		return n, err
	}
	if w.OnEvent == nil {
		return n, nil
	}
	line := strings.TrimSpace(string(p))
	if line == "" {
		return n, nil
	}
	var ev TestEvent
	if jerr := json.Unmarshal([]byte(line), &ev); jerr == nil && ev.Action != "" {
		w.OnEvent(ev)
	}
	return n, nil
}
