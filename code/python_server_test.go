package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// newPythonProject materializes a fixture Python project on disk and
// returns a PythonCodeServer rooted at it. Layout exercises the
// recursive package walk: nested subpackages, namespace packages
// (no __init__.py), an intermediate directory with no .py files of its
// own, and skip-set directories that must stay invisible.
func newPythonProject(t *testing.T, files map[string]string) *PythonCodeServer {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return NewPythonCodeServer(dir, nil)
}

func TestPythonProjectInfoReadsPreferredInputRequirements(t *testing.T) {
	server := newPythonProject(t, map[string]string{
		"requirements.in": `requests[security]>=2.0; python_version >= '3.10' \
  --hash=sha256:abcdef1234567890
-r requirements/base.txt
-e git+https://example.test/internal.git#egg=internal_lib
remote-wheel @ https://example.test/remote.whl; python_version >= '3.11'
`,
		"requirements.txt":      "transitive-package==99.0\n",
		"requirements/base.txt": "pydantic==2.9.0\nRequests==9.9.9\n",
		"app.py":                "import requests\n",
	})
	response, err := server.Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetFailure() != nil {
		t.Fatalf("project info failure = %+v", response.GetFailure())
	}
	var got []string
	for _, dependency := range response.GetGetProjectInfo().GetDependencies() {
		got = append(got, dependency.GetName()+"@"+dependency.GetVersion())
		if !dependency.GetDirect() {
			t.Fatalf("declared dependency is not direct: %+v", dependency)
		}
	}
	want := []string{
		"requests@>=2.0",
		"pydantic@==2.9.0",
		"internal_lib@git+https://example.test/internal.git",
		"remote-wheel@https://example.test/remote.whl",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("declared dependencies = %#v, want %#v", got, want)
	}
}

func TestPythonProjectInfoReadsRequirementsTxtOnlyProject(t *testing.T) {
	server := newPythonProject(t, map[string]string{
		"requirements.txt": "flask==3.1.2\n",
		"app.py":           "import flask\n",
	})
	response, err := server.Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dependencies := response.GetGetProjectInfo().GetDependencies()
	if response.GetFailure() != nil || len(dependencies) != 1 || dependencies[0].GetName() != "flask" || dependencies[0].GetVersion() != "==3.1.2" {
		t.Fatalf("requirements.txt project info = %+v failure=%+v", response.GetGetProjectInfo(), response.GetFailure())
	}
}

func TestPythonProjectInfoReturnsTypedPartialFailureForMissingRequirementInclude(t *testing.T) {
	server := newPythonProject(t, map[string]string{
		"requirements.in": "requests==2.32.5\n-r requirements/missing.txt\n",
		"app.py":          "import requests\n",
	})
	response, err := server.Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := response.GetGetProjectInfo()
	if info == nil || len(info.GetDependencies()) != 1 || info.GetDependencies()[0].GetName() != "requests" {
		t.Fatalf("partial project evidence = %+v", info)
	}
	if response.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_VALIDATION_FAILED ||
		!strings.Contains(response.GetFailure().GetMessage(), "requirements/missing.txt") {
		t.Fatalf("typed requirement failure = %+v", response.GetFailure())
	}
}

// packagesByPath indexes a GetProjectInfo package list by RelativePath.
func packagesByPath(t *testing.T, s *PythonCodeServer) map[string]pkgView {
	t.Helper()
	response, err := s.Execute(context.Background(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}}})
	if err != nil {
		t.Fatalf("GetProjectInfo: %v", err)
	}
	info := response.GetGetProjectInfo()
	if info == nil || response.GetFailure() != nil {
		t.Fatalf("GetProjectInfo response: %+v", response)
	}
	out := make(map[string]pkgView, len(info.Packages))
	for _, p := range info.Packages {
		out[p.RelativePath] = pkgView{name: p.Name, files: p.Files, imports: p.Imports}
	}
	return out
}

type pkgView struct {
	name    string
	files   []string
	imports []string
}

func (p pkgView) hasImport(name string) bool {
	for _, imp := range p.imports {
		if imp == name {
			return true
		}
	}
	return false
}

func TestPythonDiscoverPackages_NestedSubpackages(t *testing.T) {
	s := newPythonProject(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"fixture\"\n",
		// Root-level script → the "." package.
		"main.py": "import flask\n",
		// Top-level package.
		"app/__init__.py": "import requests\n",
		// Nested subpackage — invisible before the recursive walk.
		"app/api/__init__.py": "",
		"app/api/routes.py":   "from fastapi import APIRouter\nimport numpy.linalg\nfrom . import models\n",
		// Doubly-nested namespace subpackage (no __init__.py).
		"app/api/v2/handlers.py": "import pandas\n",
		// Intermediate dir with no .py of its own — must be descended
		// into but NOT emitted as a package.
		"tools/scripts/run.py": "import click\n",
		// Skip set: never surfaced.
		"venv/lib/site.py":     "import evil\n",
		"__pycache__/junk.py":  "import junk\n",
		".hidden/secret.py":    "import secret\n",
		"node_modules/x/x.py":  "import x\n",
		"app/__pycache__/c.py": "import cached\n",
	})

	pkgs := packagesByPath(t, s)

	// Nested subpackages surface with correct RelativePath.
	for _, want := range []string{".", "app", "app/api", "app/api/v2", "tools/scripts"} {
		if _, ok := pkgs[want]; !ok {
			t.Errorf("missing package %q; got %v", want, pkgPaths(pkgs))
		}
	}
	// Skip-set and intermediate dirs must not be emitted.
	for _, absent := range []string{"venv", "venv/lib", "__pycache__", ".hidden", "node_modules/x", "app/__pycache__", "tools"} {
		if _, ok := pkgs[absent]; ok {
			t.Errorf("package %q should not be emitted", absent)
		}
	}

	// Name is the dotted module path for nested packages.
	if got := pkgs["app/api"].name; got != "app.api" {
		t.Errorf("app/api Name = %q, want %q", got, "app.api")
	}

	// Files are direct children only (no recursion into subpackages).
	api := pkgs["app/api"]
	if len(api.files) != 2 || !pkgHasFile(api.files, "__init__.py") || !pkgHasFile(api.files, "routes.py") {
		t.Errorf("app/api Files = %v, want [__init__.py routes.py]", api.files)
	}

	// Imports aggregate per package dir, at the top-level dotted
	// component, with relative imports skipped.
	if !api.hasImport("fastapi") {
		t.Errorf("app/api imports missing fastapi: %v", api.imports)
	}
	if !api.hasImport("numpy") {
		t.Errorf("app/api imports missing numpy (top-level of numpy.linalg): %v", api.imports)
	}
	if api.hasImport("models") || api.hasImport(".") {
		t.Errorf("relative import leaked into app/api imports: %v", api.imports)
	}
	if v2 := pkgs["app/api/v2"]; !v2.hasImport("pandas") {
		t.Errorf("app/api/v2 imports missing pandas: %v", v2.imports)
	}
	if root := pkgs["."]; !root.hasImport("flask") {
		t.Errorf("root package imports missing flask: %v", root.imports)
	}
	if scripts := pkgs["tools/scripts"]; !scripts.hasImport("click") {
		t.Errorf("tools/scripts imports missing click: %v", scripts.imports)
	}
	// The parent package must NOT absorb its subpackages' imports —
	// aggregation is per package dir.
	if app := pkgs["app"]; app.hasImport("pandas") || app.hasImport("fastapi") {
		t.Errorf("app package absorbed subpackage imports: %v", app.imports)
	}
}

// TestPythonDiscoverPackages_DepthCap asserts the recursion stops at
// pythonPackageWalkMaxDepth so pathological trees stay bounded.
func TestPythonDiscoverPackages_DepthCap(t *testing.T) {
	files := map[string]string{}
	rel := ""
	for i := 0; i < pythonPackageWalkMaxDepth+2; i++ {
		if rel == "" {
			rel = "d"
		} else {
			rel += "/d"
		}
		files[rel+"/mod.py"] = "import os\n"
	}
	s := newPythonProject(t, files)
	pkgs := packagesByPath(t, s)

	atCap := strings.TrimSuffix(strings.Repeat("d/", pythonPackageWalkMaxDepth), "/")
	if _, ok := pkgs[atCap]; !ok {
		t.Errorf("package at depth cap %q should be discovered; got %v", atCap, pkgPaths(pkgs))
	}
	beyond := atCap + "/d"
	if _, ok := pkgs[beyond]; ok {
		t.Errorf("package beyond depth cap %q should NOT be discovered", beyond)
	}
}

func pkgPaths(m map[string]pkgView) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func pkgHasFile(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
