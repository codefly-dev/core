package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRealGoWorkspace materializes independent modules whose imports resolve
// only when the source-root go.work is active. The tests below then invoke the
// host Go toolchain through the production runner and formula paths.
func writeRealGoWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.work": `go 1.24.0

use (
	./contracts
	./service
	./grader
)
`,
		"contracts/go.mod": "module example.com/platform/contracts\n\ngo 1.24.0\n",
		"contracts/context.go": `package contracts

type RequestContext struct { RequestID string }
`,
		"service/go.mod": "module example.com/platform/service\n\ngo 1.24.0\n",
		"service/service.go": `package service

import "example.com/platform/contracts"

func RequestID(ctx contracts.RequestContext) string { return ctx.RequestID }
`,
		"service/service_test.go": `package service

import (
	"testing"
	"example.com/platform/contracts"
)

func TestServiceUsesWorkspaceContract(t *testing.T) {
	if got := RequestID(contracts.RequestContext{RequestID: "req-service"}); got != "req-service" {
		t.Fatalf("request ID = %q", got)
	}
}
`,
		"grader/go.mod": "module example.com/platform/grader\n\ngo 1.24.0\n",
		"grader/grader_test.go": `package grader

import (
	"testing"
	"example.com/platform/contracts"
	"example.com/platform/service"
)

func TestGraderCrossesWorkspaceModules(t *testing.T) {
	if got := service.RequestID(contracts.RequestContext{RequestID: "req-grader"}); got != "req-grader" {
		t.Fatalf("request ID = %q", got)
	}
}
`,
	}
	for name, body := range files {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", name, err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func TestRunGoTestsRootOwnedWorkspaceExecutesEveryModule(t *testing.T) {
	ctx := context.Background()
	root := writeRealGoWorkspace(t)
	env, err := NewNativeGoRunner(ctx, root, ".")
	if err != nil {
		t.Fatalf("new native runner: %v", err)
	}
	// false rejects an unrelated parent workspace. It must never disable the
	// go.work that belongs to the attached source root itself.
	env.WithWorkspace(false)
	if err := env.Init(ctx); err != nil {
		t.Fatalf("initialize source-owned workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown workspace runner: %v", err)
		}
	})

	execution, err := RunGoTests(ctx, env, root, nil)
	if err != nil {
		t.Fatalf("RunGoTests: %v\n%s", err, execution.RawOutput)
	}
	if execution.Passed != 2 || execution.Failed != 0 {
		t.Fatalf("counts = passed %d failed %d, want 2/0\n%s", execution.Passed, execution.Failed, execution.RawOutput)
	}
	for _, testName := range []string{"TestServiceUsesWorkspaceContract", "TestGraderCrossesWorkspaceModules"} {
		if !strings.Contains(execution.RawOutput, `"Test":"`+testName+`"`) {
			t.Fatalf("workspace run did not execute %s\n%s", testName, execution.RawOutput)
		}
	}
	if output, err := RunGoBuild(ctx, env, root, nil); err != nil {
		t.Fatalf("build source-owned workspace: %v\n%s", err, output)
	}
	if output, err := RunGoLint(ctx, env, root, nil); err != nil {
		t.Fatalf("lint source-owned workspace: %v\n%s", err, output)
	}
}

func TestRunFormulaRootOwnedWorkspaceHonorsPackageSelector(t *testing.T) {
	root := writeRealGoWorkspace(t)
	cmd, _, ok := DeriveFormula(root)
	if !ok {
		t.Fatal("DeriveFormula did not claim a valid source-root go.work")
	}
	wantScopes := "./contracts/... ./service/... ./grader/..."
	if !strings.Contains(strings.Join(cmd, " "), wantScopes) {
		t.Fatalf("derived formula = %v, want workspace scopes %q", cmd, wantScopes)
	}

	resp, err := RunFormula(formulaCtx(t), root, nil, []string{"./service"})
	if err != nil {
		t.Fatalf("RunFormula: %v", err)
	}
	if resp.GetCounts().GetTotal() != 1 || resp.GetCounts().GetPassed() != 1 {
		t.Fatalf("selector counts = %+v, want only the service test (%s)", resp.GetCounts(), resp.GetResult().GetMessage())
	}
	var caseNames []string
	for _, suite := range resp.GetSuites() {
		for _, testCase := range suite.GetCases() {
			caseNames = append(caseNames, testCase.GetName())
		}
	}
	joinedNames := strings.Join(caseNames, " ")
	if !strings.Contains(joinedNames, "TestServiceUsesWorkspaceContract") || strings.Contains(joinedNames, "TestGraderCrossesWorkspaceModules") {
		t.Fatalf("selector did not narrow the workspace formula: cases=%v", caseNames)
	}
}

func TestSourceOwnedWorkspaceDoesNotExecuteSiblingDependencyModules(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "agent")
	sibling := filepath.Join(parent, "core")
	for _, dir := range []string{source, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	files := map[string]string{
		filepath.Join(source, "go.work"): "go 1.24.0\n\nuse (\n\t.\n\t../core\n)\n",
		filepath.Join(source, "go.mod"):  "module example.com/agent\n\ngo 1.24.0\n",
		filepath.Join(sibling, "go.mod"): "module example.com/core\n\ngo 1.24.0\n",
	}
	for name, body := range files {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	workspace, owned, err := loadSourceGoWorkspace(source)
	if err != nil {
		t.Fatalf("load source workspace: %v", err)
	}
	if !owned {
		t.Fatal("source-owned go.work was not recognized")
	}
	wantSource, err := canonicalExistingDir(source)
	if err != nil {
		t.Fatalf("canonical source: %v", err)
	}
	if len(workspace.moduleDirs) != 1 || workspace.moduleDirs[0] != wantSource {
		t.Fatalf("executable module dirs = %v, want only attached source %s", workspace.moduleDirs, wantSource)
	}
	if got := strings.Join(workspace.packageTargets, " "); got != "./..." {
		t.Fatalf("package targets = %q, want only attached source packages", got)
	}
}
