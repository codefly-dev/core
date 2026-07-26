package proto

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMoveGeneratedOpenAPIPreservesCanonicalSamePath(t *testing.T) {
	canonical := filepath.Join(t.TempDir(), "openapi", "api.swagger.json")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("mkdir canonical directory: %v", err)
	}
	const document = `{"swagger":"2.0"}`
	if err := os.WriteFile(canonical, []byte(document), 0o644); err != nil {
		t.Fatalf("write canonical OpenAPI: %v", err)
	}

	if err := moveGeneratedOpenAPI(context.Background(), canonical, canonical); err != nil {
		t.Fatalf("moveGeneratedOpenAPI same path: %v", err)
	}
	content, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("canonical OpenAPI was deleted: %v", err)
	}
	if string(content) != document {
		t.Fatalf("canonical OpenAPI content = %q, want %q", content, document)
	}
}

func TestMoveGeneratedOpenAPIMovesLegacyPath(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "generated", "api.swagger.json")
	destination := filepath.Join(root, "openapi", "api.swagger.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir source directory: %v", err)
	}
	if err := os.WriteFile(source, []byte("openapi"), 0o644); err != nil {
		t.Fatalf("write generated OpenAPI: %v", err)
	}

	if err := moveGeneratedOpenAPI(context.Background(), source, destination); err != nil {
		t.Fatalf("moveGeneratedOpenAPI legacy path: %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("legacy source still exists or stat failed unexpectedly: %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read canonical OpenAPI: %v", err)
	}
	if string(content) != "openapi" {
		t.Fatalf("canonical OpenAPI content = %q", content)
	}
}

func TestNewBufTracksEveryGenerationInput(t *testing.T) {
	generator, err := NewBuf(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	dependency := generator.dependencies.Components[0]
	for _, path := range []string{
		"proto/api.proto",
		"proto/buf.gen.yaml",
		"proto/buf.yaml",
		"proto/buf.lock",
	} {
		if !dependency.Keep(path) {
			t.Errorf("generation input %q is not tracked", path)
		}
	}
	if dependency.Keep("code/pkg/gen/api.pb.go") {
		t.Fatal("generated output must not invalidate its own input cache")
	}
}

func TestBufCleanGeneratedDirsIsStrictlyScoped(t *testing.T) {
	root := t.TempDir()
	generated := filepath.Join(root, "code", "pkg", "gen")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatalf("mkdir generated directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(generated, "stale.pb.go"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	generator, err := NewBuf(context.Background(), root)
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	generator.WithGeneratedDirs(generated)
	if err := generator.cleanGeneratedDirs(); err != nil {
		t.Fatalf("cleanGeneratedDirs: %v", err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("generated directory still exists or stat failed unexpectedly: %v", err)
	}

	outside := t.TempDir()
	generator.generatedDirs = []string{outside}
	if err := generator.cleanGeneratedDirs(); err == nil {
		t.Fatal("cleanGeneratedDirs accepted an output outside the generator root")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was touched: %v", err)
	}
}

func TestBufCleanGeneratedDirsSupportsNestedProtocolRoots(t *testing.T) {
	serviceRoot := t.TempDir()
	bufRoot := filepath.Join(serviceRoot, "code")
	generated := filepath.Join(serviceRoot, "openapi")
	if err := os.MkdirAll(bufRoot, 0o755); err != nil {
		t.Fatalf("mkdir Buf root: %v", err)
	}
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatalf("mkdir generated directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(generated, "stale.json"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	generator, err := NewBuf(context.Background(), bufRoot)
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	generator.WithGeneratedRoot(serviceRoot).WithGeneratedDirs(generated)
	if err := generator.cleanGeneratedDirs(); err != nil {
		t.Fatalf("cleanGeneratedDirs: %v", err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("generated directory still exists or stat failed unexpectedly: %v", err)
	}

	outside := t.TempDir()
	generator.generatedDirs = []string{outside}
	if err := generator.cleanGeneratedDirs(); err == nil {
		t.Fatal("cleanGeneratedDirs accepted an output outside the service root")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was touched: %v", err)
	}
}
