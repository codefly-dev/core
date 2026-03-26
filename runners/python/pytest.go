package python

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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

// RunPythonTests runs pytest and returns parsed results.
// Uses verbose text output with fallback parsing.
func RunPythonTests(ctx context.Context, sourceDir string, envVars []*resources.EnvironmentVariable) (*TestSummary, error) {
	cmd := exec.CommandContext(ctx, "uv", "run", "pytest", "-v", "--tb=short")
	cmd.Dir = sourceDir

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	// Inherit parent env, then overlay codefly env vars.
	cmd.Env = os.Environ()
	for _, ev := range envVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", ev.Key, ev.Value))
	}

	runErr := cmd.Run()
	summary := ParsePytestVerbose(out.String())
	return summary, runErr
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
