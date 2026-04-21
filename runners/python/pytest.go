package python

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/resources"
)

// TestSummary holds the parsed results of a pytest run.
type TestSummary struct {
	Run      int32
	Passed   int32
	Failed   int32
	Skipped  int32
	Coverage float32
	Failures []string
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

// ParsePytestVerbose parses the verbose text output from pytest -v --tb=short.
func ParsePytestVerbose(output string) *TestSummary {
	s := &TestSummary{}

	var currentFailure strings.Builder
	inFailure := false

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, " PASSED") {
			s.Run++
			s.Passed++
			if inFailure {
				s.Failures = append(s.Failures, currentFailure.String())
				currentFailure.Reset()
				inFailure = false
			}
		} else if strings.Contains(trimmed, " FAILED") {
			s.Run++
			s.Failed++
			if inFailure {
				s.Failures = append(s.Failures, currentFailure.String())
				currentFailure.Reset()
			}
			inFailure = true
			currentFailure.WriteString(fmt.Sprintf("FAIL %s\n", trimmed))
		} else if strings.Contains(trimmed, " SKIPPED") {
			s.Run++
			s.Skipped++
		} else if inFailure {
			currentFailure.WriteString(line + "\n")
		}
	}

	if inFailure {
		s.Failures = append(s.Failures, currentFailure.String())
	}

	return s
}

// TestEvent is a single per-test signal extracted from pytest's verbose
// output. Mirrors the go-side TestEvent shape so consumers can drive a
// uniform UI across both languages.
type TestEvent struct {
	Action string // "pass" | "fail" | "skip"
	Test   string // test node id (e.g. "tests/test_admin.py::test_version")
}

// TestOptions controls pytest invocation. Currently just a streaming
// hook — we may add Coverage / Verbose flags later mirroring the Go
// runner's TestOptions.
type TestOptions struct {
	// OnEvent, when non-nil, is called for each per-test result line as
	// pytest emits it. Lets the agent forward live progress to the TUI
	// without waiting for the full summary at the end.
	OnEvent func(TestEvent)

	// CacheDir, when non-empty, persists the raw pytest output to
	// <CacheDir>/last-test.txt for post-mortem debugging.
	CacheDir string
}

// RunPythonTests runs pytest and returns parsed results. With opts.OnEvent
// the per-test stream is also pushed to the callback in real time. With
// opts.CacheDir the full raw output is dumped to <CacheDir>/last-test.txt
// for post-mortem inspection.
func RunPythonTests(ctx context.Context, sourceDir string, envVars []*resources.EnvironmentVariable, opts ...TestOptions) (*TestSummary, error) {
	var opt TestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	cmd := exec.CommandContext(ctx, "uv", "run", "pytest", "-v", "--tb=short")
	cmd.Dir = sourceDir

	var raw bytes.Buffer
	// Tee stdout: capture to buffer + line-scan for events when a sink is set.
	if opt.OnEvent != nil {
		pr, pw := io.Pipe()
		cmd.Stdout = io.MultiWriter(&raw, pw)
		cmd.Stderr = io.MultiWriter(&raw, pw)
		go scanPytestEvents(pr, opt.OnEvent)
		defer pw.Close()
	} else {
		cmd.Stdout = &raw
		cmd.Stderr = &raw
	}

	// Inherit parent env, then overlay codefly env vars.
	cmd.Env = os.Environ()
	for _, ev := range envVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", ev.Key, ev.Value))
	}

	runErr := cmd.Run()
	rawStr := raw.String()
	summary := ParsePytestVerbose(rawStr)

	if opt.CacheDir != "" {
		if err := writeLastTestOutput(opt.CacheDir, rawStr); err != nil {
			// Best-effort persistence — never mask the real result.
			_ = err
		}
	}

	return summary, runErr
}

// scanPytestEvents reads pytest's verbose output line by line and emits
// a TestEvent for each PASSED / FAILED / SKIPPED line. Pytest prints
// these as `tests/admin/test_admin.py::test_version PASSED [100%]`.
//
// Best-effort parser — non-matching lines are silently skipped so the
// callback only sees structured signal.
func scanPytestEvents(r io.Reader, onEvent func(TestEvent)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate long traceback lines
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, " PASSED"):
			onEvent(TestEvent{Action: "pass", Test: pytestNode(line, " PASSED")})
		case strings.Contains(line, " FAILED"):
			onEvent(TestEvent{Action: "fail", Test: pytestNode(line, " FAILED")})
		case strings.Contains(line, " SKIPPED"):
			onEvent(TestEvent{Action: "skip", Test: pytestNode(line, " SKIPPED")})
		}
	}
}

// pytestNode extracts the test node id (everything before the marker)
// from a pytest progress line.
func pytestNode(line, marker string) string {
	idx := strings.Index(line, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[:idx])
}

// writeLastTestOutput dumps the raw pytest output to
// <cacheDir>/last-test.txt. Atomic via tmp + rename.
func writeLastTestOutput(cacheDir, raw string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(cacheDir, "last-test.txt")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RunPythonLint runs ruff check and returns the output.
func RunPythonLint(ctx context.Context, sourceDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "uv", "run", "ruff", "check", ".")
	cmd.Dir = sourceDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
