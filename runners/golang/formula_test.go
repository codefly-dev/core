package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// writeModule materializes a throwaway Go module for formula tests.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const clampSrc = `package stringsx

func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return v // BUG: must return hi
	}
	return v
}
`

const clampGreenTests = `package stringsx

import "testing"

func TestClampInRange(t *testing.T) {
	if got := Clamp(5, 0, 10); got != 5 {
		t.Fatalf("got %d", got)
	}
}

func TestClampLowerBound(t *testing.T) {
	if got := Clamp(-3, 0, 10); got != 0 {
		t.Fatalf("got %d", got)
	}
}
`

const clampRedTest = `package stringsx

import "testing"

func TestClampUpperBound(t *testing.T) {
	if got := Clamp(15, 0, 10); got != 10 {
		t.Fatalf("Clamp(15,0,10) = %d, want 10", got)
	}
}
`

func formulaCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestDeriveFormula(t *testing.T) {
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/x\n\ngo 1.21\n"})
	cmd, output, ok := DeriveFormula(dir)
	if !ok || output != OutputGoTestJSON {
		t.Fatalf("DeriveFormula = %v/%q/%v", cmd, output, ok)
	}
	if strings.Join(cmd, " ") != "go test -json ./..." {
		t.Fatalf("derived cmd = %v", cmd)
	}
	if _, _, ok := DeriveFormula(t.TempDir()); ok {
		t.Fatal("DeriveFormula claimed a dir without go.mod")
	}
}

func TestRunFormula_HealthySuitePasses(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests,
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, nil)
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_PASSED {
		t.Fatalf("state = %s (%s)", resp.GetResult().GetState(), resp.GetResult().GetMessage())
	}
	if resp.GetCounts().GetPassed() != 2 || resp.GetCounts().GetFailed() != 0 {
		t.Fatalf("counts = %+v", resp.GetCounts())
	}
}

func TestRunFormula_FailingTestIsFailedNotBlocked(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests + "\n" + strings.TrimPrefix(clampRedTest, "package stringsx\n\nimport \"testing\"\n"),
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, nil)
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_FAILED {
		t.Fatalf("state = %s (%s)", resp.GetResult().GetState(), resp.GetResult().GetMessage())
	}
	if resp.GetCounts().GetPassed() != 2 || resp.GetCounts().GetFailed() != 1 {
		t.Fatalf("counts = %+v", resp.GetCounts())
	}
	if strings.Contains(resp.GetResult().GetMessage(), "env-blocked") {
		t.Fatalf("failing tests must never read as env-blocked: %s", resp.GetResult().GetMessage())
	}
}

func TestRunFormula_SelectorTargetsSingleTest(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests + "\n" + strings.TrimPrefix(clampRedTest, "package stringsx\n\nimport \"testing\"\n"),
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, []string{"TestClampUpperBound"})
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetCounts().GetTotal() != 1 || resp.GetCounts().GetFailed() != 1 {
		t.Fatalf("selector run counts = %+v", resp.GetCounts())
	}
}

func TestRunFormula_BrokenGoModIsEnvBlocked(t *testing.T) {
	dir := writeModule(t, map[string]string{
		// `modle` instead of `module` — the canonical deterministic, offline
		// environment breakage (mirrors examples/go/02-broken-env in mind).
		"go.mod":           "modle example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests,
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, nil)
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_ERRORED {
		t.Fatalf("state = %s", resp.GetResult().GetState())
	}
	msg := resp.GetResult().GetMessage()
	if !strings.Contains(msg, "env-blocked ("+EnvErrorModuleBroken+")") {
		t.Fatalf("broken go.mod must carry the explicit env-blocked tag, got: %s", msg)
	}
	if !strings.Contains(msg, "unknown directive") {
		t.Fatalf("env-blocked detail should carry the go.mod parse error, got: %s", msg)
	}
	if resp.GetCounts().GetTotal() != 0 {
		t.Fatalf("counts = %+v, want zero cases", resp.GetCounts())
	}
}

func TestRunFormula_CompileErrorIsNotEnvBlocked(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      "package stringsx\n\nfunc Broken() int { return }\n", // compile error in code under test
		"stringsx_test.go": clampGreenTests,
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, nil)
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_ERRORED {
		t.Fatalf("state = %s", resp.GetResult().GetState())
	}
	if strings.Contains(resp.GetResult().GetMessage(), "env-blocked") {
		t.Fatalf("a compile error in the code under test is the CODE's fault, not the environment's: %s", resp.GetResult().GetMessage())
	}
}
