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

// writeModule materializes a throwaway Go module for formula tests. File
// names may carry subdirectories ("good/good_test.go").
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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

const subTests = `package stringsx

import "testing"

func TestWithSub(t *testing.T) {
	t.Run("upper", func(t *testing.T) {})
	t.Run("lower", func(t *testing.T) {})
}
`

func TestRunFormula_SubtestSelectorIsRunPatternNotPackage(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests + "\n" + strings.TrimPrefix(subTests, "package stringsx\n\nimport \"testing\"\n"),
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, []string{"TestWithSub/upper"})
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_PASSED {
		t.Fatalf("subtest selector must run the subtest, got state = %s (%s)", resp.GetResult().GetState(), resp.GetResult().GetMessage())
	}
	// Exactly the parent + the selected subtest — not "lower", not the
	// clamp tests, and NEVER the whole module via a bogus package arg.
	if resp.GetCounts().GetTotal() != 2 || resp.GetCounts().GetPassed() != 2 {
		t.Fatalf("counts = %+v, want parent+subtest only", resp.GetCounts())
	}
}

func TestRunFormula_SelectorIsLiteralNotPrefix(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx_test.go": `package stringsx

import "testing"

func TestFoo(t *testing.T)    {}
func TestFooBar(t *testing.T) {}
`,
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, []string{"TestFoo"})
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetCounts().GetTotal() != 1 {
		t.Fatalf("selector \"TestFoo\" must not also select TestFooBar: counts = %+v", resp.GetCounts())
	}
}

func TestRunFormula_PackageSelectorNarrowsScope(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/multi\n\ngo 1.21\n",
		"good/good_test.go": `package good

import "testing"

func TestGood(t *testing.T) {}
`,
		"bad/bad_test.go": `package bad

import "testing"

func TestBad(t *testing.T) { t.Fatal("red") }
`,
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, []string{"./good"})
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_PASSED {
		t.Fatalf("./good selector must exclude the failing ./bad package: state = %s (%s)", resp.GetResult().GetState(), resp.GetResult().GetMessage())
	}
	if resp.GetCounts().GetTotal() != 1 || resp.GetCounts().GetPassed() != 1 {
		t.Fatalf("counts = %+v, want only ./good's test", resp.GetCounts())
	}
}

func TestRunFormula_NoMatchSelectorIsNeverGreen(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests,
	})
	resp, err := RunFormula(formulaCtx(t), dir, nil, []string{"TestDoesNotExistAnywhere"})
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_ERRORED {
		t.Fatalf("a selector matching nothing must not read as a pass: state = %s (%s)", resp.GetResult().GetState(), resp.GetResult().GetMessage())
	}
	if !strings.Contains(resp.GetResult().GetMessage(), "env-blocked ("+EnvErrorNoTestsMatchedSelectors+")") {
		t.Fatalf("want %s classification, got: %s", EnvErrorNoTestsMatchedSelectors, resp.GetResult().GetMessage())
	}
}

func TestRunFormula_MissingJSONFlagIsInjected(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/stringsx\n\ngo 1.21\n",
		"stringsx.go":      clampSrc,
		"stringsx_test.go": clampGreenTests,
	})
	// The proto's own TestFormula doc example — no -json.
	resp, err := RunFormula(formulaCtx(t), dir, []string{"go", "test", "./..."}, nil)
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetResult().GetState() != runtimev0.TestRunResult_PASSED || resp.GetCounts().GetPassed() != 2 {
		t.Fatalf("a formula without -json must still be parsed: state = %s counts = %+v (%s)",
			resp.GetResult().GetState(), resp.GetCounts(), resp.GetResult().GetMessage())
	}
}

func TestRunFormula_InterruptedRunIsNotPassed(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/hang\n\ngo 1.21\n",
		"hang_test.go": `package hang

import (
	"testing"
	"time"
)

func TestFast(t *testing.T) {}

func TestHang(t *testing.T) { time.Sleep(60 * time.Second) }
`,
	})
	// Pre-warm the build cache so the short-deadline run below spends its
	// budget running tests, not compiling.
	if _, err := RunFormula(formulaCtx(t), dir, nil, []string{"TestFast"}); err != nil {
		t.Fatalf("prewarm: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := RunFormula(ctx, dir, nil, nil)
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("RunFormula took %s to return after a 5s deadline — process-group kill not effective", elapsed)
	}
	if resp.GetResult().GetState() == runtimev0.TestRunResult_PASSED {
		t.Fatalf("a run killed mid-flight must never be PASSED: %s", resp.GetResult().GetMessage())
	}
	if !strings.Contains(resp.GetResult().GetMessage(), "interrupted") {
		t.Fatalf("want an interrupted-run message, got: %s", resp.GetResult().GetMessage())
	}
}

func TestBuildRunPattern(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"TestFoo"}, "^TestFoo$"},
		{[]string{"TestFoo", "TestBar"}, "^(TestFoo|TestBar)$"},
		{[]string{"TestFoo/case [1]"}, `^TestFoo$/^case \[1\]$`},
		{[]string{"TestA/x", "TestB/y"}, "^(TestA|TestB)$/^(x|y)$"},
		// Mixed depth: constrain the common prefix only (superset, never a miss).
		{[]string{"TestA/x", "TestB"}, "^(TestA|TestB)$"},
	} {
		if got := buildRunPattern(tc.in); got != tc.want {
			t.Errorf("buildRunPattern(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
