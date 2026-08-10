package code

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestDefaultCodeServerDiscoversSupportedAndGenericCodeUnits(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"src/api/go.mod",
		"src/worker/pyproject.toml",
		"src/worker/requirements.txt",
		"src/input-only/requirements.in",
		"src/ads/build.gradle",
		"src/cart/cart.sln",
		"src/cart/src/cart.csproj",
		"src/cart/tests/cart.tests.csproj",
		"src/mixed/go.mod",
		"src/mixed/package.json",
		"src/javascript/package.json",
		"src/javascript/server.js",
		"src/typescript/package.json",
		"src/typescript/browser.js",
		"src/typescript/server.ts",
		"src/nested/package.json",
		"src/nested/app.ts",
		"src/nested/worker/package.json",
		"src/nested/worker/index.mjs",
		"vendor/ignored/go.mod",
		".cache/ignored/pyproject.toml",
	}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("declaration\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_DiscoverCodeUnits{DiscoverCodeUnits: &codev0.DiscoverCodeUnitsRequest{}},
	})
	if err != nil {
		t.Fatalf("discover code units: %v", err)
	}
	units := response.GetDiscoverCodeUnits().GetCodeUnits()
	if len(units) != 10 {
		t.Fatalf("code units = %d (%+v), want 10 complete boundaries", len(units), units)
	}
	want := map[string]struct {
		language, agent string
		languages       []string
		manifests       []string
	}{
		"src/ads":           {language: "jvm", agent: "generic", languages: []string{"jvm"}, manifests: []string{"src/ads/build.gradle"}},
		"src/api":           {language: "go", agent: "go", languages: []string{"go"}, manifests: []string{"src/api/go.mod"}},
		"src/cart":          {language: "dotnet", agent: "generic", languages: []string{"dotnet"}, manifests: []string{"src/cart/cart.sln", "src/cart/src/cart.csproj", "src/cart/tests/cart.tests.csproj"}},
		"src/mixed":         {language: "go", agent: "generic", languages: []string{"go", "typescript"}, manifests: []string{"src/mixed/go.mod", "src/mixed/package.json"}},
		"src/javascript":    {language: "javascript", agent: "nextjs", languages: []string{"javascript"}, manifests: []string{"src/javascript/package.json"}},
		"src/typescript":    {language: "typescript", agent: "nextjs", languages: []string{"typescript"}, manifests: []string{"src/typescript/package.json"}},
		"src/nested":        {language: "typescript", agent: "nextjs", languages: []string{"typescript"}, manifests: []string{"src/nested/package.json"}},
		"src/nested/worker": {language: "javascript", agent: "nextjs", languages: []string{"javascript"}, manifests: []string{"src/nested/worker/package.json"}},
		"src/input-only":    {language: "python", agent: "python", languages: []string{"python"}, manifests: []string{"src/input-only/requirements.in"}},
		"src/worker":        {language: "python", agent: "python", languages: []string{"python"}, manifests: []string{"src/worker/pyproject.toml", "src/worker/requirements.txt"}},
	}
	for _, unit := range units {
		expected, ok := want[unit.GetPath()]
		if !ok {
			t.Fatalf("unexpected unit %+v", unit)
		}
		if unit.GetPrimaryLanguage() != expected.language || unit.GetRuntimeAgent() != expected.agent ||
			!reflect.DeepEqual(unit.GetLanguages(), expected.languages) || !reflect.DeepEqual(unit.GetManifestPaths(), expected.manifests) {
			t.Fatalf("unit %s = %+v, want %+v", unit.GetPath(), unit, expected)
		}
		delete(want, unit.GetPath())
	}
	if len(want) != 0 {
		t.Fatalf("missing units: %+v", want)
	}
}

func TestDefaultCodeServerDiscoversMarkerlessRootAsGeneric(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err := NewDefaultCodeServer(root).Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_DiscoverCodeUnits{DiscoverCodeUnits: &codev0.DiscoverCodeUnitsRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	units := response.GetDiscoverCodeUnits().GetCodeUnits()
	if len(units) != 1 || units[0].GetPath() != "." || units[0].GetPrimaryLanguage() != "unknown" || units[0].GetRuntimeAgent() != "generic" {
		t.Fatalf("markerless units = %+v, want one generic root", units)
	}
}
